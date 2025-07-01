package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	glog "gorm.io/gorm/logger"
)

// Custom logrus based logger for gorm
type gormlog struct {
	log *logrus.Logger
}

func (gl gormlog) LogMode(lvl glog.LogLevel) glog.Interface {
	return gl
}

func (gl gormlog) Info(_ context.Context, s string, v ...any) {
	gl.log.Infof(s, v...)
}

func (gl gormlog) Warn(_ context.Context, s string, v ...any) {
	gl.log.Warnf(s, v...)
}

func (gl gormlog) Error(_ context.Context, s string, v ...any) {
	gl.log.Errorf(s, v...)
}

func (gl gormlog) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
}

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
