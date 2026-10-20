// Package i18n translates user-facing messages. Source strings are English and act as
// message IDs; catalogs map them to other languages. Missing translations fall back to English.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Переводы лежат в locales/*.json: {"английская строка": {"ru": "...", "es": "...", ...}}.
//
//go:embed locales/*.json
var locales embed.FS

var languages = []string{"en", "ru", "es", "de", "fr"}

var catalogs = loadCatalogs()

func loadCatalogs() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, l := range languages {
		out[l] = map[string]string{}
	}
	files, _ := locales.ReadDir("locales")
	for _, f := range files {
		b, err := locales.ReadFile("locales/" + f.Name())
		if err != nil {
			panic(err)
		}
		var m map[string]map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			panic("i18n: locales/" + f.Name() + ": " + err.Error())
		}
		for id, tr := range m {
			for l, s := range tr {
				if c, ok := out[l]; ok {
					c[id] = s
				}
			}
		}
	}
	out["en"] = nil
	return out
}

var current = Detect()

// Supported returns language codes in a stable order.
func Supported() []string {
	out := append([]string{}, languages...)
	sort.Strings(out)
	return out
}

// Detect picks the language from CRTCTL_LANG, then LC_ALL, LC_MESSAGES, LANG.
func Detect() string {
	for _, v := range []string{"CRTCTL_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if l := normalize(os.Getenv(v)); l != "" {
			if _, ok := catalogs[l]; ok {
				return l
			}
			if v == "CRTCTL_LANG" {
				continue
			}
			return "en" // locale is set but unsupported
		}
	}
	return "en"
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "c" || s == "posix" {
		return ""
	}
	for _, sep := range []string{"_", ".", "@", "-"} {
		s, _, _ = strings.Cut(s, sep)
	}
	return s
}

// Set switches the language; "" keeps the detected one.
func Set(lang string) error {
	if lang == "" {
		return nil
	}
	l := normalize(lang)
	if _, ok := catalogs[l]; !ok {
		return fmt.Errorf("unsupported language %q (supported: %s)", lang, strings.Join(Supported(), ", "))
	}
	current = l
	return nil
}

func Lang() string { return current }

// T translates msg and formats it with args like fmt.Sprintf.
func T(msg string, args ...any) string {
	if c := catalogs[current]; c != nil {
		if tr, ok := c[msg]; ok && tr != "" {
			msg = tr
		}
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Errorf is fmt.Errorf with a translated format; %w is supported.
func Errorf(format string, args ...any) error {
	if c := catalogs[current]; c != nil {
		if tr, ok := c[format]; ok && tr != "" {
			format = tr
		}
	}
	return fmt.Errorf(format, args...)
}

// Pick returns the localized variant from a map like {"en": "...", "ru": "..."}.
func Pick(base string, l10n map[string]string) string {
	if v, ok := l10n[current]; ok && v != "" {
		return v
	}
	return base
}

// N помечает строку для перевода там, где она переводится позже (через T(переменная)).
func N(s string) string { return s }

// All — перевод строки на все языки (для данных, которые хранятся в файлах, например профилей).
func All(msg string) map[string]string {
	out := map[string]string{}
	for _, l := range languages {
		if c := catalogs[l]; c != nil && c[msg] != "" {
			out[l] = c[msg]
		}
	}
	return out
}
