package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestParsePackURLGivesOneIdentityForEveryFormAndCase(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/Acme/My-Pack.git",
		"https://github.com/acme/my-pack",
		"https://GitHub.com/ACME/MY-PACK",
		"ssh://git@github.com/acme/my-pack.git",
		"ssh://git@github.com/Acme/My-Pack",
		"git@github.com:acme/my-pack",
		"git@github.com:Acme/My-Pack.git",
		"https://github.com/acme/my-pack/",
		"https://github.com/acme/my-pack.git/",
	} {
		src, err := app.ParsePackURL(raw)
		if err != nil {
			t.Errorf("ParsePackURL(%q) error = %v", raw, err)
			continue
		}
		if src.ID != "github.com/acme/my-pack" || src.URL != raw {
			t.Errorf("ParsePackURL(%q) = %+v, want the identity github.com/acme/my-pack and the URL as given", raw, src)
		}
	}
}

func TestParsePackURLKeepsTheRepositoryCharacters(t *testing.T) {
	src, err := app.ParsePackURL("https://github.com/a-b-c/x_y.z-1")
	if err != nil || src.ID != "github.com/a-b-c/x_y.z-1" {
		t.Errorf("ParsePackURL() = %+v, %v", src, err)
	}
}

func TestParsePackURLRefusesAnotherHost(t *testing.T) {
	for _, raw := range []string{
		"https://gitlab.com/acme/pack",
		"ssh://git@gitlab.com/acme/pack.git",
		"git@bitbucket.org:acme/pack.git",
		"https://github.com.evil.example/acme/pack",
		"https://www.github.com/acme/pack",
	} {
		_, err := app.ParsePackURL(raw)
		if _, ok := errors.AsType[*app.HostUnsupportedError](err); !ok {
			t.Errorf("ParsePackURL(%q) error = %v, want a HostUnsupportedError", raw, err)
		} else if !strings.Contains(err.Error(), "--dir") {
			t.Errorf("the error %q does not point to --dir", err)
		}
	}
}

func TestParsePackURLRefusesWhatIsNotAGitHubRepository(t *testing.T) {
	for _, raw := range []string{
		"",
		"acme/pack",
		`C:\packs\mine`,
		"C:/packs/mine",
		"/home/me/pack",
		"./pack",
		`\\server\share\pack`,
		"http://github.com/acme/pack",
		"git://github.com/acme/pack",
		"file:///tmp/pack",
		"https://github.com/acme",
		"https://github.com/acme/pack/tree/main",
		"https://github.com/acme/pack//",
		"https://github.com/con/pack",
		"https://github.com/LPT1/pack",
		"https://github.com/acme/pack?x=1",
		"https://github.com/acme/pack#main",
		"https://user@github.com/acme/pack",
		"https://github.com:443/acme/pack",
		"ssh://github.com/acme/pack",
		"ssh://root@github.com/acme/pack",
		"ssh://git@github.com:22/acme/pack",
		"ssh://-oProxyCommand=x@github.com/acme/pack",
		"git@github.com:/acme/pack",
		"git@github.com:acme",
		"https://github.com/-acme/pack",
		"https://github.com/acme-/pack",
		"https://github.com/ac--me/pack",
		"https://github.com/ac_me/pack",
		"https://github.com/acme/pa ck",
		"https://github.com/acme/pack.",
		"https://github.com/acme/..",
		"https://github.com/acme/.git",
		"https://github.com/acme/con",
		"https://github.com/acme/NUL.git",
		"https://github.com/acme/lpt1",
		"https://github.com/acme/pa%2Fck",
		"https://github.com/" + strings.Repeat("a", 40) + "/pack",
		"https://github.com/acme/" + strings.Repeat("r", 101),
	} {
		_, err := app.ParsePackURL(raw)
		if !errors.As(err, new(*app.UsageError)) {
			t.Errorf("ParsePackURL(%q) error = %v, want a usage error", raw, err)
		}
	}
}

func TestCheckPackRef(t *testing.T) {
	// A run of hexadecimal digits may be a tag or a branch: the fetch tells
	// it from an abbreviated SHA.
	for _, ref := range []string{"", "main", "v1.2.3", "refs/tags/v1", "feature/x", "beef", "abcdef1",
		strings.Repeat("a", 39), strings.Repeat("a", 40), strings.Repeat("0", 64)} {
		if err := app.CheckPackRef(ref); err != nil {
			t.Errorf("CheckPackRef(%q) error = %v", ref, err)
		}
	}
	for ref, why := range map[string]string{
		"-u":              "-",
		"--upload-pack=x": "-",
		"ma in":           "",
		"main\n":          "",
		"main:refs/x":     ":",
		"refs/*":          "*",
		"v?":              "?",
		"v[1]":            "[",
		"a\\b":            "\\",
		"main^":           "^",
		"main~1":          "~",
		"a..b":            "..",
		"main@{1}":        "@{",
	} {
		err := app.CheckPackRef(ref)
		if !errors.As(err, new(*app.UsageError)) || !strings.Contains(err.Error(), why) {
			t.Errorf("CheckPackRef(%q) error = %v, want a usage error about %q", ref, err, why)
		}
	}
}
