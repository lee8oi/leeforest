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
	Hostname    string `json:"hostname"`
	Upstream    string `json:"upstream"`
	StripPrefix bool   `json:"strip_prefix"`
}

// APIRoute defines a path-based reverse proxy target.
type APIRoute struct {
	Path        string `json:"path"`
	Upstream    string `json:"upstream"`
	StripPrefix bool   `json:"strip_prefix"`
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
	return nil
}
