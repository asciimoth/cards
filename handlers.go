package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/markbates/goth/gothic"
	"github.com/sirupsen/logrus"
)

func getUser(c *gin.Context) *User {
	user := c.MustGet("User")
	if user == nil {
		return nil
	}
	return user.(*User)
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

func redirect(c *gin.Context, target string) {
	if c.GetHeader("HX-Request") == "true" {
		c.Header("HX-Redirect", target)
		c.Status(http.StatusOK)
	} else {
		c.Redirect(http.StatusFound, target)
	}
}

type Handler struct {
	log           *logrus.Logger
	ctx           context.Context
	g             *gin.Engine
	storage       *BlobStorage
	db            Database
	providers     []string
	locales       []string
	localizer     func(string, string) string
	maxUploadSize int64
}

func SetupHandler(
	g *gin.Engine,
	ctx context.Context,
	storage *BlobStorage,
	db Database,
	log *logrus.Logger,
	providers []string,
	locales []string,
	localizer func(string, string) string,
) {
	smus := os.Getenv("MAX_UPLOAD_SIZE")
	maxUploadSize, err := strconv.ParseInt(smus, 10, 64)
	if err != nil {
		log.Fatalf("Failed to conf max upload size: %s", smus)
	}
	handler := Handler{log, ctx, g, storage, db, providers, locales, localizer, maxUploadSize}
	g.Use(handler.headersMiddleware)
	g.Use(handler.sessionMiddleware)
	g.Use(handler.langMiddleware)
	g.NoRoute(func(c *gin.Context) {
		handler.errorPage(c, http.StatusNotFound, "")
	})
	handler.setupStatic()
	handler.setupRoutes()
}

func (h *Handler) setupRoutes() {
	h.g.GET("/", h.indexRoute)
	h.g.GET("/faq", h.faqRoute)
	h.g.GET("/tutorial", h.tutorialRoute)
	h.g.GET("/c/:id", h.cardRoute)
	h.g.GET("/media/:kind/:id", h.mediaRoute)
	// OAuth related routes
	{
		oauth := h.g.Group("/")
		oauth.GET("/auth/:provider", h.authProviderRoute)
		oauth.GET("/auth/:provider/callback", h.authCallbackRoute)
		// Telegram is not supported by goth so we handling it individually
		oauth.GET("/auth-tg", h.authTgRoute)
		// Vk is tecnically supported by goth, but seems like it support
		// only old Vk OAuth system
		oauth.POST("/auth-vk", h.authVkRoute)
	}
	// PWA related routes
	{
		pwa := h.g.Group("/")
		pwa.GET("/c/:id/manifest.json", h.cardManifestRoute)
		pwa.GET("/c/:id/sw.js", h.cardWorkerRoute)
	}
	// User session management handlers
	{
		us := h.g.Group("/")
		us.GET("/login", h.loginRoute)
		us.GET("/login/vk", h.loginVkRoute)
		us.GET("/logout", h.logoutRoute)
		us.POST("/logout", h.logoutRoute)
	}
	// Other routes
	{
		authorized := h.g.Group("/")
		authorized.Use(h.authMiddleware)
		authorized.POST("/userdel", h.userDelRoute)
		authorized.POST("/userdel/:id", h.userDelAdminRoute)
		authorized.GET("/cards", func(c *gin.Context) {
			user := getUser(c)
			redirect(c, fmt.Sprintf("/cards/%d", user.ID))
		})
		authorized.GET("/cards/:id", h.cardsRoute)
		authorized.POST("/delcard/:id", h.delCardRoute)
		authorized.GET("/editor", h.newCardRoute)
		authorized.GET("/editor/:id", h.editCardRoute)
		authorized.POST("/new", h.createCardRoute)
		authorized.POST("/update/:id", h.updateCardRoute)
		authorized.POST("/visibility/:id", h.changeCardVisibilityRoute)
		authorized.GET("/users", h.listUsersRoute)
		authorized.POST("/setlocale", h.setLocaleRoute)
		authorized.POST("/changeUserType/:id/:typ", h.changeUserTypeRoute)
	}
}

func (h *Handler) setupStatic() {
	etag := fmt.Sprintf(`W/"%d"`, time.Now().Unix())
	h.g.GET("/static/:file", func(c *gin.Context) {
		filename := c.Param("file")

		// Prevent directory traversal
		if strings.Contains(filename, "..") || strings.Contains(filename, "/") {
			h.errorPage(
				c,
				http.StatusBadRequest,
				h.localize(c, "ErrMsgInvalidFileName"),
			)
			return
		}

		// Check If-None-Match header
		if match := c.GetHeader("If-None-Match"); match != "" && os.Getenv("GO_ENV") != "debug" {
			if match == etag {
				// Client already has the latest version
				c.Status(http.StatusNotModified)
				h.log.Debugf("Etag not modified %s", c.Request.URL)
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

// Helpers

func (h *Handler) localize(c *gin.Context, key string) string {
	return h.localizer(
		key,
		c.MustGet("Lang").(string),
	)
}

func (h *Handler) execHTML(c *gin.Context, status int, card string, add gin.H) {
	dst := gin.H{
		"User":    getUser(c),
		"Lang":    c.MustGet("Lang").(string),
		"Locales": h.locales,
	}
	maps.Copy(dst, add)
	c.HTML(status, card, dst)
}

func (h *Handler) errorPage(c *gin.Context, status int, text string) {
	if text == "" {
		text = h.localize(c, fmt.Sprintf("ErrCode%d", status))
	}
	h.execHTML(c, status, "page_error.html", gin.H{
		"Code": status,
		"Text": text,
	})
}

func (h *Handler) errorBlock(c *gin.Context, status int, text string) {
	if text == "" {
		text = h.localize(c, fmt.Sprintf("ErrCode%d", status))
	}
	h.execHTML(c, status, "comp_error.html", gin.H{
		"ErrorCode": status,
		"ErrorText": text,
	})
}

func (h *Handler) uploadFormFile(c *gin.Context, form *multipart.Form, input, key string) bool {
	files := form.File[input]
	avatar := files[0]
	if avatar.Size > h.maxUploadSize {
		h.errorPage(
			c,
			http.StatusRequestEntityTooLarge,
			fmt.Sprintf(h.localize(c, "ErrMsgFileIsTooBig"), avatar.Filename),
		)
		return false
	}

	mime := avatar.Header.Get("Content-Type")
	if mime != "image/webp" {
		h.errorPage(
			c,
			http.StatusBadRequest,
			fmt.Sprintf(h.localize(c, "ErrMsgUnknownMimeType"), avatar.Filename, mime),
		)
		return false
	}

	src, err := avatar.Open()
	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to receive form file")
		h.errorPage(
			c,
			http.StatusBadRequest,
			fmt.Sprintf(h.localize(c, "ErrMsgBrokenFile"), avatar.Filename),
		)
		return false
	}

	defer src.Close()

	err = h.storage.WriteKey(h.ctx, key, src, avatar.Size, true)
	h.log.WithFields(logrus.Fields{
		"key": key,
	}).Debug("File uploaded")

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to uload file to storage")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			fmt.Sprintf(h.localize(c, "ErrMsgFailedToUploadFile"), avatar.Filename),
		)
		return false
	}
	return true
}

func (h *Handler) fetchMedia(c *gin.Context, key string) {

	size, reader, err := h.storage.GetKey(h.ctx, key, true)
	if reader != nil {
		defer reader.Close()
	}
	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Error while fetching media")
		h.errorPage(c, http.StatusNotFound, "")
		return
	}
	//c.Header("ETag", etag)
	c.Header("Content-Length", fmt.Sprintf("%d", size))
	c.Header("Content-Type", "image/webp")
	c.Header("Cache-Control", "public, max-age=31536000, immutable") // one year
	if _, err := io.Copy(c.Writer, reader); err != nil {
		h.log.WithFields(logrus.Fields{
			"key": key,
			"err": err,
		}).Error("Error streaming media")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedToLoadFile"),
		)
	}
}

// Middleware

func (h *Handler) headersMiddleware(c *gin.Context) {
	c.Header("Service-Worker-Allowed", "/")
	c.Next()
}

func (h *Handler) sessionMiddleware(c *gin.Context) {
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
		h.log.Error("Request with broken uid; failed to convert to uint")
		sess.Clear()
		sess.Save()
		c.Redirect(http.StatusTemporaryRedirect, "/")
		return
	}
	user.ID = uint(uid)
	err = h.db.GetUser(&user)
	if err != nil {
		h.log.WithFields(logrus.Fields{
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

func (h *Handler) authMiddleware(c *gin.Context) {
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

func (h *Handler) langMiddleware(c *gin.Context) {
	sess := sessions.Default(c)
	lang := sess.Get("Lang")
	if lang == nil {
		c.Set("Lang", "en")
	} else {
		c.Set("Lang", lang.(string))
	}
	c.Next()
}

// Routes

func (h *Handler) indexRoute(c *gin.Context) {
	h.execHTML(c, http.StatusOK, "page_index.html", gin.H{
		"Title": h.localize(c, "TitleMain"),
	})
}

func (h *Handler) faqRoute(c *gin.Context) {
	h.execHTML(c, http.StatusOK, "page_faq.html", gin.H{
		"Title": h.localize(c, "TitleFaq"),
	})
}

func (h *Handler) tutorialRoute(c *gin.Context) {
	h.execHTML(c, http.StatusOK, "page_tutorial.html", gin.H{
		"Title": h.localize(c, "TitleTutorial"),
	})
}

func (h *Handler) cardRoute(c *gin.Context) {
	cid, err := getUintParam(c, "id")
	if err != nil {
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidCardID"),
		)
		return
	}

	user := getUser(c)

	card, err := h.db.GetCard(cid)
	if err != nil {
		h.execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
		return
	}

	is_owner := false

	if user != nil {
		is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
	}
	if !is_owner && card.Fields.IsHidden {
		h.execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
		return
	}

	h.execHTML(c, http.StatusOK, "page_card.html", gin.H{
		"Title":   card.Fields.Name,
		"Card":    card,
		"Owner":   is_owner,
		"EditUrl": fmt.Sprintf("/editor/%d", cid),
	})
}

func (h *Handler) mediaRoute(c *gin.Context) {
	kind := c.Params.ByName("kind")
	id := c.Params.ByName("id")

	allowed := map[string]bool{
		"logo":   true,
		"avatar": true,
	}

	if _, ok := allowed[kind]; !ok {
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgIsNotFound_"+kind),
		)
		return
	}

	// TODO: Filter ID for security

	h.fetchMedia(c, "media/"+kind+"/"+id)
}

func (h *Handler) authProviderRoute(c *gin.Context) {
	provider := c.Param("provider")
	q := c.Request.URL.Query()
	q.Add("provider", provider)
	c.Request.URL.RawQuery = q.Encode()

	gothic.BeginAuthHandler(c.Writer, c.Request)
}

func (h *Handler) authCallbackRoute(c *gin.Context) {
	provider := c.Param("provider")
	q := c.Request.URL.Query()
	q.Add("provider", provider)
	c.Request.URL.RawQuery = q.Encode()

	user, err := gothic.CompleteUserAuth(c.Writer, c.Request)
	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to complete auth")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedAuth500"),
		)
		return
	}

	pid, name := UserCreds(user)

	id, err := h.db.SignUser(pid, name)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to complete auth")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedAuth500"),
		)
		return
	}

	h.log.WithFields(logrus.Fields{
		"pid":  pid,
		"uid":  id,
		"name": name,
	}).Info("Logged in")
	sess := sessions.Default(c)
	sess.Set("user_id", id)
	sess.Save()

	redirect(c, "/cards")
}

