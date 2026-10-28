package pack

import (
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// maxFileSize is the largest file the engine reads from a pack.
const maxFileSize = 1 << 20

// lstat describes name without following symbolic links. link is the first
// path element, from the root, that is a symbolic link; when it is set,
// info describes that link and nothing behind it is touched.
func lstat(fsys fs.FS, name string) (info fs.FileInfo, link string, err error) {
	for i := 0; i <= len(name); i++ {
		if i < len(name) && name[i] != '/' {
			continue
		}
		if info, err = fs.Lstat(fsys, name[:i]); err != nil {
			return nil, "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return info, name[:i], nil
		}
	}
	return info, "", nil
}

// entryProblem returns the diagnostic code and message when the entry at
// name cannot be used, or "" when it can.
func entryProblem(name, link string, mode fs.FileMode, wantDir bool) (code, msg string) {
	switch {
	case link != "" || mode&fs.ModeSymlink != 0:
		if link == "" {
			link = name
		}
		return CodeSymlinkNotAllowed, fmt.Sprintf("%q is a symbolic link; packs must not contain links", link)
	case wantDir && !mode.IsDir():
		return CodeLayerNotDirectory, fmt.Sprintf("%q is not a directory", name)
	case !wantDir && !mode.IsRegular():
		return CodeNotRegularFile, fmt.Sprintf("%q is not a regular file", name)
	}
	return "", ""
}

// readRegular reads name when it is a regular file reached without links.
// A problem with the pack is a diagnostic; a failure to read is an error,
// and so is a directory where a file is expected.
func readRegular(fsys fs.FS, name string) ([]byte, *Diagnostic, error) {
	info, link, err := lstat(fsys, name)
	if err != nil {
		return nil, nil, err
	}
	if link == "" && info.IsDir() {
		return nil, nil, fmt.Errorf("%s is a directory", name)
	}
	if code, msg := entryProblem(name, link, info.Mode(), false); code != "" {
		d := NewDiagnostic(name, 0, 0, code, "%s", msg)
		return nil, &d, nil
	}
	return readLimited(fsys, name)
}

// readLimited reads at most maxFileSize bytes of name.
func readLimited(fsys fs.FS, name string) ([]byte, *Diagnostic, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxFileSize {
		d := NewDiagnostic(name, 0, 0, CodeFileTooLarge, "%q is larger than 1 MiB", name)
		return nil, &d, nil
	}
	return data, nil, nil
}

// normalize drops a UTF-8 byte order mark and turns CRLF into LF, so that
// line and column numbers do not depend on how the file was saved.
func normalize(src []byte) string {
	return strings.ReplaceAll(strings.TrimPrefix(string(src), "\uFEFF"), "\r\n", "\n")
}
