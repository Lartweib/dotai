package app

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// / volumeNameDOS asks GetFinalPathNameByHandle for a path with a drive
// letter, normalized (FILE_NAME_NORMALIZED and VOLUME_NAME_DOS are 0).
const volumeNameDOS = 0

// resolvePath is the path p is on disk, with every link and junction on
// its way followed and each 8.3 short name, such as PROGRA~1, in its long
// form, as Windows gives the final path of an open handle; or p as it is
// when it cannot be opened. filepath.EvalSymlinks does not follow
// junctions.
func resolvePath(p string) string {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return p
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS)
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	final := windows.UTF16ToString(buf[:n])
	switch {
	case strings.HasPrefix(final, `\?\UNC\`):
		final = `\` + final[len(`\?\UNC\`):]
	default:
		final = strings.TrimPrefix(final, `\?\`)
	}
	return filepath.Clean(final)
}