func checkTelegramAuthorization(params map[string]string) (map[string]string, error) {
	// Extract and remove hash
	checkHash, ok := params["hash"]
	if !ok {
		return nil, fmt.Errorf("hash parameter missing")
	}
	delete(params, "hash")

	// Build data check string
	var dataCheckArr []string
	for key, value := range params {
		dataCheckArr = append(dataCheckArr, key+"="+value)
	}
	sort.Strings(dataCheckArr)
	dataCheckString := strings.Join(dataCheckArr, "\n")

	// Compute secret key
	secretKey := sha256.Sum256([]byte(os.Getenv("TG_BOT_TOKEN")))

	// Compute HMAC-SHA256 of data_check_string
	h := hmac.New(sha256.New, secretKey[:])
	h.Write([]byte(dataCheckString))
	computedHash := hex.EncodeToString(h.Sum(nil))

	// Compare hashes
	if !hmac.Equal([]byte(computedHash), []byte(checkHash)) {
		return nil, fmt.Errorf("data is NOT from Telegram")
	}

	// Check auth_date
	authDateStr, ok := params["auth_date"]
	if !ok {
		return nil, fmt.Errorf("auth_date parameter missing")
	}
	authDateInt, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid auth_date: %v", err)
	}
	if time.Now().Unix()-authDateInt > 86400 {
		return nil, fmt.Errorf("data is outdated")
	}

	return params, nil
}

