package main

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func errorPage(c *gin.Context, status int, text string) {
	if text == "" {
		text = http.StatusText(status)
	}
	c.HTML(status, "error.html", gin.H{
		"Code": status,
		"Text": text,
	})
}

func SetupRoutes(g *gin.Engine, ctx context.Context, storage *BlobStorage, db *PGDB, log *logrus.Logger) {
	g.NoRoute(func(c *gin.Context) {
		errorPage(c, http.StatusNotFound, "")
	})
}
