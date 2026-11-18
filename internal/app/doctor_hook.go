package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CodeOnboardingHookStale: the session-start hook of decision 0045 in a
// harness's hooks file runs a dotai binary that is gone, as after an
// upgrade or a move, so that every new session runs a missing command, or
// one that is not the dotai that runs doctor. dotai install writes it
// again with the binary that runs it.
const CodeOnboardingHookStale = "onboarding-hook-stale"

// hookFile reports whether target, a file of the install state, may hold
// the session-start hook as rulesync renders it: a JSON hooks or settings
// file, or OpenCode's hooks plugin.
func hookFile(target string) bool {
	switch strings.ToLower(filepath.Ext(target)) {
	case ".json", ".jsonc", ".js", ".mjs", ".cjs", ".ts":
		return true
	}
	return false
}

// checkHook checks the session-start hooks of the harness id in data,
// what the file target of the install state holds: each must run a dotai
// binary that exists and, when the binary that runs doctor is known, that
// is the same file.
func (doc *doctorRun) checkHook(target, id string, data []byte) {
	for _, bin := range hookBinaries(data, id) {
		why := doc.staleHook(bin)
		if why == "" {
			continue
		}
		doc.add(Finding{Code: CodeOnboardingHookStale, Harness: id, Path: target,
			Message: fmt.Sprintf("the session-start hook of %s in %s runs %s, %s: run dotai install with the dotai you use, which writes the hook again with that binary", id, target, bin, why)})
	}
}

// staleHook says why the hook binary bin is stale, "" when it is not.
func (doc *doctorRun) staleHook(bin string) string {
	fi, err := os.Stat(bin)
	if err != nil || !fi.Mode().IsRegular() {
		return "which is gone, so every new session runs a missing command"
	}
	if doc.exe == nil {
		return ""
	}
	exe, err := doc.exe()
	if err != nil {
		return ""
	}
	ei, err := os.Stat(exe)
	if err != nil || os.SameFile(fi, ei) {
		return ""
	}
	return fmt.Sprintf("which is not the dotai that runs now, %s", exe)
}

// hookBinaries are the dotai binaries the session-start hooks of the
// harness id in data run, each once, in order: each command ends with
// onboarding hook --harness <id>, after the binary in double quotes, as
// Claude Code and OpenCode run it, or bare, as agy does (hookCommand),
// whether data holds the command as written or inside a JSON string.
func hookBinaries(data []byte, id string) []string {
	suffix := " onboarding hook --harness " + id
	var out []string
	for rest := string(data); ; {
		i := strings.Index(rest, suffix)
		if i < 0 {
			return out
		}
		end := i + len(suffix)
		longer := end < len(rest) && (rest[end] == '-' || rest[end] == '_' || 'a' <= rest[end] && rest[end] <= 'z' || '0' <= rest[end] && rest[end] <= '9')
		if bin := hookBinary(rest[:i]); !longer && bin != "" && !slices.Contains(out, bin) {
			out = append(out, bin)
		}
		rest = rest[end:]
	}
}

// hookBinary is the binary at the end of before, the text before
// " onboarding hook": in double quotes, which a JSON string escapes as \",
// with forward slashes, or bare, without whitespace or quotes, whose
// backslashes a JSON string doubles.
func hookBinary(before string) string {
	switch {
	case strings.HasSuffix(before, `\"`):
		inner := before[:len(before)-2]
		if j := strings.LastIndex(inner, `\"`); j >= 0 {
			return filepath.FromSlash(inner[j+2:])
		}
		return ""
	case strings.HasSuffix(before, `"`):
		inner := before[:len(before)-1]
		if j := strings.LastIndex(inner, `"`); j >= 0 {
			return filepath.FromSlash(inner[j+1:])
		}
		return ""
	}
	bin := before[strings.LastIndexAny(before, "\"'`\t\r\n ")+1:]
	if strings.Contains(bin, `\\`) {
		bin = strings.ReplaceAll(bin, `\\`, `\`)
	}
	return bin
}
