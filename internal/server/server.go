package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/lee8oi/leeforest/internal/config"
	"github.com/lee8oi/leeforest/internal/router"
)

// Server manages the HTTPS and HTTP listeners with Autocert.
type Server struct {
	cfg    *config.Config
	router *router.Router
}

// New creates a Server from config.
func New(cfg *config.Config) *Server {
	return &Server{
		cfg:    cfg,
		router: router.New(cfg),
	}
}

// spawnApp runs a child binary and restarts it if it exits, with exponential
// backoff on repeated failures. It exits when ctx is cancelled.
func (s *Server) spawnApp(ctx context.Context, binary string, wg *sync.WaitGroup) {
	defer wg.Done()

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping app supervisor for %s", binary)
			return
		default:
		}

		cmd := exec.CommandContext(ctx, binary)

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("Failed to create stdout pipe for %s: %v", binary, err)
			sleepWithContext(ctx, backoff)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			log.Printf("Failed to create stderr pipe for %s: %v", binary, err)
			sleepWithContext(ctx, backoff)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		go io.Copy(os.Stdout, stdoutPipe)
		go io.Copy(os.Stderr, stderrPipe)

		if err := cmd.Start(); err != nil {
			log.Printf("Failed to start %s: %v", binary, err)
			sleepWithContext(ctx, backoff)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		log.Printf("Started app: %s (PID %d)", binary, cmd.Process.Pid)

		err = cmd.Wait()

		// Check if we're shutting down
		if ctx.Err() != nil {
			log.Printf("App %s stopped during shutdown", binary)
			return
		}

		if err != nil {
			log.Printf("%s exited with error: %v", binary, err)
		} else {
			log.Printf("%s exited cleanly", binary)
		}

		log.Printf("Restarting %s in %v...", binary, backoff)
		sleepWithContext(ctx, backoff)
		backoff = nextBackoff(backoff, maxBackoff)
	}
}

// sleepWithContext blocks for d, but returns early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// nextBackoff doubles the backoff duration up to max.
func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

// Start launches both HTTP and HTTPS listeners. Blocks until stopped.
func (s *Server) Start() error {
	// Ensure cert cache directory exists
	if err := os.MkdirAll(s.cfg.CertCache, 0700); err != nil {
		return fmt.Errorf("creating cert cache dir: %w", err)
	}

	// Build host list from config for Autocert whitelist
	hosts := []string{s.cfg.Domain, "www." + s.cfg.Domain}
	for _, site := range s.cfg.Sites {
		if site.Hostname != "" {
			hosts = append(hosts, site.Hostname)
		}
	}

	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(s.cfg.CertCache),
		HostPolicy: autocert.HostWhitelist(hosts...),
	}

	// HTTPS server with Autocert TLS config
	httpsServer := &http.Server{
		Addr:         s.cfg.ListenHTTPS,
		Handler:      s.router,
		TLSConfig:    manager.TLSConfig(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// HTTP server: ACME challenges pass through, everything else redirects
	var httpHandler http.Handler
	if s.cfg.RedirectHTTP {
		httpHandler = manager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}))
	} else {
		httpHandler = manager.HTTPHandler(nil)
	}

	httpServer := &http.Server{
		Addr:         s.cfg.ListenHTTP,
		Handler:      httpHandler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// Spawn child apps with cancellable context
	appCtx, appCancel := context.WithCancel(context.Background())
	var appWg sync.WaitGroup
	for _, site := range s.cfg.Sites {
		if site.BinaryPath != "" {
			appWg.Add(1)
			go s.spawnApp(appCtx, site.BinaryPath, &appWg)
		}
	}

	// Setup shutdown signal handling
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Shared error channel for server errors
	errChan := make(chan error, 2)

	// Start HTTP listener
	go func() {
		log.Printf("HTTP listening on %s", s.cfg.ListenHTTP)
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Start HTTPS listener
	go func() {
		log.Printf("HTTPS listening on %s", s.cfg.ListenHTTPS)
		err := httpsServer.ListenAndServeTLS("", "")
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("HTTPS server error: %w", err)
		}
	}()

	// Wait for shutdown signal or fatal server error
	select {
	case sig := <-shutdownChan:
		log.Printf("Received signal %v, shutting down...", sig)
	case err := <-errChan:
		log.Printf("Server error: %v", err)
	}

	// Cancel child app context first — signals them to stop and return
	appCancel()
	appWg.Wait()
	log.Println("All child apps stopped.")

	// Shut down HTTP servers with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	httpServer.Shutdown(ctx)
	httpsServer.Shutdown(ctx)

	log.Println("Shutdown complete.")
	return nil
}