func (h *Handler) authTgRoute(c *gin.Context) {
	h.log.Warn("Start tg login")
	// Collect GET parameters
	query := c.Request.URL.Query()
	params := make(map[string]string)
	for key, values := range query {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}

	// Verify Telegram authorization data
	authData, err := checkTelegramAuthorization(params)
	if err != nil {
		h.log.Warnf("%v\n", err)
		c.String(http.StatusUnauthorized, err.Error())
		return
	}

	h.log.Warnf("%v\n", authData)
}

func (h *Handler) authVkRoute(c *gin.Context) {
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
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to complete VK auth")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			"Failed to contact VK",
		)
		return
	}

	if resp.StatusCode != http.StatusOK {
		h.log.WithFields(logrus.Fields{
			"status": resp.StatusCode,
		}).Error("Failed to complete VK auth")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			"VK API error",
		)
		return
	}

	// Decode the JSON response
	var info VKUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to complete VK auth")
		h.errorPage(
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

	id, err := h.db.SignUser(pid, name)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to complete auth")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedAuth500"),
		)
		return
	}

	h.log.WithFields(logrus.Fields{
		"pid":  pid,
		"uid":  id,
		"name": name,
	}).Info("Logged in")
	sess := sessions.Default(c)
	sess.Set("user_id", id)
	sess.Save()

	redirect(c, "/cards")
}

