package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/markbates/goth/gothic"
	"github.com/sirupsen/logrus"
)

func headersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Service-Worker-Allowed", "/")
		c.Next()
	}
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

func sessionMiddleware(log *logrus.Logger, db Database) gin.HandlerFunc {
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
			redirect(c, "/")
			return
		}
		if user.Type == UserTypeLimited {
			redirect(c, "/")
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

func uploader(
	log *logrus.Logger,
	storage *BlobStorage,
	ctx context.Context,
	maxUploadSize int64,
	errorPage func(*gin.Context, int, string),
	localize func(*gin.Context, string) string,
) func(*gin.Context, *multipart.Form, string, string) bool {
	return func(c *gin.Context, form *multipart.Form, input, key string) bool {
		files := form.File[input]
		avatar := files[0]
		if avatar.Size > maxUploadSize {
			errorPage(
				c,
				http.StatusRequestEntityTooLarge,
				fmt.Sprintf(localize(c, "ErrMsgFileIsTooBig"), avatar.Filename),
			)
			return false
		}

		mime := avatar.Header.Get("Content-Type")
		if mime != "image/webp" {
			errorPage(
				c,
				http.StatusBadRequest,
				fmt.Sprintf(localize(c, "ErrMsgUnknownMimeType"), avatar.Filename, mime),
			)
			return false
		}

		src, err := avatar.Open()
		if err != nil {
			log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Failed to receive form file")
			errorPage(
				c,
				http.StatusBadRequest,
				fmt.Sprintf(localize(c, "ErrMsgBrokenFile"), avatar.Filename),
			)
			return false
		}

		defer src.Close()

		err = storage.WriteKey(ctx, key, src, avatar.Size, true)
		log.WithFields(logrus.Fields{
			"key": key,
		}).Debug("File uploaded")

		if err != nil {
			log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Failed to uload file to storage")
			errorPage(
				c,
				http.StatusInternalServerError,
				fmt.Sprintf(localize(c, "ErrMsgFailedToUploadFile"), avatar.Filename),
			)
			return false
		}
		return true
	}
}

func mediaFetcher(
	log *logrus.Logger,
	storage *BlobStorage,
	ctx context.Context,
	errorPage func(*gin.Context, int, string),
	localize func(*gin.Context, string) string,
) func(c *gin.Context, key string) {
	return func(c *gin.Context, key string) {
		size, reader, err := storage.GetKey(ctx, key, true)
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
		//c.Header("ETag", etag)
		c.Header("Content-Length", fmt.Sprintf("%d", size))
		c.Header("Content-Type", "image/webp")
		c.Header("Cache-Control", "public, max-age=31536000, immutable") // one year
		if _, err := io.Copy(c.Writer, reader); err != nil {
			log.WithFields(logrus.Fields{
				"key": key,
				"err": err,
			}).Error("Error streaming media")
			errorPage(
				c,
				http.StatusInternalServerError,
				localize(c, "ErrMsgFailedToLoadFile"),
			)
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

func setupStatic(
	g *gin.Engine,
	log *logrus.Logger,
	errorPage func(*gin.Context, int, string),
	localize func(*gin.Context, string) string,
) {
	etag := fmt.Sprintf(`W/"%d"`, time.Now().Unix())
	g.GET("/static/:file", func(c *gin.Context) {
		filename := c.Param("file")

		// Prevent directory traversal
		if strings.Contains(filename, "..") || strings.Contains(filename, "/") {
			errorPage(
				c,
				http.StatusBadRequest,
				localize(c, "ErrMsgInvalidFileName"),
			)
			return
		}

		// Check If-None-Match header
		if match := c.GetHeader("If-None-Match"); match != "" && os.Getenv("GO_ENV") != "debug" {
			if match == etag {
				// Client already has the latest version
				c.Status(http.StatusNotModified)
				log.Debugf("Etag not modified %s", c.Request.URL)
				return
			}
		}

		if strings.HasSuffix(filename, ".svg") {
			filename = filepath.Join("svg", filename)
		} else if strings.HasSuffix(filename, ".css") {
			filename = filepath.Join("css", filename)
		} else if strings.HasSuffix(filename, ".js") {
			filename = filepath.Join("js", filename)
		}

		fullPath := filepath.Join("./static", filename)

		if os.Getenv("GO_ENV") != "debug" {
			// Set caching headers
			c.Header("Etag", etag)
		}

		// Serve the file
		c.File(fullPath)
	})
}

func SetupRoutes(
	g *gin.Engine,
	ctx context.Context,
	storage *BlobStorage,
	db Database,
	log *logrus.Logger,
	providers []string,
	locales []string,
	localizer func(string, string) string,
) {
	max_upload_size := os.Getenv("MAX_UPLOAD_SIZE")
	mus, err := strconv.ParseInt(max_upload_size, 10, 64)
	if err != nil {
		log.Fatalf("Failed to conf max upload size: %s", max_upload_size)
	}

	localize := func(c *gin.Context, key string) string {
		return localizer(
			key,
			c.MustGet("Lang").(string),
		)
	}

	execHTML := func(c *gin.Context, status int, card string, add gin.H) {
		dst := gin.H{
			"User":    getUser(c),
			"Lang":    c.MustGet("Lang").(string),
			"Locales": locales,
		}
		maps.Copy(dst, add)
		c.HTML(status, card, dst)
	}

	errorPage := func(c *gin.Context, status int, text string) {
		if text == "" {
			text = localize(c, fmt.Sprintf("ErrCode%d", status))
		}
		execHTML(c, status, "page_error.html", gin.H{
			"Code": status,
			"Text": text,
		})
	}

	errorBlock := func(c *gin.Context, status int, text string) {
		if text == "" {
			text = localize(c, fmt.Sprintf("ErrCode%d", status))
		}
		execHTML(c, status, "comp_error.html", gin.H{
			"ErrorCode": status,
			"ErrorText": text,
		})
	}

	uploadFormFile := uploader(log, storage, ctx, mus, errorPage, localize)
	fetchMedia := mediaFetcher(log, storage, ctx, errorPage, localize)

	g.Use(headersMiddleware())
	g.Use(sessionMiddleware(log, db))
	g.Use(langMiddleware())

	g.NoRoute(func(c *gin.Context) {
		errorPage(c, http.StatusNotFound, "")
	})

	setupStatic(g, log, errorPage, localize)

	g.GET("/", func(c *gin.Context) {
		execHTML(c, http.StatusOK, "page_index.html", gin.H{
			"Title": localize(c, "TitleMain"),
		})
	})
	g.GET("/faq", func(c *gin.Context) {
		execHTML(c, http.StatusOK, "page_faq.html", gin.H{
			"Title": localize(c, "TitleFaq"),
		})
	})
	g.GET("/tutorial", func(c *gin.Context) {
		execHTML(c, http.StatusOK, "page_tutorial.html", gin.H{
			"Title": localize(c, "TitleTutorial"),
		})
	})

	g.GET("/c/:id", func(c *gin.Context) {
		cid, err := getUintParam(c, "id")
		if err != nil {
			errorPage(
				c,
				http.StatusBadRequest,
				localize(c, "ErrMsgInvalidCardID"),
			)
			return
		}

		user := getUser(c)

		card, err := db.GetCard(cid)
		if err != nil {
			execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
			return
		}

		is_owner := false

		if user != nil {
			is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
		}
		if !is_owner && card.Fields.IsHidden {
			execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
			return
		}

		execHTML(c, http.StatusOK, "page_card.html", gin.H{
			"Title":   card.Fields.Name,
			"Card":    card,
			"Owner":   is_owner,
			"EditUrl": fmt.Sprintf("/editor/%d", cid),
		})
	})

	g.GET("/media/:kind/:id", func(c *gin.Context) {
		kind := c.Params.ByName("kind")
		id := c.Params.ByName("id")

		allowed := map[string]bool{
			"logo":   true,
			"avatar": true,
		}

		if _, ok := allowed[kind]; !ok {
			errorPage(
				c,
				http.StatusBadRequest,
				localize(c, "ErrMsgIsNotFound_"+kind),
			)
			return
		}

		// TODO: Filter ID for security

		fetchMedia(c, "media/"+kind+"/"+id)
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
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedAuth500"),
				)
				return
			}

			pid, name := UserCreds(user)

			id, err := db.SignUser(pid, name)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to complete auth")
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedAuth500"),
				)
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
		// Awful code
		// TODO: Refactor
		oauth.POST("/auth-vk", func(c *gin.Context) {
			type VKUserInfo struct {
				User struct {
					UserID    string `json:"user_id"`
					FirstName string `json:"first_name"`
					LastName  string `json:"last_name"`
				} `json:"user"`
			}

			accessToken := c.PostForm("access_token")
			if accessToken == "" {
				redirect(c, "/login")
				return
			}

			form := url.Values{}
			form.Set("client_id", os.Getenv("VK_CLIENT_ID"))
			form.Set("access_token", accessToken)

			resp, err := http.PostForm("https://id.vk.com/oauth2/user_info", form)
			if resp != nil && resp.Body != nil {
				defer resp.Body.Close()
			}
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to complete VK auth")
				errorPage(
					c,
					http.StatusInternalServerError,
					"Failed to contact VK",
				)
				return
			}

			if resp.StatusCode != http.StatusOK {
				log.WithFields(logrus.Fields{
					"status": resp.StatusCode,
				}).Error("Failed to complete VK auth")
				errorPage(
					c,
					http.StatusInternalServerError,
					"VK API error",
				)
				return
			}

			// Decode the JSON response
			var info VKUserInfo
			if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to complete VK auth")
				errorPage(
					c,
					http.StatusInternalServerError,
					"VK API error",
				)
				return
			}

			pid := "vk:" + info.User.UserID
			name := info.User.FirstName
			if name != "" && info.User.LastName != "" {
				name += " "
			}
			name += info.User.LastName

			id, err := db.SignUser(pid, name)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to complete auth")
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedAuth500"),
				)
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

	// PWA related handlers
	{
		pwa := g.Group("/")
		pwa.GET("/c/:id/manifest.json", func(c *gin.Context) {
			cid, err := getUintParam(c, "id")
			if err != nil {
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgInvalidCardID"),
				)
				return
			}

			user := getUser(c)

			card, err := db.GetCard(cid)
			if err != nil {
				execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
				return
			}

			is_owner := false

			if user != nil {
				is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
			}
			if !is_owner && card.Fields.IsHidden {
				execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
				return
			}
			manifest := map[string]any{
				"name":       card.Fields.Name,
				"short_name": card.Fields.Name,
				"start_url":  fmt.Sprintf("/c/%d", cid),
				"scope":      fmt.Sprintf("/c/%d", cid),
				"display":    "standalone",
				"icons": []map[string]string{
					{
						"src":   "/" + card.Avatar,
						"sizes": "192x192",
						"type":  "image/webp",
					},
					{
						"src":   "/" + card.Avatar,
						"sizes": "512x512",
						"type":  "image/webp",
					},
				},
			}
			c.JSON(200, manifest)
		})
		pwa.GET("/c/:id/sw.js", func(c *gin.Context) {
			cid, err := getUintParam(c, "id")
			if err != nil {
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgInvalidCardID"),
				)
				return
			}

			user := getUser(c)

			card, err := db.GetCard(cid)
			if err != nil {
				execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
				return
			}

			is_owner := false

			if user != nil {
				is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
			}
			if !is_owner && card.Fields.IsHidden {
				execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
				return
			}

			c.Header("Content-Type", "application/javascript")
			// a minimal SW: cache the card’s HTML + assets
			c.String(200, fmt.Sprintf(`
			    const CACHE = "card-%d-v8";
			    const toCache = [
				  "/",
			      "/c/%d",
				  "/c/%d/",
			      "/static/style.css",
				  "/static/cards.css",
				  "/static/card.css",
				  "/static/card.js",
				  "/static/collapse.js",
				  "/static/copy.js",
				  "/static/preview.js",
				  "/static/airplane.svg",
				  "/static/burger.svg",
				  "/static/close.svg",
				  "/static/contact.svg",
				  "/static/copy.svg",
				  "/static/copy-svgrepo-com.svg",
				  "/static/delete.svg",
				  "/static/edit.svg",
				  "/static/email.svg",
				  "/static/favicon-192.svg",
				  "/static/fly.svg",
				  "/static/lock.svg",
				  "/static/logo.svg",
				  "/static/phone.svg",
				  "/static/qr-code-.svg",
				  "/static/telegram.svg",
				  "/static/unlock.svg",
				  "/static/view.svg",
				  "/static/vk-logo.svg",
				  "/static/yandex-logo.svg",
			      "/%s",
				  "https://cdnjs.cloudflare.com/ajax/libs/qrcodejs/1.0.0/qrcode.js",
				  "https://cdnjs.cloudflare.com/ajax/libs/dom-to-image/2.6.0/dom-to-image.min.js"
			    ];
			    self.addEventListener("install", e => {
			      e.waitUntil(caches.open(CACHE).then(c => c.addAll(toCache)));
			    });
					self.addEventListener("activate", e => {
						e.waitUntil(self.clients.claim());
					});
					self.addEventListener("fetch", ev => {
						ev.respondWith(
							fetch(ev.request)
								.then(networkRes => {
									// If valid response, clone & store it in cache
									if (networkRes.ok) {
										const copy = networkRes.clone();
										caches.open(CACHE).then(cache => cache.put(ev.request, copy));
									}
									return networkRes;
								})
								.catch(() => {
									// Network failed (offline?), fall back to cache
									return caches.match(ev.request);
								})
						);
					});
			`, cid, cid, cid, card.Avatar))
		})
	}

	// User session management handlers
	{
		us := g.Group("/")
		us.GET("/login", func(c *gin.Context) {
			execHTML(c, http.StatusOK, "page_login.html", gin.H{
				"Title":     localize(c, "TitleLogin"),
				"Providers": providers,
			})
		})
		us.GET("/login/vk", func(c *gin.Context) {
			execHTML(c, http.StatusOK, "page_login_vk.html", gin.H{
				"Title":      "VK login",
				"vkapp":      os.Getenv("VK_CLIENT_ID"),
				"vkredirect": os.Getenv("VK_CLIENT_CALLBACK_URL"),
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
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgBrokenUserID"),
				)
				return
			}

			err = db.DeleteUser(uid)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to delete user")
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedTODeleteUser"),
				)
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
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedToListCards"),
				)
				return
			}

			execHTML(c, http.StatusOK, "page_cards.html", gin.H{
				"Title": localize(c, "TitleCards"),
				"Cards": cards,
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
				errorPage(
					c,
					http.StatusForbidden,
					localize(c, "ErrMsgCardIsOwnedByAnotherUser"),
				)
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
			execHTML(c, http.StatusOK, "page_editor.html", gin.H{
				"Title":        localize(c, "TitleCreateNewCard"),
				"EditUrl":      "/new",
				"SubmitButton": "CreateCard",
				"Card": Card{
					ID:     0,
					Owner:  0,
					Avatar: "",
					Logo:   "",
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

			execHTML(c, http.StatusOK, "page_editor.html", gin.H{
				"Title":        localize(c, "TitleEditCard"),
				"EditUrl":      fmt.Sprintf("/update/%d", cid),
				"SubmitButton": "UpdateCard",
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
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgInvalidFromData"),
				)
				return
			}

			form, err := c.MultipartForm()
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to get multipart form data")
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgInvalidFromData"),
				)
				return
			}

			card, err := db.CreateCard(user.ID, fields)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to create card")
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedToCreateCard500"),
				)
				return
			}

			avatar := fmt.Sprintf("media/avatar/%d-%s.webp", card.ID, uuid.New().String())
			if isFileInForm(form, "avatar") {
				if !uploadFormFile(c, form, "avatar", avatar) {
					return
				}
				card.Avatar = avatar
				err = db.UpdateCard(card)
				if err != nil {
					log.WithFields(logrus.Fields{
						"err": err,
					}).Error("Failed to upload avatar")
					errorPage(
						c,
						http.StatusInternalServerError,
						localize(c, "ErrMsgFailedToUploadAvatar"),
					)
					return
				}
			}

			logo := fmt.Sprintf("media/logo/%d-%s.webp", card.ID, uuid.New().String())
			if isFileInForm(form, "logo") {
				if !uploadFormFile(c, form, "logo", logo) {
					return
				}
				card.Logo = logo
				err = db.UpdateCard(card)
				if err != nil {
					log.WithFields(logrus.Fields{
						"err": err,
					}).Error("Failed to upload logo")
					errorPage(
						c,
						http.StatusInternalServerError,
						localize(c, "ErrMsgFailedToUploadLogo"),
					)
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
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgInvalidFromData"),
				)
				return
			}

			form, err := c.MultipartForm()
			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to get multipart form data")
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgInvalidFromData"),
				)
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

			avatar := fmt.Sprintf("media/avatar/%d-%s.webp", card.ID, uuid.New().String())
			if isFileInForm(form, "avatar") {
				if !uploadFormFile(c, form, "avatar", avatar) {
					return
				}
				old_avatar := card.Avatar
				card.Avatar = avatar
				err = db.UpdateCard(card)
				if err != nil {
					log.WithFields(logrus.Fields{
						"err": err,
					}).Error("Failed to upload avatar")
					errorPage(c, http.StatusInternalServerError, "Failed to upload avatar")
					return
				}
				if old_avatar != "" {
					err := storage.DelKey(ctx, old_avatar)
					if err != nil {
						log.WithFields(logrus.Fields{
							"err":    err,
							"avatar": old_avatar,
						}).Error("Failed to delete previous avatar")
					}
				}
			}

			logo := fmt.Sprintf("media/logo/%d-%s.webp", card.ID, uuid.New().String())
			if isFileInForm(form, "logo") {
				if !uploadFormFile(c, form, "logo", logo) {
					return
				}
				old_logo := card.Logo
				card.Logo = logo
				err = db.UpdateCard(card)
				if err != nil {
					log.WithFields(logrus.Fields{
						"err": err,
					}).Error("Failed to upload logo")
					errorPage(c, http.StatusInternalServerError, "Failed to upload logo")
					return
				}
				if old_logo != "" {
					err := storage.DelKey(ctx, old_logo)
					if err != nil {
						log.WithFields(logrus.Fields{
							"err":  err,
							"logo": old_logo,
						}).Error("Failed to delete previous logo")
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
				errorBlock(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgInvalidFromData"),
				)
				return
			}

			if card.Owner != user.ID && user.Type != UserTypeAdmin {
				errorBlock(
					c,
					http.StatusForbidden,
					localize(c, "ErrMsgCardIsOwnedByAnotherUser"),
				)
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

			execHTML(c, http.StatusOK, "comp_cardElement.html", gin.H{
				"Card": card,
			})
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
				errorPage(
					c,
					http.StatusInternalServerError,
					localize(c, "ErrMsgFailedToListUsers"),
				)
				return
			}

			execHTML(c, http.StatusOK, "page_users.html", gin.H{
				"Title": localize(c, "TitleUsers"),
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
			referrer := c.Request.Referer()
			if referrer == "" {
				referrer = "/"
			}
			redirect(c, referrer)
		})
		authorized.POST("/changeUserType/:id/:typ", func(c *gin.Context) {
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
				errorPage(
					c,
					http.StatusBadRequest,
					localize(c, "ErrMsgBrokenUserID"),
				)
				return
			}

			typ, err := getUintParam(c, "typ")

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed get user type param")
				errorPage(
					c,
					http.StatusInternalServerError,
					"",
				)
				return
			}

			target := User{ID: uid}

			err = db.GetUser(&target)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to delete user")
				errorPage(
					c,
					http.StatusNotFound,
					"",
				)
				return
			}

			target.Type = typ

			err = db.UpdateUser(target)

			if err != nil {
				log.WithFields(logrus.Fields{
					"err": err,
				}).Error("Failed to update user")
				errorPage(
					c,
					http.StatusInternalServerError,
					"",
				)
				return
			}

			redirect(c, "/users")
		})
	}
}
