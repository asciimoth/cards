package main

import (
	"os"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/discord"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
	"github.com/markbates/goth/providers/yandex"
	"github.com/sirupsen/logrus"
)

func SetupProviders(log *logrus.Logger) []string {
	providers := []goth.Provider{}
	names := []string{}

	ghClientID := os.Getenv("GITHUB_CLIENT_ID")
	ghClientSecret := os.Getenv("GITHUB_CLIENT_SECRET")
	ghClientCallbackURL := os.Getenv("GITHUB_CLIENT_CALLBACK_URL")

	if ghClientID != "" && ghClientSecret != "" && ghClientCallbackURL != "" {
		providers = append(providers, github.New(
			ghClientID, ghClientSecret, ghClientCallbackURL,
		))
		log.Debug("Adding github OAuth provider")
	}

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	googleClientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	googleClientCallbackURL := os.Getenv("GOOGLE_CLIENT_CALLBACK_URL")

	if googleClientID != "" && googleClientSecret != "" && googleClientCallbackURL != "" {
		providers = append(providers, google.New(
			googleClientID, googleClientSecret, googleClientCallbackURL,
		))
		log.Debug("Adding google OAuth provider")
	}

	dcClientID := os.Getenv("DISCORD_CLIENT_ID")
	dcClientSecret := os.Getenv("DISCORD_CLIENT_SECRET")
	dcClientCallbackURL := os.Getenv("DISCORD_CLIENT_CALLBACK_URL")

	if dcClientID != "" && dcClientSecret != "" && dcClientCallbackURL != "" {
		providers = append(providers, discord.New(
			dcClientID, dcClientSecret, dcClientCallbackURL,
		))
		log.Debug("Adding discord OAuth provider")
	}

	yandexClientID := os.Getenv("YANDEX_CLIENT_ID")
	yandexClientSecret := os.Getenv("YANDEX_CLIENT_SECRET")
	yandexClientCallbackURL := os.Getenv("YANDEX_CLIENT_CALLBACK_URL")

	if yandexClientID != "" && yandexClientSecret != "" && yandexClientCallbackURL != "" {
		providers = append(providers, yandex.New(
			yandexClientID, yandexClientSecret, yandexClientCallbackURL,
		))
		log.Debug("Adding yandex OAuth provider")
	}

	if os.Getenv("VK_CLIENT_ID") != "" {
		names = append(names, "vk")
		log.Debug("Adding VK OAuth provider")
	}

	if os.Getenv("TG_CLIENT_ID") != "" {
		names = append(names, "telegram")
		log.Debug("Adding Tg OAuth provider")
	}

	if len(providers) < 1 {
		log.Fatal("There is no OAuth providers configured")
	}

	for _, p := range providers {
		names = append(names, p.Name())
	}

	goth.UseProviders(providers...)

	return names
}

func UserCreds(user goth.User) (id string, name string) {
	id = user.Provider + "::" + user.UserID
	name = user.NickName
	if name != "" {
		return
	}
	name = user.Name
	if name != "" {
		return
	}
	name = user.FirstName
	if name != "" {
		if user.LastName != "" {
			name += " " + user.LastName
		}
		return
	}
	name = user.Email
	return
}
