package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tailscale/hujson"

	"github.com/lartweib/dotai/internal/plan"
)

// writeRealHome replaces, in every file rulesync wrote into the scope tree
// scope of the staging tree root, the home directory rulesync ran with,
// that scope tree, by the real home directory of tools: rulesync 43.9.2
// writes a ~/ path of agy's permissions as an absolute path under its
// home, which would name a staging tree that is gone. The files of staged,
// which dotai wrote, keep their bytes.
//
// The home directory is replaced in each form rulesync can write it, by
// the real one in the same form: with forward slashes, with the OS's
// separators, and, in a .json or .jsonc file, escaped as a JSON string
// holds it, the real home directory then escaped too. The staging tree is
// matched as given and as it is on disk, and, on Windows and macOS, whose
// paths usually ignore case, without case. A match must end the path:
// rulesync's home directory followed by a letter is another path. Then any
// file rulesync wrote that still names the staging tree, with any
// separators, after any prefix of a network share, or percent-encoded as a
// file URL holds it, fails the stage: a file that is not UTF-8 text, or that
// holds a NUL byte, is not rewritten, as expandPaths does not expand one,
// but it is checked.
//
// A file that names the home directory fails the stage when the real one
// is unknown, or when a command cannot name it inside double quotes, as
// for ${DOTAI_HOME}, and the match lands in a command: in a hook or
// command of a JSON file, or anywhere in a file that is neither JSON nor
// markdown, where dotai cannot tell a command from data. Each file keeps
// its mode.
func writeRealHome(w stageFS, root, scope string, staged map[string]content, tools ToolPaths) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(abs, scope)
	// The staging tree as given and as it is on disk, its links followed
	// and, on Windows, its short names long, as a program may resolve it.
	roots := slices.Compact([]string{abs, realPath(abs)})
	fold := plan.FoldsCase(runtime.GOOS)
	names, err := w.Files(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		file := path.Join(scope, name)
		if _, ok := staged[file]; ok {
			continue
		}
		data, err := w.ReadFile(dir, name)
		if err != nil {
			return err
		}
		rewritten, ok, err := realHomeIn(file, data, abs, roots, scope, tools, fold)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		mode, err := w.Mode(dir, name)
		if err != nil {
			return err
		}
		if err := w.WriteFile(dir, name, rewritten, mode); err != nil {
			return err
		}
	}
	return nil
}

// realHomeIn is data, the file file rulesync wrote, with rulesync's home
// directory, the scope tree scope under each of roots, the staging tree
// abs, replaced by the real one of tools, and whether data named it. It
// fails when the result still names the staging tree.
func realHomeIn(file string, data []byte, abs string, roots []string, scope string, tools ToolPaths, fold bool) ([]byte, bool, error) {
	text := utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
	ok := false
	if text {
		rewritten, named, err := rewriteHome(file, string(data), roots, scope, tools, fold)
		if err != nil {
			return nil, false, err
		}
		if named {
			data, ok = []byte(rewritten), true
		}
	}
	if namesStaging(string(data), roots, fold) {
		how := "in a form dotai cannot replace with the real home directory"
		if !text {
			how = "in data that is not UTF-8 text, which dotai does not rewrite"
		}
		return nil, false, fmt.Errorf("%s, rendered by rulesync, names the staging tree %s, which is removed after the stage, %s", file, abs, how)
	}
	return data, ok, nil
}

// homeForm is a form of rulesync's home directory, from, and the real home
// directory in the same form, to.
type homeForm struct{ from, to string }

