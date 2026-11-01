//go:build !windows && !linux

package proc

import (
	"reflect"
	"testing"
)

func TestParsePS(t *testing.T) {
	comm := []byte("    1 /sbin/launchd\n  812 /Applications/My Tool.app/Contents/MacOS/claude\n 9000 node\nbad line\n")
	args := []byte("    1 /sbin/launchd\n  812 claude --resume\n 9000 node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js\n")
	want := []Process{
		{1, "launchd", "launchd", "/sbin/launchd"},
		{812, "claude", "claude", "claude --resume"},
		{9000, "node", "node", "node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js"},
	}
	if got := parsePS(comm, args); !reflect.DeepEqual(got, want) {
		t.Errorf("parsePS() = %+v, want %+v", got, want)
	}
}
