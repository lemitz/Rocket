package fsx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"CON.txt": "_CON.txt", "lpt1": "_lpt1", "COM¹.txt": "_COM¹.txt", "pic:.jpg ": "pic_.jpg",
		"../evil": ".._evil", "a\\b": "a_b", "video.mp4": "video.mp4",
	}
	for input, want := range cases {
		got, err := SafeName(input)
		if err != nil || got != want {
			t.Errorf("SafeName(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "...", " ", string([]byte{0xff})} {
		if _, err := SafeName(input); err == nil {
			t.Errorf("accepted invalid name %q", input)
		}
	}
}

func TestPublishDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	part, err := CreatePart(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := part.Close(); err != nil {
		t.Fatal(err)
	}
	saved, err := Publish(part.Name(), dir, "file.txt")
	if err != nil || saved != "file (1).txt" {
		t.Fatalf("publish: %q %v", saved, err)
	}
	original, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil || string(original) != "keep" {
		t.Fatalf("original replaced: %q %v", original, err)
	}
}

func TestConcurrentPublishNeverReplacesAnotherFile(t *testing.T) {
	dir := t.TempDir()
	const count = 12
	var wg sync.WaitGroup
	var errs [count]error
	var names [count]string
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			part, err := CreatePart(dir)
			if err != nil {
				errs[i] = err
				return
			}
			defer os.Remove(part.Name())
			if _, err = fmt.Fprint(part, i); err == nil {
				err = part.Close()
			}
			if err == nil {
				names[i], err = Publish(part.Name(), dir, "collision.txt")
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool)
	for i := range errs {
		if errs[i] != nil || names[i] == "" || seen[names[i]] {
			t.Fatalf("publish #%d: name %q err %v", i, names[i], errs[i])
		}
		seen[names[i]] = true
		data, err := os.ReadFile(filepath.Join(dir, names[i]))
		if err != nil || string(data) != fmt.Sprint(i) {
			t.Fatalf("publish #%d corrupted %q: %q, %v", i, names[i], data, err)
		}
	}
}

func TestCleanupOnlyOldRocketParts(t *testing.T) {
	dir := t.TempDir()
	part, err := CreatePart(dir)
	if err != nil {
		t.Fatal(err)
	}
	part.Close()
	other := filepath.Join(filepath.Dir(part.Name()), "user.part")
	if err := os.WriteFile(other, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(part.Name(), old, old); err != nil {
		t.Fatal(err)
	}
	if err := CleanOldParts(dir, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(part.Name()); !os.IsNotExist(err) {
		t.Fatalf("stale part still exists: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated file deleted: %v", err)
	}
}
