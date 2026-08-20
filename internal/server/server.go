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

const (
	pidFilePath = "/opt/leeforest/leeforest.pid"
	configPath  = "/opt/leeforest/config.json"
)

// Server manages the HTTPS and HTTP listeners with Autocert.
type Server struct {
	cfg     *config.Config
	router  *router.Router
	apps    map[string]*appInfo // binary_path -> app info
	appCtx  context.Context
	appCancel context.CancelFunc
	mu      sync.RWMutex
}

type appInfo struct {
	cancel context.CancelFunc
	pid    int
}

// New creates a Server from config.
func New(cfg *config.Config) *Server {
	return &Server{
		cfg:    cfg,
		router: router.New(cfg),
		apps:   make(map[string]*appInfo),
	}
}

// spawnApp runs a child binary and restarts it if it exits, with exponential
// backoff on repeated failures. It exits when ctx is cancelled.
func (s *Server) spawnApp(ctx context.Context, binary string, wg *sync.WaitGroup) *appInfo {
	info := &appInfo{}
	
	s.mu.Lock()
	s.apps[binary] = info
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.apps, binary)
		s.mu.Unlock()
		wg.Done()
	}()

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping app supervisor for %s", binary)
			return info
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

		info.pid = cmd.Process.Pid
		log.Printf("Started app: %s (PID %d)", binary, cmd.Process.Pid)

		err = cmd.Wait()

		// Check if we're shutting down
		if ctx.Err() != nil {
			log.Printf("App %s stopped during shutdown", binary)
			return info
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

// stopApp gracefully terminates a running child app.
func (s *Server) stopApp(binary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.apps[binary]
	if !ok {
		return fmt.Errorf("app not found: %s", binary)
	}

	log.Printf("Stopping app: %s (PID %d)", binary, info.pid)
	info.cancel()
	return nil
}

// reconcileApps compares config.Sites against running apps and starts/stops as needed.
// Uses the provided ctx for the entire lifetime of the apps, not just reconciliation.
func (s *Server) reconcileApps(ctx context.Context) error {
	s.mu.RLock()
	runningApps := make(map[string]bool)
	for binary := range s.apps {
		runningApps[binary] = true
	}
	s.mu.RUnlock()

	requiredApps := make(map[string]bool)

	for _, site := range s.cfg.Sites {
		if site.BinaryPath == "" {
			continue
		}
		requiredApps[site.BinaryPath] = true

		if runningApps[site.BinaryPath] {
			continue
		}

		// Start new app
		go s.spawnApp(ctx, site.BinaryPath, nil)
	}

	// Stop removed apps
	for binary := range runningApps {
		if !requiredApps[binary] {
			if err := s.stopApp(binary); err != nil {
				log.Printf("Failed to stop app %s: %v", binary, err)
			}
		}
	}

	// Wait briefly for new apps to start
	time.Sleep(500 * time.Millisecond)

	return nil
}

// writePIDFile writes the current process PID to the PID file.
func (s *Server) writePIDFile() error {
	return os.WriteFile(pidFilePath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

// reload loads a fresh config and reconciles child apps.
func (s *Server) reload() error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	// Update router with new config
	newRouter := router.New(cfg)
	s.router = newRouter

	// Create new context for apps (previous context will be abandoned)
	newAppCtx, newAppCancel := context.WithCancel(context.Background())
	
	s.mu.Lock()
	s.appCtx = newAppCtx
	s.appCancel = newAppCancel
	s.mu.Unlock()
	
	// Stop old apps
	if s.appCtx != nil {
		for binary := range s.apps {
			if err := s.stopApp(binary); err != nil {
				log.Printf("Failed to stop old app %s: %v", binary, err)
			}
		}
	}

	// Reconcile child apps using the new context
	if err := s.reconcileApps(newAppCtx); err != nil {
		return fmt.Errorf("reconciling apps: %w", err)
	}

	log.Printf("Config reloaded. Active sites:")
	for _, site := range cfg.Sites {
		if site.Hostname != "" {
			log.Printf("  - %s (port %d)", site.Hostname, site.UpstreamPort)
		}
	}

	return nil
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
	// Write PID file
	if err := s.writePIDFile(); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer os.Remove(pidFilePath)

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

	// Create app context that lives for the server lifetime
	appCtx, appCancel := context.WithCancel(context.Background())
	s.appCtx = appCtx
	s.appCancel = appCancel

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

	// Spawn initial child apps
	var initialWg sync.WaitGroup
	for _, site := range s.cfg.Sites {
		if site.BinaryPath != "" {
			initialWg.Add(1)
			go s.spawnApp(appCtx, site.BinaryPath, &initialWg)
		}
	}

	// Setup shutdown signal handling
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

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

	// Wait for signals in a loop for SIGHUP
	for {
		sig := <-shutdownChan
		switch sig {
		case syscall.SIGHUP:
			log.Println("Received SIGHUP, reloading config...")
			if err := s.reload(); err != nil {
				log.Printf("Config reload failed: %v", err)
			}
			// Continue looping to wait for more signals
		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf("Received signal %v, shutting down...", sig)
			goto shutdown
		}
	}

shutdown:
	// Cancel child apps
	s.appCancel()
	initialWg.Wait()
	log.Println("All child apps stopped.")

	// Shut down HTTP servers with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	httpServer.Shutdown(ctx)
	httpsServer.Shutdown(ctx)

	log.Println("Shutdown complete.")
	return nil
}
