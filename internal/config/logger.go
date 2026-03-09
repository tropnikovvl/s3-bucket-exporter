package config

import (
	"os"

	log "github.com/sirupsen/logrus"
)

func (c *Config) SetupLogger() {
	if c.LogFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	} else {
		log.SetFormatter(&log.TextFormatter{})
	}

	level, err := log.ParseLevel(c.LogLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %s", c.LogLevel)
	}
	log.SetLevel(level)
	log.SetOutput(os.Stdout)
}
