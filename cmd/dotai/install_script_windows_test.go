package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// lockFile opens path without sharing it, as a running program holds its
// executable, so that it cannot be renamed or removed until unlock.
func lockFile(t *testing.T, path string) (unlock func()) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	var once bool
	unlock = func() {
		if !once {
			once = true
			_ = windows.CloseHandle(h)
		}
	}
	t.Cleanup(unlock)
	return unlock
}

// testKeyRoot holds the test-only stand-ins for HKCU\Environment that
// install.ps1 writes through DOTAI_TEST_USER_ENV_KEY.
const testKeyRoot = `Software\dotai-install-test`

// testUserEnvKey returns a new HKCU subkey for one run, removed when the
// test ends.
func testUserEnvKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	key := testKeyRoot + `\` + hex.EncodeToString(b)
	t.Cleanup(func() {
		if err := registry.DeleteKey(registry.CURRENT_USER, key); err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("remove the test key %s: %v", key, err)
		}
		// Fails, harmlessly, while another test still has its key.
		_ = registry.DeleteKey(registry.CURRENT_USER, testKeyRoot)
	})
	return key
}

// setTestUserPath writes Path in the test key, as REG_EXPAND_SZ or REG_SZ.
func setTestUserPath(t *testing.T, key, value string, expand bool) {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if expand {
		err = k.SetExpandStringValue("Path", value)
	} else {
		err = k.SetStringValue("Path", value)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// testUserPathValue reads Path from the test key, unexpanded, with whether
// it is REG_EXPAND_SZ and whether it exists.
func testUserPathValue(t *testing.T, key string) (value string, expand, ok bool) {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	value, kind, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return value, kind == registry.EXPAND_SZ, true
}
