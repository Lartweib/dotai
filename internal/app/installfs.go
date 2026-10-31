package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/plan"
)

// The write side of osInstallFS. Every write into the home directory goes
// through an os.Root opened at the directory it belongs to (a harness's
// isolated directory, the tools directory, the dotai directory or a
// backup), which refuses any name or link that leads outside it, so a
// directory swapped for a link or a junction after the checks cannot
// redirect a write. What is left is the moment between checking the
// directories from the home directory down to that root and opening it.

func (osInstallFS) Read(ctx context.Context, name string) ([]byte, plan.Current, error) {
	f, info, err := openRegular(name)
	if err != nil {
		return nil, plan.Current{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(ctxReader{ctx, f})
	if err != nil {
		return nil, plan.Current{}, fmt.Errorf("%s: %w", name, err)
	}
	h := sha256.Sum256(data)
	return data, plan.Current{Hash: hex.EncodeToString(h[:]), Exec: info.Mode()&0o111 != 0}, nil
}

// Link reads the link at name without following it: a symbolic link, or a
// junction on Windows, which Lstat reports as an irregular file.
func (osInstallFS) Link(name string) (plan.Current, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return plan.Current{}, err
	}
	notLink := &NotRegularError{Path: name, Mode: info.Mode(), Link: true}
	if !linkMode(info.Mode()) {
		return plan.Current{}, notLink
	}
	dest, err := os.Readlink(name)
	if err != nil {
		return plan.Current{}, notLink // another reparse point
	}
	return plan.LinkCurrent(dest), nil
}

// LinkIn creates the link name, a slash path inside root, to the absolute
// dest: a junction on Windows, which needs no administrator rights, and a
// symbolic link elsewhere (decision 0036). name must be missing.
func (osInstallFS) LinkIn(root, name, dest string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return makeLink(r, name, dest)
}

// WriteIn writes a temporary file next to name, syncs it and moves it over
// name, so that a crash leaves the old or the new file, and leaves no
// temporary file behind when it fails. A file already at name keeps the
// stricter of its permissions and mode, and loses a read-only attribute
// that would stop the move.
func (o osInstallFS) WriteIn(root, name string, data []byte, mode fs.FileMode) (err error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if !o.ExecBit() {
		mode &^= 0o111
	}
	if info, err := r.Lstat(name); err == nil && info.Mode().IsRegular() {
		mode = narrowMode(mode, info.Mode().Perm())
		if err := clearReadOnly(r, name, info.Mode()); err != nil {
			return err
		}
	}
	tmp, err := tempName(name)
	if err != nil {
		return err
	}
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = r.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := r.Chmod(tmp, mode); err != nil {
		return err
	}
	return retry(func() error { return r.Rename(tmp, name) })
}

// RemoveIn removes a file or an empty directory, clearing a read-only
// attribute that would stop it.
func (osInstallFS) RemoveIn(root, name string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if info, err := r.Lstat(name); err == nil && info.Mode().IsRegular() {
		if err := clearReadOnly(r, name, info.Mode()); err != nil {
			return err
		}
	}
	return retry(func() error { return r.Remove(name) })
}

func (osInstallFS) MkdirIn(root, name string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.MkdirAll(name, 0o755)
}

// WriteSynced writes a new file and syncs it before it returns.
func (osInstallFS) WriteSynced(dir, name string, data []byte, perm fs.FileMode) (err error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.MkdirAll(path.Dir(name), 0o700); err != nil {
		return err
	}
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := f.Close(); err == nil {
			err = cErr
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func (osInstallFS) Remove(name string) error                     { return os.Remove(name) }
func (osInstallFS) Mkdir(name string, perm fs.FileMode) error    { return os.Mkdir(name, perm) }
func (osInstallFS) MkdirAll(name string, perm fs.FileMode) error { return os.MkdirAll(name, perm) }
func (osInstallFS) Now() time.Time                               { return time.Now() }
func (osInstallFS) SyncDir(name string) error                    { return syncDir(name) }

// tempName is a new name next to the slash path name.
func tempName(name string) (string, error) {
	b := make([]byte, tempBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return path.Join(path.Dir(name), "."+path.Base(name)+tempInfix+hex.EncodeToString(b)), nil
}

const (
	tempInfix = ".dotai-"
	tempBytes = 8
)

// isTempOf reports whether entry is a name tempName gives for a file
// named base.
func isTempOf(entry, base string) bool {
	rest, ok := strings.CutPrefix(entry, "."+base+tempInfix)
	if !ok || len(rest) != 2*tempBytes {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}

// narrowMode is mode with no read or write permission that cur lacks, so
// that a file the user made private stays private; the exec bits follow
// mode, for each class that may read.
func narrowMode(mode, cur fs.FileMode) fs.FileMode {
	m := mode & cur & 0o666
	if mode&0o111 != 0 {
		m |= (m & 0o444) >> 2
	}
	return m
}

// retry runs f until it works, it fails in a way that is not transient on
// this OS, or it has run five times, waiting longer after each try.
func retry(f func() error) error {
	return retryWith(f, transient, 5, 10*time.Millisecond, time.Sleep)
}

func retryWith(f func() error, transient func(error) bool, attempts int, wait time.Duration, sleep func(time.Duration)) error {
	for i := 1; ; i++ {
		err := f()
		if err == nil || i >= attempts || !transient(err) {
			return err
		}
		sleep(wait)
		wait *= 2
	}
}
