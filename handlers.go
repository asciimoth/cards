package main

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
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

func uploader(log *logrus.Logger, storage *BlobStorage, ctx context.Context, maxUploadSize int64) func(*gin.Context, *multipart.Form, string, string) bool {
	return func(c *gin.Context, form *multipart.Form, input, key string) bool {
		files, ok := form.File[input]
		if ok && len(files) > 0 && files[0] != nil {
			avatar := files[0]
			if avatar.Size > maxUploadSize {
				errorPage(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("%s too large", avatar.Filename))
				return false
			}

			mime := avatar.Header.Get("Content-Type")
			if mime == "" {
				errorPage(c, http.StatusBadRequest, fmt.Sprintf("Content type of %s unknown", avatar.Filename))
			}

			src, err := avatar.Open()
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to receive form file")
				errorPage(c, http.StatusBadRequest, fmt.Sprintf("Broken file %s", avatar.Filename))
				return false
			}

			defer src.Close()

			err = storage.WriteKey(ctx, key, src, avatar.Size, mime)
			log.WithFields(logrus.Fields{
				"key": key,
			}).Debug("File uploaded")

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to uload file to storage")
				errorPage(c, http.StatusInternalServerError, fmt.Sprintf("Failed to upload file %s", avatar.Filename))
				return false
			}
		}
		return true
	}
}

func mediaFetcher(log *logrus.Logger, storage *BlobStorage, ctx context.Context) func(c *gin.Context, key string) {
	return func(c *gin.Context, key string) {
		changed, size, reader, mime, etag, err := storage.GetKey(ctx, key, c.GetHeader("If-None-Match"))
		if reader != nil {
			defer reader.Close()
		}
		if err != nil {
			log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Error while fetching media")
			errorPage(c, http.StatusNotFound, "")
			return
		}
		if !changed {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("ETag", etag)
		c.Header("Content-Length", fmt.Sprintf("%d", size))
		if mime != "" {
			c.Header("Content-Type", mime)
		} else {
			log.WithFields(logrus.Fields{
				"key": key,
			}).Error("Unknown content-type of media")
			errorPage(c, http.StatusInternalServerError, "failed to load")
			return
		}
		if _, err := io.Copy(c.Writer, reader); err != nil {
			log.WithFields(logrus.Fields{
				"key": key,
				"err": err,
			}).Error("Error streaming media")
			errorPage(c, http.StatusInternalServerError, "failed to load")
		}
	}
}

func SetupRoutes(
	g *gin.Engine,
	ctx context.Context,
	storage *BlobStorage,
	db *PGDB,
	log *logrus.Logger,
	providers []string,
) {
	max_upload_size := os.Getenv("MAX_UPLOAD_SIZE")
	mus, err := strconv.ParseInt(max_upload_size, 10, 64)
	if err != nil {
		log.Fatalf("Failed to conf max upload size: %s", max_upload_size)
	}

	uploadFormFile := uploader(log, storage, ctx, mus)
	fetchMedia := mediaFetcher(log, storage, ctx)

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
			"EditUrl": fmt.Sprintf("/editor/%d", cid),
			"Avatar":  fmt.Sprintf("/media/avatar/%d", cid),
			"Logo":    fmt.Sprintf("/media/logo/%d", cid),
		})
	})

	g.GET("/media/:kind/:id", func(c *gin.Context) {
		kind := c.Params.ByName("kind")

		allowed := map[string]bool{
			"logo":   true,
			"avatar": true,
		}

		if _, ok := allowed[kind]; !ok {
			errorPage(c, http.StatusNotFound, kind+" not found")
			return
		}

		cid, err := getUintParam(c, "id")
		if err != nil {
			errorPage(c, http.StatusNotFound, kind+" not found")
			return
		}
		fetchMedia(c, fmt.Sprintf("media/%s/%d", kind, cid))
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
		authorized.GET("/cards", func(c *gin.Context) {
			user := getUser(c)

			cards, err := db.ListCards(user.ID)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to list cards")
				errorPage(c, http.StatusInternalServerError, "Failed to list cards")
				return
			}

			c.HTML(http.StatusOK, "cards.html", gin.H{
				"Title": "Your cards",
				"User":  user,
				"Cards": cards,
			})
		})
		authorized.GET("/delcard/:id", func(c *gin.Context) {
			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			card, err := db.GetCard(cid)

			if err != nil {
				log.WithFields(logrus.Fields{
					"cid": cid,
					"err": err,
				}).Error("Failed to find a card")
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			if card.Owner != user.ID {
				errorPage(c, http.StatusForbidden, "Card is owned by another user")
				return
			}

			err = db.DeleteCard(cid)

			if err != nil {
				log.WithFields(logrus.Fields{
					"cid": cid,
					"err": err,
				}).Error("Failed to delete a card")
			}

			c.Redirect(http.StatusTemporaryRedirect, "/cards")
		})
		authorized.GET("/editor", func(c *gin.Context) {
			c.HTML(http.StatusOK, "editor.html", gin.H{
				"Title":        "Create new card",
				"User":         getUser(c),
				"EditUrl":      "/new",
				"SubmitButton": "Create Card",
			})
		})
		authorized.GET("/editor/:id", func(c *gin.Context) {
			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			card, err := db.GetCard(cid)
			if err != nil {
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			c.HTML(http.StatusOK, "editor.html", gin.H{
				"Title":        "Edit card",
				"EditUrl":      fmt.Sprintf("/update/%d", cid),
				"User":         user,
				"SubmitButton": "Update Card",
				"Card":         card,
			})
		})
		authorized.POST("/new", func(c *gin.Context) {
			user := getUser(c)

			var fields CardFields

			if err := c.Bind(&fields); err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to bind form data")
				errorPage(c, http.StatusBadRequest, "Invalid form data")
				return
			}

			form, err := c.MultipartForm()
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to get multipart form data")
				errorPage(c, http.StatusBadRequest, "Invalid form data")
				return
			}

			cardId, err := db.CreateCard(user.ID, fields)

			if !uploadFormFile(c, form, "avatar", fmt.Sprintf("media/avatar/%d", cardId)) {
				return
			}

			if !uploadFormFile(c, form, "logo", fmt.Sprintf("media/logo/%d", cardId)) {
				return
			}

			c.Redirect(http.StatusFound, "/cards")
		})
		authorized.POST("/update/:id", func(c *gin.Context) {
			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			card, err := db.GetCard(cid)
			if err != nil {
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			if card.Owner != user.ID {
				c.Redirect(http.StatusTemporaryRedirect, "/cards")
				return
			}

			var fields CardFields

			if err := c.Bind(&fields); err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to bind form data")
				errorPage(c, http.StatusBadRequest, "Invalid form data")
				return
			}

			form, err := c.MultipartForm()
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to get multipart form data")
				errorPage(c, http.StatusBadRequest, "Invalid form data")
				return
			}

			card.Fields = fields
			err = db.UpdateCard(card)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to update the card")
				errorPage(c, http.StatusBadRequest, "Failed to update card content")
				return
			}

			if !uploadFormFile(c, form, "avatar", fmt.Sprintf("media/avatar/%d", cid)) {
				return
			}

			if !uploadFormFile(c, form, "logo", fmt.Sprintf("media/logo/%d", cid)) {
				return
			}

			c.Redirect(http.StatusFound, "/cards")
		})
	}
}
