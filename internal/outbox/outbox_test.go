package outbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const phoneID = "0123456789abcdef0123456789abcdef"

func TestQueuePollGetAck(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "from-pc.txt")
	if err := os.WriteFile(path, []byte("rocket"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Queue(phoneID, []string{path}); err == nil {
		t.Fatal("accepted offline phone")
	}
	if _, err := r.Touch(phoneID, "Phone One"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Queue(phoneID, []string{path}); err != nil {
		t.Fatal(err)
	}
	listed, err := r.Touch(phoneID, "Phone One")
	if err != nil || len(listed) != 1 || listed[0].Name != "from-pc.txt" {
		t.Fatalf("offers = %v; %v", listed, err)
	}
	f, info, err := r.Open(phoneID, listed[0].ID)
	if err != nil || info.Size() != 6 {
		t.Fatalf("open = %v %v", info, err)
	}
	f.Close()
	if _, _, err := r.Open("ffffffffffffffffffffffffffffffff", listed[0].ID); err == nil {
		t.Fatal("other phone obtained the offer")
	}
	if err := r.Done(phoneID, listed[0].ID); err != nil {
		t.Fatal(err)
	}
	listed, _ = r.Touch(phoneID, "Phone One")
	if len(listed) != 0 {
		t.Fatal("offer still queued after ack")
	}
}

func TestDropForgetsWaitingFiles(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "drop.txt")
	os.WriteFile(path, []byte("x"), 0600)
	r.Touch(phoneID, "Phone")
	if _, err := r.Queue(phoneID, []string{path}); err != nil {
		t.Fatal(err)
	}
	r.Drop(phoneID)
	if listed, _ := r.Touch(phoneID, "Phone"); len(listed) != 0 {
		t.Fatalf("dropped offers still listed: %v", listed)
	}
}

func TestChangedSourceAndInvalidIdentity(t *testing.T) {
	r := New()
	if _, err := r.Touch("not-an-id", "Phone"); err == nil {
		t.Fatal("bad device ID accepted")
	}
	if _, err := r.Touch(phoneID, "Phone"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.bin")
	os.WriteFile(path, []byte("old"), 0600)
	r.Queue(phoneID, []string{path})
	list, _ := r.Touch(phoneID, "Phone")
	os.WriteFile(path, []byte("a longer changed version"), 0600)
	if _, _, err := r.Open(phoneID, list[0].ID); err == nil {
		t.Fatal("modified source was served")
	}
	r.mu.Lock()
	r.phones[phoneID] = Phone{ID: phoneID, LastSeen: time.Now().Add(-onlineFor - time.Second)}
	r.mu.Unlock()
	if len(r.Phones()) != 0 {
		t.Fatal("stale phone shown as online")
	}
}
