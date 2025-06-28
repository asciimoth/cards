package main

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/markbates/goth/gothic"
	"github.com/sirupsen/logrus"
)

func errorPage(c *gin.Context, status int, text string) {
	if text == "" {
		text = http.StatusText(status)
	}
	c.HTML(status, "page_error.html", gin.H{
		"Code": status,
		"Text": text,
	})
}

func errorBlock(c *gin.Context, status int, text string) {
	if text == "" {
		text = http.StatusText(status)
	}
	c.HTML(status, "comp_error.html", gin.H{
		"ErrorCode": status,
		"ErrorText": text,
	})
}

func langMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)
		lang := sess.Get("Lang")
		if lang == nil {
			c.Set("Lang", "en")
		} else {
			c.Set("Lang", lang.(string))
		}
		c.Next()
	}
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

func isFileInForm(form *multipart.Form, input string) bool {
	files, ok := form.File[input]
	if !ok {
		return false
	}
	return len(files) > 0 && files[0] != nil
}

func uploader(log *logrus.Logger, storage *BlobStorage, ctx context.Context, maxUploadSize int64) func(*gin.Context, *multipart.Form, string, string) bool {
	return func(c *gin.Context, form *multipart.Form, input, key string) bool {
		files := form.File[input]
		avatar := files[0]
		if avatar.Size > maxUploadSize {
			errorPage(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("%s too large", avatar.Filename))
			return false
		}

		mime := avatar.Header.Get("Content-Type")
		if mime == "" {
			errorPage(c, http.StatusBadRequest, fmt.Sprintf("Content type of %s unknown", avatar.Filename))
		}
		if !strings.HasPrefix(mime, "image/") {
			errorPage(c, http.StatusBadRequest, fmt.Sprintf("%s is not an image. mime: %s", avatar.Filename, mime))
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

func redirect(c *gin.Context, target string) {
	if c.GetHeader("HX-Request") == "true" {
		c.Header("HX-Redirect", target)
		c.Status(http.StatusOK)
	} else {
		c.Redirect(http.StatusFound, target)
	}
}

func SetupRoutes(
	g *gin.Engine,
	ctx context.Context,
	storage *BlobStorage,
	db *PGDB,
	log *logrus.Logger,
	providers []string,
	locales []string,
) {
	max_upload_size := os.Getenv("MAX_UPLOAD_SIZE")
	mus, err := strconv.ParseInt(max_upload_size, 10, 64)
	if err != nil {
		log.Fatalf("Failed to conf max upload size: %s", max_upload_size)
	}

	uploadFormFile := uploader(log, storage, ctx, mus)
	fetchMedia := mediaFetcher(log, storage, ctx)

	g.Use(sessionMiddleware(log, db))
	g.Use(langMiddleware())

	g.NoRoute(func(c *gin.Context) {
		errorPage(c, http.StatusNotFound, "")
	})

	g.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "page_index.html", gin.H{
			"Title": "Cards",
			"User":  getUser(c),
		})
	})
	g.GET("/faq", func(c *gin.Context) {
		c.HTML(http.StatusOK, "page_faq.html", gin.H{
			"Title": "Faq",
			"User":  getUser(c),
		})
	})
	g.GET("/tutorial", func(c *gin.Context) {
		c.HTML(http.StatusOK, "page_tutorial.html", gin.H{
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
			c.HTML(http.StatusNotFound, "page_cardNotFound.html", gin.H{"User": user})
			return
		}

		is_owner := false

		if user != nil {
			is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
		}
		if !is_owner && card.Fields.IsHidden {
			c.HTML(http.StatusNotFound, "page_cardNotFound.html", gin.H{"User": user})
			return
		}

		c.HTML(http.StatusOK, "page_card.html", gin.H{
			"Title":   card.Fields.Name,
			"Card":    card,
			"User":    user,
			"Owner":   is_owner,
			"EditUrl": fmt.Sprintf("/editor/%d", cid),
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

			redirect(c, "/cards")
		})
	}

	// User session management handlers
	{
		us := g.Group("/")
		us.GET("/login", func(c *gin.Context) {
			c.HTML(http.StatusOK, "page_login.html", gin.H{
				"Title":     "Login",
				"Providers": providers,
				// "Providers": providers,
				"User": getUser(c),
			})
		})
		us.POST("/logout", func(c *gin.Context) {
			sess := sessions.Default(c)
			sess.Clear()
			sess.Save()
			redirect(c, "/")
		})
		us.GET("/logout", func(c *gin.Context) {
			sess := sessions.Default(c)
			sess.Clear()
			sess.Save()
			redirect(c, "/")
		})
	}

	// Handlers that requires authorisation
	{
		authorized := g.Group("/")
		authorized.Use(authMiddleware())
		authorized.POST("/userdel", func(c *gin.Context) {
			user := getUser(c)

			db.DeleteUser(user.ID)

			sess := sessions.Default(c)
			sess.Clear()
			sess.Save()
			redirect(c, "/")
		})
		authorized.POST("/userdel/:id", func(c *gin.Context) {
			user := getUser(c)

			if user.Type != UserTypeAdmin {
				errorPage(c, http.StatusNotFound, "")
				return
			}

			uid, err := getUintParam(c, "id")

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Wrong user id")
				errorPage(c, http.StatusInternalServerError, "Wrong user id")
				return
			}

			err = db.DeleteUser(uid)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to delete user")
				errorPage(c, http.StatusInternalServerError, "Failed to delete user")
				return
			}

			redirect(c, "/users")
		})
		authorized.GET("/cards", func(c *gin.Context) {
			user := getUser(c)
			redirect(c, fmt.Sprintf("/cards/%d", user.ID))
		})
		authorized.GET("/cards/:id", func(c *gin.Context) {
			user := getUser(c)

			uid, err := getUintParam(c, "id")

			if err != nil {
				redirect(c, fmt.Sprintf("/cards/%d", user.ID))
				return
			}

			if user.Type != UserTypeAdmin && uid != user.ID {
				redirect(c, fmt.Sprintf("/cards/%d", user.ID))
			}

			cards, err := db.ListCards(uid)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to list cards")
				errorPage(c, http.StatusInternalServerError, "Failed to list cards")
				return
			}

			c.HTML(http.StatusOK, "page_cards.html", gin.H{
				"Title":   "Your cards",
				"User":    user,
				"Cards":   cards,
				"Lang":    c.MustGet("Lang").(string),
				"Locales": locales,
			})
		})
		authorized.POST("/delcard/:id", func(c *gin.Context) {
			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				redirect(c, "/cards")
				return
			}

			card, err := db.GetCard(cid)

			if err != nil {
				log.WithFields(logrus.Fields{
					"cid": cid,
					"err": err,
				}).Error("Failed to find a card")
				redirect(c, "/cards")
				return
			}

			if card.Owner != user.ID && user.Type != UserTypeAdmin {
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

			redirect(c, fmt.Sprintf("/cards/%d", card.Owner))
		})
		authorized.GET("/editor", func(c *gin.Context) {
			c.HTML(http.StatusOK, "page_editor.html", gin.H{
				"Title":        "Create new card",
				"User":         getUser(c),
				"EditUrl":      "/new",
				"SubmitButton": "Create Card",
				"Card": Card{
					ID:          0,
					Owner:       0,
					AvatarExist: false,
					LogoExist:   false,
					Fields: CardFields{
						Name:        "",
						Company:     "",
						Position:    "",
						Description: "",
						Phone:       "",
						Email:       "",
						Telegram:    "",
						Whatsapp:    "",
						VK:          "",
						IsHidden:    false,
					},
				},
			})
		})
		authorized.GET("/editor/:id", func(c *gin.Context) {
			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				redirect(c, "/cards")
				return
			}

			card, err := db.GetCard(cid)
			if err != nil {
				redirect(c, "/cards")
				return
			}

			if card.Owner != user.ID && user.Type != UserTypeAdmin {
				redirect(c, "/cards")
				return
			}

			c.HTML(http.StatusOK, "page_editor.html", gin.H{
				"Title":        "Edit card",
				"EditUrl":      fmt.Sprintf("/update/%d", cid),
				"User":         user,
				"SubmitButton": "Update Card",
				"Card":         card,
			})
		})
		// TODO: Merge /new & /update/:id endpoints
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

			card, err := db.CreateCard(user.ID, fields)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to create card")
				errorPage(c, http.StatusInternalServerError, "Failed to create card")
				return
			}

			if isFileInForm(form, "avatar") {
				if !uploadFormFile(c, form, "avatar", fmt.Sprintf("media/avatar/%d", card.ID)) {
					return
				}
				card.AvatarExist = true
				err = db.UpdateCard(card)
				if err != nil {
					log.WithFields(logrus.Fields{
						"err": err,
					}).Error("Failed to upload avatar")
					errorPage(c, http.StatusInternalServerError, "Failed to upload avatar")
					return
				}
			}

			if isFileInForm(form, "logo") {
				if !uploadFormFile(c, form, "logo", fmt.Sprintf("media/logo/%d", card.ID)) {
					return
				}
				card.LogoExist = true
				err = db.UpdateCard(card)
				if err != nil {
					log.WithFields(logrus.Fields{
						"err": err,
					}).Error("Failed to upload logo")
					errorPage(c, http.StatusInternalServerError, "Failed to upload logo")
					return
				}
			}

			redirect(c, "/cards")
		})
		authorized.POST("/update/:id", func(c *gin.Context) {
			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				redirect(c, "/cards")
				return
			}

			card, err := db.GetCard(cid)
			if err != nil {
				redirect(c, "/cards")
				return
			}

			if card.Owner != user.ID && user.Type != UserTypeAdmin {
				redirect(c, "/cards")
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

			fields.IsHidden = card.Fields.IsHidden // TODO: Make it less ugly
			card.Fields = fields
			err = db.UpdateCard(card)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to update the card")
				errorPage(c, http.StatusBadRequest, "Failed to update card content")
				return
			}

			if isFileInForm(form, "avatar") {
				if !uploadFormFile(c, form, "avatar", fmt.Sprintf("media/avatar/%d", cid)) {
					return
				}
				if !card.AvatarExist {
					card.AvatarExist = true
					err = db.UpdateCard(card)
					if err != nil {
						log.WithFields(logrus.Fields{
							"err": err,
						}).Error("Failed to upload avatar")
						errorPage(c, http.StatusInternalServerError, "Failed to upload avatar")
						return
					}
				}
			}

			if isFileInForm(form, "logo") {
				if !uploadFormFile(c, form, "logo", fmt.Sprintf("media/logo/%d", cid)) {
					return
				}
				if !card.LogoExist {
					card.LogoExist = true
					err = db.UpdateCard(card)
					if err != nil {
						log.WithFields(logrus.Fields{
							"err": err,
						}).Error("Failed to upload logo")
						errorPage(c, http.StatusInternalServerError, "Failed to upload logo")
						return
					}
				}
			}

			redirect(c, fmt.Sprintf("/cards/%d", card.Owner))
		})
		authorized.POST("/visibility/:id", func(c *gin.Context) {

			user := getUser(c)

			cid, err := getUintParam(c, "id")

			if err != nil {
				errorBlock(c, http.StatusBadRequest, "")
				return
			}

			card, err := db.GetCard(cid)
			if err != nil {
				errorBlock(c, http.StatusInternalServerError, "Card not found")
				return
			}

			if card.Owner != user.ID && user.Type != UserTypeAdmin {
				errorBlock(c, http.StatusForbidden, "You are not owner of this card")
				return
			}

			switch c.Query("visible") {
			case "true":
				card.Fields.IsHidden = false
				err = db.UpdateCard(card)
			case "false":
				card.Fields.IsHidden = true
				err = db.UpdateCard(card)
			}

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to update card visibility")
			}

			c.HTML(http.StatusOK, "comp_cardElement.html", card)
		})
		authorized.GET("/users", func(c *gin.Context) {
			user := getUser(c)

			if user.Type != UserTypeAdmin {
				errorPage(c, http.StatusNotFound, "")
				return
			}

			users, err := db.ListUsers()

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to list users")
				errorPage(c, http.StatusInternalServerError, "Failed to list users")
				return
			}

			c.HTML(http.StatusOK, "page_users.html", gin.H{
				"Title": "Your cards",
				"User":  user,
				"Users": users,
			})
		})
		authorized.POST("/setlocale", func(c *gin.Context) {
			locale := c.PostForm("lang")
			sess := sessions.Default(c)
			if locale == "" {
				sess.Set("Lang", "en")
			} else {
				sess.Set("Lang", locale)
			}
			sess.Save()
			redirect(c, "/cards")
		})
	}
}
