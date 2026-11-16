package moodle

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckDest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	dl := filepath.Join(t.TempDir(), "moodle")
	outside := t.TempDir()

	tests := []struct {
		dest string
		ok   bool
	}{
		{dl + "/", true},
		{filepath.Join(dl, "sub", "a.pdf"), true},
		{"~/Documents/pdp/", true},
		{"~/documents/pdp26/task.pdf", true}, // case-insensitive file systems
		{"~/Desktop/a.pdf", true},
		{"~/Downloads", true},
		{"~/Downloads/moodle/", true},
		{"~", false},
		{"~/Projects/x.pdf", false},
		{"~/.ssh/", false},
		{"~/.zshenv", false},
		{"~/Documents/.git/hooks/", false},
		{"~/Documents/WindowsPowerShell/Microsoft.PowerShell_profile.ps1", false},
		{"~/Documents/PowerShell/profile.ps1", false},
		{"~/Library/LaunchAgents/x.plist", false},
		{"~/AppData/Roaming/x", false},
		{"~/Start Menu/Programs/Startup/x.cmd", false},
		{"~/Application Data/Microsoft/x", false},
		{"~/Documents/../.config/x", false},
		{outside + "/", false},
		{filepath.Join(outside, "a.pdf"), false},
	}
	for _, tt := range tests {
		err := CheckDest(tt.dest, dl)
		if (err == nil) != tt.ok {
			t.Errorf("CheckDest(%q) = %v, want ok=%v", tt.dest, err, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrDestNotAllowed) {
			t.Errorf("CheckDest(%q): %v is not ErrDestNotAllowed", tt.dest, err)
		}
	}
}

// A visible folder that is a symbolic link into a hidden one is refused.
func TestCheckDest_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "Documents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "Documents", "keys")); err != nil {
		t.Fatal(err)
	}
	if err := CheckDest("~/Documents/keys/authorized_keys", ""); !errors.Is(err, ErrDestNotAllowed) {
		t.Errorf("symlink into ~/.ssh: %v", err)
	}
}
