package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/server"
)

const uiURL = "http://127.0.0.1:4321/?token=abc"

// uiRun is what a faked dotai ui was given: whether it started, its
// options, and the addresses the browser was asked to open.
type uiRun struct {
	started bool
	opts    server.Options
	opened  []string
}

// uiEnv is the test environment with dotai ui faked: it reports ready at
// uiURL and returns err, recording what it was given in run.
func uiEnv(stdout, stderr *bytes.Buffer, run *uiRun, err error) cli.Env {
	env := testEnv(stdout, stderr)
	env.UI = func(ctx context.Context, opts server.Options) error {
		run.started, run.opts = true, opts
		if ctx == nil {
			return errors.New("no context")
		}
		if opts.Ready != nil {
			if rerr := opts.Ready(uiURL); rerr != nil {
				return rerr
			}
		}
		return err
	}
	env.OpenBrowser = func(url string) error {
		run.opened = append(run.opened, url)
		return nil
	}
	return env
}

func TestRunUIPrintsTheAddressAndOpensTheBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOTAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	var stdout, stderr bytes.Buffer
	var run uiRun
	if code := cli.Run([]string{"ui"}, uiEnv(&stdout, &stderr, &run, nil)); code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), uiURL) || !strings.Contains(stdout.String(), "Ctrl+C") {
		t.Errorf("stdout = %q, want the address and how to stop", stdout.String())
	}
	if len(run.opened) != 1 || run.opened[0] != uiURL {
		t.Errorf("opened = %v, want the address once", run.opened)
	}
	if run.opts.Home != home || !filepath.IsAbs(run.opts.CacheDir) || run.opts.Port != 0 {
		t.Errorf("options = %+v, want the home, an absolute cache and a free port", run.opts)
	}
}

func TestRunUIWithNoOpenAndAPort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	var run uiRun
	if code := cli.Run([]string{"ui", "--no-open", "--port", "8123"}, uiEnv(&stdout, &stderr, &run, nil)); code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr %q", code, stderr.String())
	}
	if len(run.opened) != 0 || run.opts.Port != 8123 {
		t.Errorf("opened = %v, port %d, want no browser and port 8123", run.opened, run.opts.Port)
	}
}

func TestRunUIJSONPrintsOneEnvelopeWithTheAddress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	var run uiRun
	if code := cli.Run([]string{"ui", "--json", "--no-open"}, uiEnv(&stdout, &stderr, &run, nil)); code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr %q", code, stderr.String())
	}
	var e struct {
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	dec := json.NewDecoder(&stdout)
	if err := dec.Decode(&e); err != nil || !e.OK || e.Command != "ui" || e.Data.URL != uiURL {
		t.Errorf("envelope = %+v, %v, want ui with the address", e, err)
	}
	if dec.More() {
		t.Error("stdout holds more than one envelope")
	}
}

func TestRunUIRefusesABadPort(t *testing.T) {
	for _, port := range []string{"-1", "65536", "http"} {
		var stdout, stderr bytes.Buffer
		var run uiRun
		if code := cli.Run([]string{"ui", "--port", port}, uiEnv(&stdout, &stderr, &run, nil)); code != cli.ExitUsage {
			t.Errorf("--port %s: exit code = %d, want %d", port, code, cli.ExitUsage)
		}
		if run.started {
			t.Errorf("--port %s: the server was started", port)
		}
	}
}

func TestRunUIFailureIsAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	var run uiRun
	env := uiEnv(&stdout, &stderr, &run, nil)
	env.UI = func(context.Context, server.Options) error { return errors.New("address already in use") }
	if code := cli.Run([]string{"ui", "--port", "8123"}, env); code != cli.ExitInternal || !strings.Contains(stderr.String(), "address already in use") {
		t.Errorf("exit code = %d, stderr %q, want the failure", code, stderr.String())
	}
}

func TestRunUIGoesOnWhenTheBrowserCannotOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	var run uiRun
	env := uiEnv(&stdout, &stderr, &run, nil)
	env.OpenBrowser = func(string) error { return errors.New("no display") }
	if code := cli.Run([]string{"ui"}, env); code != cli.ExitOK || !strings.Contains(stderr.String(), "no display") || !strings.Contains(stdout.String(), uiURL) {
		t.Errorf("exit code = %d, stdout %q, stderr %q, want the address and a note on stderr", code, stdout.String(), stderr.String())
	}
}

func TestRunUIJSONFailureAfterTheEnvelopeGoesToStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var stdout, stderr bytes.Buffer
	var run uiRun
	env := uiEnv(&stdout, &stderr, &run, errors.New("dotai ui stopped: accept failed"))
	if code := cli.Run([]string{"ui", "--json", "--no-open"}, env); code != cli.ExitInternal || !strings.Contains(stderr.String(), "accept failed") {
		t.Errorf("exit code = %d, stderr %q, want the failure on stderr", code, stderr.String())
	}
	out := stdout.String()
	dec := json.NewDecoder(strings.NewReader(out))
	var e struct {
		OK bool `json:"ok"`
	}
	if err := dec.Decode(&e); err != nil || !e.OK || dec.More() {
		t.Errorf("stdout = %q, want only the envelope of the running server", out)
	}
}
