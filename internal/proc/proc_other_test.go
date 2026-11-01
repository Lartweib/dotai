//go:build !windows && !linux

package proc

import (
	"reflect"
	"testing"
)

func TestParsePS(t *testing.T) {
	out := []byte("    1 /sbin/launchd\n  812 /Applications/My Tool.app/Contents/MacOS/claude\n 9000 opencode-service-1-ab\nbad line\n")
	want := []Process{{1, "launchd"}, {812, "claude"}, {9000, "opencode-service-1-ab"}}
	if got := parsePS(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parsePS() = %+v, want %+v", got, want)
	}
}
