package i18n

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/ImmortallKIng/crtctl/internal/i18n/extract"
)

const root = "../.."

func TestCatalogsComplete(t *testing.T) {
	msgs, err := extract.Messages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 100 {
		t.Fatalf("найдено подозрительно мало строк: %d", len(msgs))
	}
	used := map[string]bool{}
	for _, m := range msgs {
		used[m.ID] = true
	}
	for _, lang := range []string{"ru", "es", "de", "fr"} {
		cat := catalogs[lang]
		var missing []string
		for _, m := range msgs {
			tr, ok := cat[m.ID]
			if !ok || strings.TrimSpace(tr) == "" {
				missing = append(missing, fmt.Sprintf("%q: \"\", // %s", m.ID, m.Pos))
				continue
			}
			if a, b := extract.Verbs(m.ID), extract.Verbs(tr); fmt.Sprint(a) != fmt.Sprint(b) {
				t.Errorf("%s: плейсхолдеры не совпадают\n  en %v: %q\n  %s %v: %q", lang, a, m.ID, lang, b, tr)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s: нет перевода для %d строк:\n%s", lang, len(missing), strings.Join(missing, "\n"))
		}
		var stale []string
		for k := range cat {
			if !used[k] {
				stale = append(stale, fmt.Sprintf("%q", k))
			}
		}
		sort.Strings(stale)
		if len(stale) > 0 {
			t.Errorf("%s: устаревшие строки в каталоге (%d):\n%s", lang, len(stale), strings.Join(stale, "\n"))
		}
	}
}

func TestNoCyrillicOutsideCatalogs(t *testing.T) {
	lits, err := extract.CyrillicLiterals(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lits {
		t.Errorf("кириллица в коде вне каталога: %s %q", l.Pos, l.ID)
	}
}

func TestDetect(t *testing.T) {
	for _, c := range []struct{ env, want string }{
		{"ru_RU.UTF-8", "ru"}, {"de_DE.UTF-8", "de"}, {"fr_CA.UTF-8", "fr"}, {"es_ES@euro", "es"},
		{"ja_JP.UTF-8", "en"}, {"C", "en"}, {"", "en"},
	} {
		os.Setenv("CRTCTL_LANG", "")
		os.Setenv("LC_ALL", "")
		os.Setenv("LC_MESSAGES", "")
		os.Setenv("LANG", c.env)
		if got := Detect(); got != c.want {
			t.Errorf("LANG=%q: %s, want %s", c.env, got, c.want)
		}
	}
	os.Setenv("LANG", "de_DE.UTF-8")
	os.Setenv("CRTCTL_LANG", "fr")
	if got := Detect(); got != "fr" {
		t.Errorf("CRTCTL_LANG должен побеждать LANG: %s", got)
	}
}

func TestFormatting(t *testing.T) {
	defer Set("en")
	Set("ru")
	if got := T("%d bytes from /dev/i2c-%d", 128, 7); !strings.Contains(got, "128") || !strings.Contains(got, "i2c-7") {
		t.Errorf("перевод с аргументами: %q", got)
	}
	if err := Set("xx"); err == nil {
		t.Error("неизвестный язык должен давать ошибку")
	}
}
