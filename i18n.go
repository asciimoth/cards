package main

import (
	"path/filepath"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/sirupsen/logrus"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

func SetupLocales(log *logrus.Logger) (func(string, string) string, []string) {
	names := []string{}
	b := i18n.NewBundle(language.English)
	b.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

	files, err := filepath.Glob("locales/*.yaml")
	if err != nil {
		log.Fatalf("failed to read locale files: %v", err)
	}

	if len(files) == 0 {
		log.Fatal("no locale files found in ./locales")
	}

	for _, file := range files {
		base := filepath.Base(file)
		name := strings.TrimSuffix(base, filepath.Ext(base))
		names = append(names, name)
		if _, err := b.LoadMessageFile(file); err != nil {
			log.Fatalf("failed to load locale file %s: %v", file, err)
		}
	}

	localizer := func(key string, locale string) string {
		// Create a localizer for given locale
		localizer := i18n.NewLocalizer(b, locale)
		msg, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: key})
		if err != nil {
			return "<<" + key + ">>"
		}
		return msg
	}

	return localizer, names
}