func (h *Handler) cardManifestRoute(c *gin.Context) {
	cid, err := getUintParam(c, "id")
	if err != nil {
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidCardID"),
		)
		return
	}

	user := getUser(c)

	card, err := h.db.GetCard(cid)
	if err != nil {
		h.execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
		return
	}

	is_owner := false

	if user != nil {
		is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
	}
	if !is_owner && card.Fields.IsHidden {
		h.execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
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
}

func (h *Handler) cardWorkerRoute(c *gin.Context) {
	cid, err := getUintParam(c, "id")
	if err != nil {
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidCardID"),
		)
		return
	}

	user := getUser(c)

	card, err := h.db.GetCard(cid)
	if err != nil {
		h.execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
		return
	}

	is_owner := false

	if user != nil {
		is_owner = card.Owner == user.ID || user.Type == UserTypeAdmin
	}
	if !is_owner && card.Fields.IsHidden {
		h.execHTML(c, http.StatusNotFound, "page_cardNotFound.html", gin.H{})
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
}

func (h *Handler) loginRoute(c *gin.Context) {
	h.execHTML(c, http.StatusOK, "page_login.html", gin.H{
		"Title":     h.localize(c, "TitleLogin"),
		"Providers": h.providers,
	})
}

func (h *Handler) loginVkRoute(c *gin.Context) {
	h.execHTML(c, http.StatusOK, "page_login_vk.html", gin.H{
		"Title":      "VK login",
		"vkapp":      os.Getenv("VK_CLIENT_ID"),
		"vkredirect": os.Getenv("VK_CLIENT_CALLBACK_URL"),
	})
}

func (h *Handler) logoutRoute(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Clear()
	sess.Save()
	redirect(c, "/")
}

func (h *Handler) userDelRoute(c *gin.Context) {
	user := getUser(c)

	h.db.DeleteUser(user.ID)

	sess := sessions.Default(c)
	sess.Clear()
	sess.Save()
	redirect(c, "/")
}

func (h *Handler) userDelAdminRoute(c *gin.Context) {
	user := getUser(c)

	if user.Type != UserTypeAdmin {
		h.errorPage(c, http.StatusNotFound, "")
		return
	}

	uid, err := getUintParam(c, "id")

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Wrong user id")
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgBrokenUserID"),
		)
		return
	}

	err = h.db.DeleteUser(uid)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to delete user")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedTODeleteUser"),
		)
		return
	}

	redirect(c, "/users")
}

func (h *Handler) cardsRoute(c *gin.Context) {
	user := getUser(c)

	uid, err := getUintParam(c, "id")

	if err != nil {
		redirect(c, fmt.Sprintf("/cards/%d", user.ID))
		return
	}

	if user.Type != UserTypeAdmin && uid != user.ID {
		redirect(c, fmt.Sprintf("/cards/%d", user.ID))
	}

	cards, err := h.db.ListCards(uid)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to list cards")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedToListCards"),
		)
		return
	}

	h.execHTML(c, http.StatusOK, "page_cards.html", gin.H{
		"Title": h.localize(c, "TitleCards"),
		"Cards": cards,
	})
}

