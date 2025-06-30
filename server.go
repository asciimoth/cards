package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func dict(values ...interface{}) (map[string]interface{}, error) {
	if len(values)%2 != 0 {
		return nil, errors.New("invalid dict call: must have even number of args")
	}
	m := make(map[string]interface{}, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, errors.New("dict keys must be strings")
		}
		m[key] = values[i+1]
	}
	return m, nil
}

func SetupServer(log *logrus.Logger, localizer func(string, string) string) (*gin.Engine, *http.Server) {
	str_ttl := os.Getenv("COOKIE_TTL")
	cookie_ttl := 24
	if str_ttl == "" {
		log.Warnf("Cookies TTL not set; using default value %d hours", cookie_ttl)
	} else {
		ttl, err := strconv.Atoi(str_ttl)
		cookie_ttl = ttl
		if err != nil {
			log.Fatalf("Failed to parse cooie ttl: %s", str_ttl)
		}
	}

	host, port := os.Getenv("GIN_HOST"), os.Getenv("GIN_PORT")
	if host == "" {
		host = "0.0.0.0"
	}
	if port == "" {
		port = "8080"
	}
	addr := fmt.Sprintf("%s:%s", host, port)

	log.WithFields(logrus.Fields{
		"addr": addr,
	}).Debug("Setting up server")

	gin.DefaultWriter = log.WriterLevel(logrus.InfoLevel)
	gin.DefaultErrorWriter = log.WriterLevel(logrus.ErrorLevel)

	gin.SetMode(gin.ReleaseMode)

	g := gin.New()

	g.Use(gin.LoggerWithWriter(log.Writer()))
	g.Use(gin.RecoveryWithWriter(log.Writer()))

	// Serve static files from local filesystem
	// Exposes ./static directory at /static URL path
	//g.Static("/static", "./static")

	// Parse and set HTML templates from local filesystem
	tmpl := template.Must(template.New("").Funcs(template.FuncMap{
		"T":    localizer,
		"dict": dict,
	}).ParseGlob("templates/*.html"))
	g.SetHTMLTemplate(tmpl)

	store := cookie.NewStore([]byte(os.Getenv("SESSION_SECRET")))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   3600 * cookie_ttl,
		HttpOnly: os.Getenv("GO_ENV") == "debug",
		Secure:   os.Getenv("GO_ENV") != "debug",
		SameSite: http.SameSiteLaxMode,
	})
	g.Use(sessions.Sessions("login", store))

	srv := &http.Server{Addr: addr, Handler: g}
	return g, srv
}

func RunServer(srv *http.Server, wg *sync.WaitGroup, ctx context.Context, log *logrus.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("Starting server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Server error")
		}
	}()

	go func() {
		<-ctx.Done()

		log.Info("Stopping server")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Server graceful shutdown failed")
		} else {
			log.Info("Server stopped gracefully")
		}
	}()
}
