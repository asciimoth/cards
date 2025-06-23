package main

import (
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

func SetupLogger() *logrus.Logger {
	logger := logrus.New()
	lvl := os.Getenv("LOG_LEVEL")
	switch strings.ToLower(lvl) {
	case "trace":
		logger.SetLevel(logrus.TraceLevel)
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "info", "":
		logger.SetLevel(logrus.InfoLevel)
	case "warn", "warning":
		logger.SetLevel(logrus.WarnLevel)
	case "error":
		logger.SetLevel(logrus.ErrorLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
		logger.Warnf("Invalid LOG_LEVEL '%s'; Using INFO", lvl)
	}
	logger.SetLevel(logrus.TraceLevel)
	logger.SetOutput(os.Stdout)
	return logger
}
