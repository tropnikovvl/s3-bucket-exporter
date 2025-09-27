package config

import (
	"os"

	log "github.com/sirupsen/logrus"
)

func SetupLogger() {
	if LogFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	} else {
		log.SetFormatter(&log.TextFormatter{})
	}

	level, err := log.ParseLevel(LogLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %s", LogLevel)
	}
	log.SetLevel(level)

	log.SetOutput(os.Stdout)
}
