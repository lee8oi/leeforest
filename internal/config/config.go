package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all gateway configuration loaded from JSON.
type Config struct {
	Domain       string     `json:"domain"`
	StaticRoot   string     `json:"static_root"`
	CertCache    string     `json:"cert_cache"`
	ListenHTTPS  string     `json:"listen_https"`
	ListenHTTP   string     `json:"listen_http"`
	RedirectHTTP bool       `json:"redirect_http"`
	Sites        []Site     `json:"sites"`
	APIRoutes    []APIRoute `json:"api_routes"`
}

// Site defines a subdomain reverse proxy target.
type Site struct {
	Hostname     string `json:"hostname"`
	UpstreamPort int    `json:"upstream_port"`
	BinaryPath   string `json:"binary_path"`
}

// APIRoute defines a path-based reverse proxy target.
type APIRoute struct {
	Path         string `json:"path"`
	UpstreamPort int    `json:"upstream_port"`
	StripPrefix  bool   `json:"strip_prefix"`
}

// Load reads and parses a JSON config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Domain == "" {
		return fmt.Errorf("domain is required")
	}
	if c.StaticRoot == "" {
		return fmt.Errorf("static_root is required")
	}
	if c.CertCache == "" {
		return fmt.Errorf("cert_cache is required")
	}
	if c.ListenHTTPS == "" {
		c.ListenHTTPS = ":443"
	}
	if c.ListenHTTP == "" {
		c.ListenHTTP = ":80"
	}

	// Validate sites
	seenHosts := make(map[string]bool)
	for i, site := range c.Sites {
		if site.Hostname == "" {
			return fmt.Errorf("sites[%d]: hostname is required", i)
		}
		if site.UpstreamPort <= 0 || site.UpstreamPort > 65535 {
			return fmt.Errorf("sites[%d] (%s): upstream_port must be between 1 and 65535", i, site.Hostname)
		}
		if seenHosts[site.Hostname] {
			return fmt.Errorf("sites[%d] (%s): duplicate hostname", i, site.Hostname)
		}
		seenHosts[site.Hostname] = true
	}

	// Validate API routes
	seenPaths := make(map[string]bool)
	for i, route := range c.APIRoutes {
		if route.Path == "" {
			return fmt.Errorf("api_routes[%d]: path is required", i)
		}
		if route.UpstreamPort <= 0 || route.UpstreamPort > 65535 {
			return fmt.Errorf("api_routes[%d] (%s): upstream_port must be between 1 and 65535", i, route.Path)
		}
		if seenPaths[route.Path] {
			return fmt.Errorf("api_routes[%d] (%s): duplicate path", i, route.Path)
		}
		seenPaths[route.Path] = true
	}

	return nil
}
