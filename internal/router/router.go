package router

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/lee8oi/leeforest/internal/config"
)

// Router wraps an http.ServeMux with vhost and API route awareness.
type Router struct {
	mux        *http.ServeMux
	staticRoot string
	mu         sync.RWMutex
	sites      map[string]*httputil.ReverseProxy
	apiRoutes  []apiEntry
}

type apiEntry struct {
	prefix      string
	proxy       *httputil.ReverseProxy
	stripPrefix bool
}

// New creates a Router configured from the given Config.
func New(cfg *config.Config) *Router {
	r := &Router{
		mux:        http.NewServeMux(),
		staticRoot: cfg.StaticRoot,
		sites:      make(map[string]*httputil.ReverseProxy),
	}

	r.build(cfg)
	r.mux.HandleFunc("/", r.handle)

	return r
}

// build constructs the proxy map and API routes from config.
func (r *Router) build(cfg *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.staticRoot = cfg.StaticRoot
	r.sites = make(map[string]*httputil.ReverseProxy)

	// Build subdomain proxy map
	for _, site := range cfg.Sites {
		target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", site.UpstreamPort))
		if err != nil {
			continue
		}
		r.sites[site.Hostname] = newProxy(target)
	}

	// Build API route entries
	r.apiRoutes = nil
	for _, route := range cfg.APIRoutes {
		target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", route.UpstreamPort))
		if err != nil {
			continue
		}
		r.apiRoutes = append(r.apiRoutes, apiEntry{
			prefix:      route.Path,
			proxy:       newProxy(target),
			stripPrefix: route.StripPrefix,
		})
	}
}

// Reload updates the router with new config. Thread-safe.
func (r *Router) Reload(cfg *config.Config) {
	r.build(cfg)
}

// newProxy creates a ReverseProxy that sets X-Forwarded-For with the real client IP.
func newProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
			if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
				req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
			} else {
				req.Header.Set("X-Forwarded-For", clientIP)
			}
		}
	}
	return proxy
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) handle(w http.ResponseWriter, req *http.Request) {
	// 1. Check subdomain proxies first
	host := strings.SplitN(req.Host, ":", 2)[0]
	
	r.mu.RLock()
	proxy, ok := r.sites[host]
	if ok {
		r.mu.RUnlock()
		proxy.ServeHTTP(w, req)
		return
	}
	r.mu.RUnlock()

	// 2. Check API path routes
	r.mu.RLock()
	apiRoutes := make([]apiEntry, len(r.apiRoutes))
	copy(apiRoutes, r.apiRoutes)
	r.mu.RUnlock()

	for _, entry := range apiRoutes {
		if strings.HasPrefix(req.URL.Path, entry.prefix) {
			if entry.stripPrefix {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, entry.prefix)
			}
			entry.proxy.ServeHTTP(w, req)
			return
		}
	}

	// 3. Fall back to static file server
	fs := http.FileServer(http.Dir(r.staticRoot))
	fs.ServeHTTP(w, req)
}