// rulesyncHomeForms are the forms of rulesync's home directory, the scope
// tree scope under each of roots, that the file file may hold, each with
// the real home directory home in the same form, the longest of each root
// first.
func rulesyncHomeForms(file string, roots []string, scope, home string) []homeForm {
	hslash, hnative := filepath.ToSlash(home), filepath.FromSlash(home)
	inJSON := isJSON(file)
	if inJSON {
		hslash = jsonStringText(hslash)
	}
	var forms []homeForm
	for _, root := range roots {
		dir := filepath.Join(root, scope)
		slash := filepath.ToSlash(dir)
		if inJSON && strings.Contains(dir, `\`) {
			forms = append(forms, homeForm{strings.ReplaceAll(dir, `\`, `\\`), jsonStringText(hnative)})
		}
		if dir != slash {
			forms = append(forms, homeForm{dir, hnative})
		}
		forms = append(forms, homeForm{slash, hslash})
	}
	return forms
}

// rewriteHome is the text of the file file with rulesync's home directory,
// the scope tree scope under each of roots, replaced by the real one of
// tools, and whether text named it.
func rewriteHome(file, text string, roots []string, scope string, tools ToolPaths, fold bool) (string, bool, error) {
	forms := rulesyncHomeForms(file, roots, scope, tools.Home)
	has := func(s string) bool {
		for i := range len(s) {
			if _, ok := formAt(s, i, forms, fold); ok {
				return true
			}
		}
		return false
	}
	if !has(text) {
		return text, false, nil
	}
	if tools.UserErr != nil {
		return "", false, fmt.Errorf("%s, rendered by rulesync, names its home directory, which cannot become the real one: %w", file, tools.UserErr)
	}
	if err := commandHome(tools.Home, tools.goos()); err != nil {
		switch command, known := homeInCommand(file, text, has); {
		case !known:
			return "", false, fmt.Errorf("%s, rendered by rulesync, names its home directory in a file where dotai cannot tell a command from data, and a command cannot name the real one: %w", file, err)
		case command:
			return "", false, fmt.Errorf("%s, rendered by rulesync, names its home directory in a command, which cannot name the real one: %w", file, err)
		}
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if f, ok := formAt(text, i, forms, fold); ok {
			b.WriteString(f.to)
			i += len(f.from)
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String(), true, nil
}

// formAt is the first of forms that s holds at i, followed by the end of
// the path.
func formAt(s string, i int, forms []homeForm, fold bool) (homeForm, bool) {
	for _, f := range forms {
		if len(s)-i >= len(f.from) && sameText(s[i:i+len(f.from)], f.from, fold) && pathEnds(s[i+len(f.from):]) {
			return f, true
		}
	}
	return homeForm{}, false
}

func sameText(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathChars are the characters but letters and digits that continue a
// path component.
const pathChars = "._-~+@#$%&!=,"

// pathEnds reports whether rest, what follows a path, ends it: it is empty
// or starts with a separator, a quote or any other character that a path
// component does not hold.
func pathEnds(rest string) bool {
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(pathChars, r)
}

// namesStaging reports whether s mentions any of roots, the staging tree,
// as it is or percent-encoded, as a file URL holds it.
func namesStaging(s string, roots []string, fold bool) bool {
	named := func(s string) bool {
		return slices.ContainsFunc(roots, func(r string) bool { return stagingIndex(s, r, fold) >= 0 })
	}
	if named(s) {
		return true
	}
	decoded, ok := percentDecoded(s)
	return ok && named(decoded)
}

// percentDecoded is s with each byte percent-encoded as %HH, its hex
// digits in either case, decoded, and whether s held one. A % that no two
// hex digits follow stays.
func percentDecoded(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, false
	}
	var b strings.Builder
	found := false
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if hi, lo := unhex(s[i+1]), unhex(s[i+2]); hi >= 0 && lo >= 0 {
				b.WriteByte(byte(hi<<4 | lo))
				i += 2
				found = true
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String(), found
}

// unhex is the value of the hex digit c, or -1.
func unhex(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// stagingIndex is the index of the first mention in s of the staging tree
// root, an absolute path, with any of the separators /, \, or \\ or \/ as
// a JSON string escapes them, or -1. A root on a network share, //server
// or \\?\UNC\server, is mentioned after any prefix that opens one
// (sharePrefixes); a \\?\ or \\.\ prefix of any other root is not needed.
func stagingIndex(s, root string, fold bool) int {
	share, parts := stagingParts(filepath.ToSlash(root))
	for i := range len(s) {
		if !share {
			if stagingAt(s, i, parts, fold) {
				return i
			}
			continue
		}
		for _, n := range sharePrefixes(s[i:]) {
			if stagingAt(s, i+n, parts, fold) {
				return i
			}
		}
	}
	return -1
}

// stagingParts are the components of root, a slash path, without its
// prefix, and whether that prefix opens a network share: // or //?/UNC/.
// The prefix //?/ or //./ of any other root is dropped, as a path with it
// holds the path without it.
func stagingParts(root string) (share bool, parts []string) {
	for _, p := range []string{"//?/", "//./"} {
		if rest, ok := strings.CutPrefix(root, p); ok {
			if len(rest) >= 4 && strings.EqualFold(rest[:4], "UNC/") {
				return true, strings.Split(rest[4:], "/")
			}
			return false, strings.Split(rest, "/")
		}
	}
	if rest, ok := strings.CutPrefix(root, "//"); ok {
		return true, strings.Split(rest, "/")
	}
	return false, strings.Split(root, "/")
}

// sharePrefixes are the lengths of the prefixes at the start of s that
// open a network share, each one token: two separators, as \\, //, mixed,
// or \\\\ as a JSON string escapes \\, then, for the long form, ? or ., a
// separator, UNC in any case and a separator.
func sharePrefixes(s string) []int {
	var ns []int
	for _, a := range separatorLens(s) {
		for _, b := range separatorLens(s[a:]) {
			n := a + b
			ns = append(ns, n)
			if n >= len(s) || (s[n] != '?' && s[n] != '.') {
				continue
			}
			for _, c := range separatorLens(s[n+1:]) {
				m := n + 1 + c
				if len(s)-m < 3 || !strings.EqualFold(s[m:m+3], "UNC") {
					continue
				}
				for _, d := range separatorLens(s[m+3:]) {
					ns = append(ns, m+3+d)
				}
			}
		}
	}
	return ns
}

// separatorLens are the lengths of each separator s starts with: \\ is a
// JSON-escaped \ or the first of two, so both are tried.
func separatorLens(s string) []int {
	var ns []int
	for _, sep := range []string{`\\`, `\/`, "/", `\`} {
		if strings.HasPrefix(s, sep) {
			ns = append(ns, len(sep))
		}
	}
	return ns
}

func stagingAt(s string, i int, parts []string, fold bool) bool {
	for k, p := range parts {
		if k > 0 {
			n := separatorLen(s[i:])
			if n == 0 {
				return false
			}
			i += n
		}
		if len(s)-i < len(p) || !sameText(s[i:i+len(p)], p, fold) {
			return false
		}
		i += len(p)
	}
	return true
}

func separatorLen(s string) int {
	for _, sep := range []string{`\\`, `\/`, "/", `\`} {
		if strings.HasPrefix(s, sep) {
			return len(sep)
		}
	}
	return 0
}

// homeInCommand reports whether the text of the file file names the home
// directory, as has tells, in a command, and whether dotai can tell: in a
// JSON file, a string under a key that names a hook or a command is one;
// markdown has none; any other file is unknown.
func homeInCommand(file, text string, has func(string) bool) (command, known bool) {
	switch strings.ToLower(path.Ext(file)) {
	case ".md", ".mdc", ".markdown":
		return false, true
	case ".json", ".jsonc":
		v, err := hujson.Parse([]byte(text))
		if err != nil {
			return false, false
		}
		return inCommand(v, false, has), true
	}
	return false, false
}

// inCommand reports whether v holds a string that has names under a key
// that names a hook or a command, which cmd says of v itself.
func inCommand(v hujson.Value, cmd bool, has func(string) bool) bool {
	switch t := v.Value.(type) {
	case *hujson.Object:
		for _, m := range t.Members {
			name, _ := m.Name.Value.(hujson.Literal)
			if cmd && has(name.String()) {
				return true
			}
			key := strings.ToLower(name.String())
			if inCommand(m.Value, cmd || strings.Contains(key, "hook") || strings.Contains(key, "command"), has) {
				return true
			}
		}
	case *hujson.Array:
		for _, e := range t.Elements {
			if inCommand(e, cmd, has) {
				return true
			}
		}
	case hujson.Literal:
		return cmd && t.Kind() == '"' && has(t.String())
	}
	return false
}

func isJSON(file string) bool {
	ext := strings.ToLower(path.Ext(file))
	return ext == ".json" || ext == ".jsonc"
}

// jsonStringText is s as a JSON string holds it, without the quotes.
func jsonStringText(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(s); err != nil {
		panic(err) // a string always encodes
	}
	out := strings.TrimSuffix(b.String(), "\n")
	return out[1 : len(out)-1]
}
