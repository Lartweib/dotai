package cli

import (
	"context"
	"errors"
	"io"
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