func (h *Handler) delCardRoute(c *gin.Context) {
	user := getUser(c)

	cid, err := getUintParam(c, "id")

	if err != nil {
		redirect(c, "/cards")
		return
	}

	card, err := h.db.GetCard(cid)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"cid": cid,
			"err": err,
		}).Error("Failed to find a card")
		redirect(c, "/cards")
		return
	}

	if card.Owner != user.ID && user.Type != UserTypeAdmin {
		h.errorPage(
			c,
			http.StatusForbidden,
			h.localize(c, "ErrMsgCardIsOwnedByAnotherUser"),
		)
		return
	}

	err = h.db.DeleteCard(cid)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"cid": cid,
			"err": err,
		}).Error("Failed to delete a card")
	}

	redirect(c, fmt.Sprintf("/cards/%d", card.Owner))
}

func (h *Handler) newCardRoute(c *gin.Context) {
	h.execHTML(c, http.StatusOK, "page_editor.html", gin.H{
		"Title":        h.localize(c, "TitleCreateNewCard"),
		"EditUrl":      "/new",
		"SubmitButton": "CreateCard",
		"Card":         Card{},
	})
}

func (h *Handler) editCardRoute(c *gin.Context) {
	user := getUser(c)

	cid, err := getUintParam(c, "id")

	if err != nil {
		redirect(c, "/cards")
		return
	}

	card, err := h.db.GetCard(cid)
	if err != nil {
		redirect(c, "/cards")
		return
	}

	if card.Owner != user.ID && user.Type != UserTypeAdmin {
		redirect(c, "/cards")
		return
	}

	h.execHTML(c, http.StatusOK, "page_editor.html", gin.H{
		"Title":        h.localize(c, "TitleEditCard"),
		"EditUrl":      fmt.Sprintf("/update/%d", cid),
		"SubmitButton": "UpdateCard",
		"Card":         card,
	})
}

// TODO: Merge createCardRoute & updateCardRoute
func (h *Handler) createCardRoute(c *gin.Context) {
	user := getUser(c)

	var fields CardFields

	if err := c.Bind(&fields); err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to bind form data")
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidFromData"),
		)
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to get multipart form data")
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidFromData"),
		)
		return
	}

	card, err := h.db.CreateCard(user.ID, fields)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to create card")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedToCreateCard500"),
		)
		return
	}

	avatar := fmt.Sprintf("media/avatar/%d-%s.webp", card.ID, uuid.New().String())
	if isFileInForm(form, "avatar") {
		if !h.uploadFormFile(c, form, "avatar", avatar) {
			return
		}
		card.Avatar = avatar
		err = h.db.UpdateCard(card)
		if err != nil {
			h.log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Failed to upload avatar")
			h.errorPage(
				c,
				http.StatusInternalServerError,
				h.localize(c, "ErrMsgFailedToUploadAvatar"),
			)
			return
		}
	}

	logo := fmt.Sprintf("media/logo/%d-%s.webp", card.ID, uuid.New().String())
	if isFileInForm(form, "logo") {
		if !h.uploadFormFile(c, form, "logo", logo) {
			return
		}
		card.Logo = logo
		err = h.db.UpdateCard(card)
		if err != nil {
			h.log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Failed to upload logo")
			h.errorPage(
				c,
				http.StatusInternalServerError,
				h.localize(c, "ErrMsgFailedToUploadLogo"),
			)
			return
		}
	}

	redirect(c, "/cards")
}

