package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestAsk(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "Yes\r\n": true, " y \n": true, "\n": false, "n\n": false, "yep\n": false, "": false, "y": true} {
		var out strings.Builder
		got, err := ask(context.Background(), strings.NewReader(in), &out, "Overwrite it? [y/N] ")
		if err != nil || got != want || out.String() != "Overwrite it? [y/N] " {
			t.Errorf("ask(%q) = %v, %v, printed %q, want %v after the question", in, got, err, out.String(), want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, w := io.Pipe() // never answers
	defer w.Close()
	if got, err := ask(ctx, r, io.Discard, "?"); got || !errors.Is(err, context.Canceled) {
		t.Errorf("ask() after Ctrl-C = %v, %v, want no and the cancellation", got, err)
	}
}

// TestTerminalPromptNeedsBothTerminals checks that the question is asked
// only when stdin and stdout are both terminals: piped to tee, for one,
// no one sees it, so there is no prompt and the command refuses instead.
func TestTerminalPromptNeedsBothTerminals(t *testing.T) {
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer out.Close()
	for _, tt := range []struct {
		terminals []uintptr
		want      bool
	}{
		{[]uintptr{in.Fd(), out.Fd()}, true},
		{[]uintptr{in.Fd()}, false},
		{[]uintptr{out.Fd()}, false},
		{nil, false},
	} {
		isTerm := func(fd int) bool { return slices.Contains(tt.terminals, uintptr(fd)) }
		if got := terminalPrompt(in, out, isTerm) != nil; got != tt.want {
			t.Errorf("terminalPrompt() with terminals %v gives a prompt: %v, want %v", tt.terminals, got, tt.want)
		}
	}
}
