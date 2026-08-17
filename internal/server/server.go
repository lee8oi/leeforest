package server

import (
	"context"
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

// spawnApp runs a child binary and restarts it if it exits.
func (s *Server) spawnApp(binary string, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		cmd := exec.Command(binary)
		stdoutPipe, _ := cmd.StdoutPipe()
		stderrPipe, _ := cmd.StderrPipe()

		go io.Copy(os.Stdout, stdoutPipe)
		go io.Copy(os.Stderr, stderrPipe)

		if err := cmd.Start(); err != nil {
			log.Printf("Failed to start %s: %v", binary, err)
			time.Sleep(5 * time.Second)
			continue
		}

		log.Printf("Started app: %s (PID %d)", binary, cmd.Process.Pid)
		cmd.Wait()
		log.Printf("%s exited, restarting in 1s...", binary)
		time.Sleep(time.Second)
	}
}

// Start launches both HTTP and HTTPS listeners. Blocks until stopped.
func (s *Server) Start() error {
	// Ensure cert cache directory exists
	if err := os.MkdirAll(s.cfg.CertCache, 0700); err != nil {
		return fmt.Errorf("creating cert cache dir: %w", err)
	}

	// Configure Autocert manager
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(s.cfg.CertCache),
		HostPolicy: autocert.HostWhitelist(s.cfg.Domain, "www."+s.cfg.Domain),
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

	// Spawn child apps
	var appWg sync.WaitGroup
	for _, site := range s.cfg.Sites {
		if site.BinaryPath != "" {
			appWg.Add(1)
			go s.spawnApp(site.BinaryPath, &appWg)
		}
	}

	// Setup shutdown signal handling
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP listener
	go func() {
		log.Printf("HTTP listening on %s", s.cfg.ListenHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start HTTPS listener
	go func() {
		log.Printf("HTTPS listening on %s", s.cfg.ListenHTTPS)
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTPS server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-shutdownChan

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Println("Shutting down servers...")
	httpServer.Shutdown(ctx)
	httpsServer.Shutdown(ctx)

	return nil
}
