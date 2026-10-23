package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreAndOverride(t *testing.T) {
	root := t.TempDir()
	store := Store{Path: filepath.Join(root, "cfg", "config.json")}
	s, err := store.Load()
	if err != nil || s.DownloadDir != "" {
		t.Fatalf("empty config: %+v %v", s, err)
	}
	folder := filepath.Join(root, "Downloads")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Settings{DownloadDir: folder}); err != nil {
		t.Fatal(err)
	}
	s, err = store.Load()
	if err != nil || s.DownloadDir != folder {
		t.Fatalf("saved config: %+v %v", s, err)
	}
	got, err := s.ResolvedDownloadDir()
	if err != nil || got != folder {
		t.Fatalf("resolved: %q %v", got, err)
	}
	if err := store.Save(Settings{}); err != nil {
		t.Fatal(err)
	}
	s, err = store.Load()
	if err != nil || s.DownloadDir != "" {
		t.Fatalf("reset: %+v %v", s, err)
	}
}

func TestNodeIdentityPersistsThroughFolderChanges(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.json")}
	settings, err := store.EnsureNodeID(Settings{})
	if err != nil || len(settings.NodeID) != 32 {
		t.Fatalf("node id: %+v %v", settings, err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.NodeID != settings.NodeID {
		t.Fatalf("node id was not saved: %+v %v", loaded, err)
	}
	loaded.DownloadDir = t.TempDir()
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	loaded.DownloadDir = ""
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil || loaded.NodeID != settings.NodeID {
		t.Fatalf("node id changed with directory: %+v %v", loaded, err)
	}
	if _, err := store.EnsureNodeID(loaded); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidOverrideDoesNotReplaceExistingConfig(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := store.Save(Settings{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Settings{DownloadDir: "relative/path"}); err == nil {
		t.Fatal("accepted relative path")
	}
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path, []byte(`{"download_dir":"x"} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
