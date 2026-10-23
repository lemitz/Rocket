package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Settings contains user overrides. An empty download_dir means the OS Downloads folder.
type Settings struct {
	DownloadDir string `json:"download_dir,omitempty"`
	// NodeID is a public, stable device label for discovery; it is NOT a secret or authentication.
	NodeID string `json:"node_id,omitempty"`
}

type Store struct{ Path string }

func DefaultStore() (Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Path: filepath.Join(base, "Rocket", "config.json")}, nil
}

func (s Store) Load() (Settings, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("invalid config: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Settings{}, errors.New("invalid config: trailing data")
	}
	return settings, nil
}

// EnsureNodeID creates a stable public discovery label on first daemon launch.
func (s Store) EnsureNodeID(settings Settings) (Settings, error) {
	if settings.NodeID != "" {
		if len(settings.NodeID) != 32 {
			return settings, errors.New("invalid config: node_id must be 32 lowercase hex characters")
		}
		decoded, err := hex.DecodeString(settings.NodeID)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != settings.NodeID {
			return settings, errors.New("invalid config: node_id must be 32 lowercase hex characters")
		}
		return settings, nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return settings, err
	}
	settings.NodeID = hex.EncodeToString(random[:])
	if err := s.Save(settings); err != nil {
		return settings, err
	}
	return settings, nil
}

func (s Store) Save(settings Settings) error {
	if settings.DownloadDir != "" {
		path, err := ValidateDirectory(settings.DownloadDir)
		if err != nil {
			return err
		}
		settings.DownloadDir = path
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path), ".rocket-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	data, err := json.MarshalIndent(settings, "", "  ")
	if err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceConfig(f.Name(), s.Path)
}

func (s Settings) ResolvedDownloadDir() (string, error) {
	if s.DownloadDir != "" {
		return ValidateDirectory(s.DownloadDir)
	}
	path, err := knownDownloads()
	if err != nil {
		return "", err
	}
	return ValidateDirectory(path)
}

func ValidateDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("directory must be an absolute path: %q", path)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %q", clean)
	}
	return clean, nil
}