func (h *Handler) updateCardRoute(c *gin.Context) {
	user := getUser(c)

	cid, err := getUintParam(c, "id")

	if err != nil {
		redirect(c, "/cards")
		return
	}

	card, err := h.db.GetCard(cid)
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
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to bind form data")
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidFromData"),
		)
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to get multipart form data")
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgInvalidFromData"),
		)
		return
	}

	fields.IsHidden = card.Fields.IsHidden // TODO: Make it less ugly
	card.Fields = fields
	err = h.db.UpdateCard(card)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to update the card")
		h.errorPage(c, http.StatusBadRequest, "Failed to update card content")
		return
	}

	avatar := fmt.Sprintf("media/avatar/%d-%s.webp", card.ID, uuid.New().String())
	if isFileInForm(form, "avatar") {
		if !h.uploadFormFile(c, form, "avatar", avatar) {
			return
		}
		old_avatar := card.Avatar
		card.Avatar = avatar
		err = h.db.UpdateCard(card)
		if err != nil {
			h.log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Failed to upload avatar")
			h.errorPage(c, http.StatusInternalServerError, "Failed to upload avatar")
			return
		}
		if old_avatar != "" {
			err := h.storage.DelKey(h.ctx, old_avatar)
			if err != nil {
				h.log.WithFields(logrus.Fields{
					"err":    err,
					"avatar": old_avatar,
				}).Error("Failed to delete previous avatar")
			}
		}
	}

	logo := fmt.Sprintf("media/logo/%d-%s.webp", card.ID, uuid.New().String())
	if isFileInForm(form, "logo") {
		if !h.uploadFormFile(c, form, "logo", logo) {
			return
		}
		old_logo := card.Logo
		card.Logo = logo
		err = h.db.UpdateCard(card)
		if err != nil {
			h.log.WithFields(logrus.Fields{
				"err": err,
			}).Error("Failed to upload logo")
			h.errorPage(c, http.StatusInternalServerError, "Failed to upload logo")
			return
		}
		if old_logo != "" {
			err := h.storage.DelKey(h.ctx, old_logo)
			if err != nil {
				h.log.WithFields(logrus.Fields{
					"err":  err,
					"logo": old_logo,
				}).Error("Failed to delete previous logo")
			}
		}
	}

	redirect(c, fmt.Sprintf("/cards/%d", card.Owner))
}

func (h *Handler) changeCardVisibilityRoute(c *gin.Context) {
	user := getUser(c)

	cid, err := getUintParam(c, "id")

	if err != nil {
		h.errorBlock(c, http.StatusBadRequest, "")
		return
	}

	card, err := h.db.GetCard(cid)
	if err != nil {
		h.errorBlock(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgInvalidFromData"),
		)
		return
	}

	if card.Owner != user.ID && user.Type != UserTypeAdmin {
		h.errorBlock(
			c,
			http.StatusForbidden,
			h.localize(c, "ErrMsgCardIsOwnedByAnotherUser"),
		)
		return
	}

	switch c.Query("visible") {
	case "true":
		card.Fields.IsHidden = false
		err = h.db.UpdateCard(card)
	case "false":
		card.Fields.IsHidden = true
		err = h.db.UpdateCard(card)
	}

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to update card visibility")
	}

	h.execHTML(c, http.StatusOK, "comp_cardElement.html", gin.H{
		"Card": card,
	})
}

func (h *Handler) listUsersRoute(c *gin.Context) {
	user := getUser(c)

	if user.Type != UserTypeAdmin {
		h.errorPage(c, http.StatusNotFound, "")
		return
	}

	users, err := h.db.ListUsers()

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to list users")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			h.localize(c, "ErrMsgFailedToListUsers"),
		)
		return
	}

	h.execHTML(c, http.StatusOK, "page_users.html", gin.H{
		"Title": h.localize(c, "TitleUsers"),
		"Users": users,
	})
}

func (h *Handler) setLocaleRoute(c *gin.Context) {
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
}

func (h *Handler) changeUserTypeRoute(c *gin.Context) {
	user := getUser(c)

	if user.Type != UserTypeAdmin {
		h.errorPage(c, http.StatusNotFound, "")
		return
	}

	uid, err := getUintParam(c, "id")

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Wrong user id")
		h.errorPage(
			c,
			http.StatusBadRequest,
			h.localize(c, "ErrMsgBrokenUserID"),
		)
		return
	}

	typ, err := getUintParam(c, "typ")

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed get user type param")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			"",
		)
		return
	}

	target := User{ID: uid}

	err = h.db.GetUser(&target)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to delete user")
		h.errorPage(
			c,
			http.StatusNotFound,
			"",
		)
		return
	}

	target.Type = typ

	err = h.db.UpdateUser(target)

	if err != nil {
		h.log.WithFields(logrus.Fields{
			"err": err,
		}).Error("Failed to update user")
		h.errorPage(
			c,
			http.StatusInternalServerError,
			"",
		)
		return
	}

	redirect(c, "/users")
}
