package moodle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrDestNotAllowed is returned for a download destination outside the
// places CheckDest allows.
var ErrDestNotAllowed = errors.New("moodle: download destination not allowed")

// userFolders are the folders of the home directory a download may go into
// (compared case-insensitively). Everything else in the home directory may
// hold settings or autostart entries.
var userFolders = []string{"Desktop", "Documents", "Downloads"}

// scriptFolders hold shell profiles that run on start-up even though they
// sit inside Documents (PowerShell 5 and 7).
var scriptFolders = []string{"WindowsPowerShell", "PowerShell"}

// CheckDest allows a download destination only inside downloadDir, or inside
// Desktop, Documents or Downloads of the home directory — never in a hidden
// file or folder, a PowerShell profile folder, or behind a Windows junction.
// Moodle content reaches the assistant as text, so a prompt hidden in a
// course page must not be able to make the server drop a file into a
// configuration or autostart location. Symbolic links are resolved first.
func CheckDest(dest, downloadDir string) error {
	deny := fmt.Errorf("%w: %s", ErrDestNotAllowed, dest)
	abs, err := absPath(dest)
	if err != nil {
		return err
	}
	if hasJunction(abs) {
		return deny
	}
	target := resolve(abs)
	if downloadDir != "" {
		if root, err := absPath(downloadDir); err == nil && !hasJunction(root) && within(resolve(root), target) && clean(resolve(root), target) {
			return nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return deny
	}
	for _, f := range userFolders {
		root := resolve(filepath.Join(home, f))
		if !within(root, target) {
			continue
		}
		if clean(root, target) {
			return nil
		}
	}
	return deny
}

// clean reports whether the part of p below root has no hidden component and
// no PowerShell profile folder.
func clean(root, p string) bool {
	rel, err := filepath.Rel(strings.ToLower(root), strings.ToLower(p))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	for _, c := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(c, ".") || equalAny(c, scriptFolders) {
			return false
		}
	}
	return true
}

func equalAny(s string, list []string) bool {
	for _, x := range list {
		if strings.EqualFold(s, x) {
			return true
		}
	}
	return false
}

// within reports whether p is root or below it, ignoring case: macOS and
// Windows file systems usually do.
func within(root, p string) bool {
	rel, err := filepath.Rel(strings.ToLower(root), strings.ToLower(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// absPath expands "~" and makes p absolute and clean.
func absPath(p string) (string, error) {
	p, err := expandHome(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// resolve resolves symbolic links in the longest existing prefix of p; the
// part that does not exist yet is appended as is.
func resolve(p string) string {
	var rest []string
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// hasJunction reports whether an existing component of p is a Windows
// junction or another link EvalSymlinks leaves alone (since Go 1.23 it does
// not follow junctions; they report os.ModeIrregular).
func hasJunction(p string) bool {
	for cur := p; ; {
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeIrregular != 0 {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}
