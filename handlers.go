package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/markbates/goth/gothic"
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

func sessionMiddleware(log *logrus.Logger, db *PGDB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("User", nil)
		user := User{
			ID:         0,
			ProviderID: "",
			Name:       "",
		}
		sess := sessions.Default(c)
		str_uid := sess.Get("user_id")
		if str_uid == nil {
			c.Next()
			return
		}
		uid, err := strconv.ParseUint(str_uid.(string), 10, 64)
		if err != nil {
			log.Error("Request with broken uid; failed to convert to uint")
			sess.Clear()
			sess.Save()
			c.Redirect(http.StatusTemporaryRedirect, "/")
			return
		}
		user.ID = uint(uid)
		err = db.GetUser(&user)
		if err != nil {
			log.WithFields(logrus.Fields{
				"uid": user.ID,
			}).Error("Broken user session")
			sess.Clear()
			sess.Save()
			c.Redirect(http.StatusTemporaryRedirect, "/")
			return
		}
		c.Set("User", &user)
		c.Next()
	}
}

func getUser(c *gin.Context) *User {
	user := c.MustGet("User")
	if user == nil {
		return nil
	}
	return user.(*User)
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := getUser(c)
		if user == nil {
			c.Redirect(http.StatusTemporaryRedirect, "/")
			return
		}
		c.Next()
	}
}

func getUintParam(c *gin.Context, name string) (uint, error) {
	sv := c.Params.ByName(name)
	iv, err := strconv.ParseUint(sv, 10, 64)
	return uint(iv), err
}

func SetupRoutes(g *gin.Engine, ctx context.Context, storage *BlobStorage, db *PGDB, log *logrus.Logger, providers []string) {
	g.Use(sessionMiddleware(log, db))

	g.NoRoute(func(c *gin.Context) {
		errorPage(c, http.StatusNotFound, "")
	})

	g.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"Title": "Cards",
			"User":  getUser(c),
		})
	})
	g.GET("/faq", func(c *gin.Context) {
		c.HTML(http.StatusOK, "faq.html", gin.H{
			"Title": "Faq",
			"User":  getUser(c),
		})
	})
	g.GET("/tutorial", func(c *gin.Context) {
		c.HTML(http.StatusOK, "tutorial.html", gin.H{
			"Title": "Tutorial",
			"User":  getUser(c),
		})
	})

	g.GET("/c/:id", func(c *gin.Context) {
		cid, err := getUintParam(c, "id")
		if err != nil {
			errorPage(c, http.StatusBadRequest, "Invalid card id")
			return
		}

		user := getUser(c)

		card, err := db.GetCard(cid)
		if err != nil {
			c.HTML(http.StatusNotFound, "cardNotFound.html", gin.H{"User": user})
			return
		}

		is_owner := false
		if user != nil {
			is_owner = card.Owner == user.ID
		}
		if !is_owner && card.Fields.IsHidden {
			c.HTML(http.StatusNotFound, "cardNotFound.html", gin.H{"User": user})
		}
		c.HTML(http.StatusOK, "card.html", gin.H{
			"Title":   card.Fields.Name,
			"Card":    card,
			"User":    user,
			"Owner":   is_owner,
			"EditUrl": fmt.Sprintf("/edit/%d", cid),
			"Avatar":  fmt.Sprintf("/media/avatar/%d", cid),
			"Logo":    fmt.Sprintf("/media/logo/%d", cid),
		})
	})

	// OAuth related handlers
	{
		oauth := g.Group("/")
		oauth.GET("/auth/:provider", func(c *gin.Context) {
			provider := c.Param("provider")
			q := c.Request.URL.Query()
			q.Add("provider", provider)
			c.Request.URL.RawQuery = q.Encode()

			gothic.BeginAuthHandler(c.Writer, c.Request)
		})
		oauth.GET("/auth/:provider/callback", func(c *gin.Context) {
			provider := c.Param("provider")
			q := c.Request.URL.Query()
			q.Add("provider", provider)
			c.Request.URL.RawQuery = q.Encode()

			user, err := gothic.CompleteUserAuth(c.Writer, c.Request)
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to complete auth")
				errorPage(c, http.StatusNotFound, "Failed to complete auth due internal server error")
				return
			}

			pid, name := UserCreds(user)

			id, err := db.SignUser(pid, name)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to complete auth")
				errorPage(c, http.StatusNotFound, "Failed to complete auth due internal server error")
				return
			}

			log.WithFields(logrus.Fields{
				"pid":  pid,
				"uid":  id,
				"name": name,
			}).Info("Logged in")
			sess := sessions.Default(c)
			sess.Set("user_id", id)
			sess.Save()

			c.Redirect(http.StatusTemporaryRedirect, "/cards")
		})
	}

	// User session management handlers
	{
		us := g.Group("/")
		us.GET("/login", func(c *gin.Context) {
			c.HTML(http.StatusOK, "login.html", gin.H{
				"Title":     "Login",
				"Providers": providers,
				"User":      getUser(c),
			})
		})
		us.GET("/logout", func(c *gin.Context) {
			sess := sessions.Default(c)
			sess.Clear()
			sess.Save()
			c.Redirect(http.StatusTemporaryRedirect, "/")
		})
	}

	// Handlers that requires authorisation
	{
		authorized := g.Group("/")
		authorized.Use(authMiddleware())
		authorized.GET("/userdel", func(c *gin.Context) {
			user := getUser(c)

			if user != nil {
				db.DeleteUser(user.ID)
			}

			sess := sessions.Default(c)
			sess.Clear()
			sess.Save()
			c.Redirect(http.StatusTemporaryRedirect, "/")
		})
	}
}
