package app

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// transient reports an error that another program holding the file, such
// as an antivirus scan or a search indexer, causes for a moment.
func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// crossDevice reports a rename that failed because it crosses volumes.
func crossDevice(err error) bool { return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE) }

// copyLink makes a copy of a link inside r at to, pointing at dest: a
// symbolic link for one, which Windows may refuse without the right to
// make them, and a junction for a junction.
func copyLink(r *os.Root, mode fs.FileMode, dest, to string) error {
	if mode&fs.ModeSymlink != 0 {
		return r.Symlink(dest, to)
	}
	return makeLink(r, to, dest)
}

// syncIn does nothing, as syncDir.
func syncIn(*os.Root, string) error { return nil }

// clearReadOnly clears the read-only attribute of name, which would stop a
// move over it or its removal.
func clearReadOnly(r *os.Root, name string, mode fs.FileMode) error {
	if mode&0o200 != 0 {
		return nil
	}
	return r.Chmod(name, mode|0o200)
}

// syncDir does nothing: Windows cannot sync a directory, and makes the
// entries of a directory durable with the files themselves.
func syncDir(string) error { return nil }

// linkMode reports a mode that Link reads as a link: a symbolic link, or a
// junction or another reparse point, which Lstat reports as irregular and
// Readlink tells apart.
func linkMode(mode fs.FileMode) bool { return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0 }

// makeLink creates the junction name inside r to dest, which needs no
// administrator rights, unlike a symbolic link. The directory is created
// through a handle of its parent opened inside r, with the reparse point
// set on its own handle, so that nothing outside r is created even if a
// directory on the way is swapped for a link; a junction left half made is
// removed.
func makeLink(r *os.Root, name, dest string) (err error) {
	if err := canLink(dest); err != nil {
		return err
	}
	parent, err := r.Open(path.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	base, err := windows.NewNTUnicodeString(path.Base(name))
	if err != nil {
		return err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: base, Attributes: objDontReparse}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.FILE_WRITE_DATA|windows.FILE_WRITE_ATTRIBUTES|windows.SYNCHRONIZE, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: err}
	}
	buf := mountPoint(dest)
	var n uint32
	err = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &n, nil)
	if cErr := windows.CloseHandle(h); err == nil {
		err = cErr
	}
	if err != nil {
		return errors.Join(&fs.PathError{Op: "junction", Path: name, Err: err}, r.Remove(name))
	}
	return nil
}

// canLink refuses a destination a junction cannot point at: one that is
// not an absolute path on a local drive, such as a network path.
func canLink(dest string) error {
	// A drive letter and a colon; a network path has a longer volume.
	if !filepath.IsAbs(dest) || len(filepath.VolumeName(dest)) != 2 {
		return fmt.Errorf("a junction needs an absolute path on a local drive, and %s is not one: put the home directory on a local drive, or use a variant that does not share skills", dest)
	}
	return nil
}

// objDontReparse is OBJ_DONT_REPARSE: the name is not resolved through a
// reparse point.
const objDontReparse = 0x1000

// mountPoint is the reparse data of a junction to the absolute path dest:
// the substitute name \??\<dest> and the print name <dest>, each followed
// by a NUL, after the header of a mount point.
func mountPoint(dest string) []byte {
	sub, printed := utf16.Encode([]rune(`\??\`+dest)), utf16.Encode([]rune(dest))
	subLen, printLen := 2*len(sub), 2*len(printed)
	names := make([]byte, subLen+2+printLen+2)
	for i, c := range sub {
		binary.LittleEndian.PutUint16(names[2*i:], c)
	}
	for i, c := range printed {
		binary.LittleEndian.PutUint16(names[subLen+2+2*i:], c)
	}
	buf := make([]byte, 16+len(names))
	binary.LittleEndian.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(8+len(names))) // the data after the 8-byte header
	binary.LittleEndian.PutUint16(buf[8:], 0)                    // the substitute name's offset
	binary.LittleEndian.PutUint16(buf[10:], uint16(subLen))
	binary.LittleEndian.PutUint16(buf[12:], uint16(subLen+2)) // the print name's offset
	binary.LittleEndian.PutUint16(buf[14:], uint16(printLen))
	copy(buf[16:], names)
	return buf
}
