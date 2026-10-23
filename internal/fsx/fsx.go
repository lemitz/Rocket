package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const stagingDir = ".rocket-staging"

func SafeName(input string) (string, error) {
	if input == "" || !utf8.ValidString(input) {
		return "", errors.New("empty or invalid filename")
	}
	var b strings.Builder
	for _, r := range input {
		if r < 32 || r == 127 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	name := strings.TrimRight(b.String(), " .")
	if name == "" || name == "." || name == ".." || len(name) > 200 {
		return "", errors.New("invalid or overlong filename")
	}
	stem := strings.TrimRight(strings.SplitN(name, ".", 2)[0], " ")
	upper := strings.ToUpper(stem)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || upper == "CONIN$" || upper == "CONOUT$" || reservedPortName(upper) {
		name = "_" + name
	}
	return name, nil
}

func reservedPortName(name string) bool {
	runes := []rune(name)
	if len(runes) != 4 || (string(runes[:3]) != "COM" && string(runes[:3]) != "LPT") {
		return false
	}
	r := runes[3]
	return r >= '0' && r <= '9' || r == '¹' || r == '²' || r == '³'
}

func CreatePart(dir string) (*os.File, error) {
	staging := filepath.Join(dir, stagingDir)
	if err := os.MkdirAll(staging, 0700); err != nil {
		return nil, err
	}
	return os.CreateTemp(staging, "rocket-*.part")
}

// Publish never overwrites an existing destination. The part must be on the same volume.
// Publication is atomic as a directory-entry change, not a power-loss durability guarantee.
func Publish(part, dir, name string) (string, error) {
	clean, err := SafeName(name)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(clean)
	stem := strings.TrimSuffix(clean, ext)
	for i := 0; i < 10000; i++ {
		candidate := clean
		if i != 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		err := moveNoReplace(part, filepath.Join(dir, candidate))
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("too many conflicting filenames")
}

// CleanOldParts only removes Rocket-owned regular files older than the grace period.
// It must not disrupt another active process using the same destination folder.
func CleanOldParts(dir string, olderThan time.Duration) error {
	entries, err := os.ReadDir(filepath.Join(dir, stagingDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		if e.Type() != 0 || !strings.HasPrefix(e.Name(), "rocket-") || !strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, stagingDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
