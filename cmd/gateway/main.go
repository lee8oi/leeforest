package main

import (
	"flag"
	"log"

	"github.com/lee8oi/leeforest/internal/config"
	"github.com/lee8oi/leeforest/internal/server"
)

// leeforest gateway
func main() {
	configPath := flag.String("config", "/opt/leeforest/config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
