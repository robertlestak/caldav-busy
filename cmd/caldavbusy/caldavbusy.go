package main

import (
	"flag"
	"os"

	"github.com/robertlestak/caldav-busy/internal/config"
	"github.com/robertlestak/caldav-busy/internal/server"
	log "github.com/sirupsen/logrus"
)

func init() {
	// Try to load config to set log level
	if cfg, err := config.Load(); err == nil {
		if ll, err := log.ParseLevel(cfg.Server.LogLevel); err == nil {
			log.SetLevel(ll)
		}
	} else {
		// Fallback to environment variable or default
		ll, err := log.ParseLevel(os.Getenv("LOG_LEVEL"))
		if err != nil {
			ll = log.InfoLevel
		}
		log.SetLevel(ll)
	}
}

func main() {
	l := log.WithFields(log.Fields{
		"module": "caldavbusy",
		"fn":     "main",
	})

	addr := flag.String("addr", "", "address to listen on (overrides config)")
	configFile := flag.String("config", "config.yaml", "path to configuration file")
	createExample := flag.Bool("create-example", false, "create example configuration file")
	flag.Parse()

	if *createExample {
		if err := config.CreateExample(*configFile); err != nil {
			l.WithError(err).Fatal("failed to create example configuration")
		}
		l.WithField("file", *configFile).Info("created example configuration file")
		return
	}

	l.Info("starting caldav-busy proxy")

	// Load configuration from specified file
	cfg, err := config.LoadFromFile(*configFile)
	if err != nil {
		l.WithError(err).Fatal("failed to load configuration")
	}

	// Override address if provided
	if *addr != "" {
		cfg.Server.Address = *addr
	}

	// Update log level from config
	if ll, err := log.ParseLevel(cfg.Server.LogLevel); err == nil {
		log.SetLevel(ll)
	}

	server, err := server.NewServer(cfg)
	if err != nil {
		l.WithError(err).Fatal("failed to create server")
	}

	if err := server.Start(); err != nil {
		l.WithError(err).Fatal("failed to start server")
	}
}
