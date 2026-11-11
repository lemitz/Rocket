package outbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const otherPhoneID = "ffffffffffffffffffffffffffffffff"

func TestQueueTextValidation(t *testing.T) {
	r := New()
	if _, err := r.QueueText(phoneID, "hello"); err == nil {
		t.Fatal("queued text for an offline phone")
	}
	if _, err := r.Touch(phoneID, "Phone"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ device, text string }{
		{phoneID, ""},
		{phoneID, "a\x00b"},
		{phoneID, string([]byte{0xff})},
		{phoneID, strings.Repeat("é", MaxTextBytes/2+1)},
		{"bad-id", "hello"},
	} {
		if _, err := r.QueueText(tc.device, tc.text); err == nil || strings.Contains(err.Error(), tc.text) && tc.text != "" {
			t.Fatalf("invalid message accepted or leaked in error: %q, %v", tc.device, err)
		}
	}
	if id, err := r.QueueText(phoneID, strings.Repeat("é", MaxTextBytes/2)); err != nil || !ValidID(id) {
		t.Fatalf("valid 8192-byte message: id %q, %v", id, err)
	}
	if len(r.Texts(phoneID)) != 1 {
		t.Fatal("invalid message entered queue")
	}
}

func TestTextsDestinationConfirmationExpiryAndFiles(t *testing.T) {
	r := New()
	r.Touch(phoneID, "Phone")
	r.Touch(otherPhoneID, "Other")
	path := filepath.Join(t.TempDir(), "offer.txt")
	if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Queue(phoneID, []string{path}); err != nil {
		t.Fatal(err)
	}
	id, err := r.QueueText(phoneID, "private message")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Texts(otherPhoneID)) != 0 || len(r.Texts("invalid")) != 0 {
		t.Fatal("text leaked to another destination")
	}
	if n, err := r.DoneText(otherPhoneID, id); !errors.Is(err, ErrNoText) || n != 0 {
		t.Fatalf("other phone acknowledged text: %d %v", n, err)
	}
	if n, err := r.DoneText(phoneID, "invalid"); !errors.Is(err, ErrNoText) || n != 0 {
		t.Fatalf("invalid ID acknowledged text: %d %v", n, err)
	}
	if got := r.Texts(phoneID); len(got) != 1 || got[0].ID != id || got[0].Text != "private message" {
		t.Fatalf("queued text: %+v", got)
	}
	if n, err := r.DoneText(phoneID, id); err != nil || n != len("private message") {
		t.Fatalf("confirmation: %d, %v", n, err)
	}
	if _, err := r.DoneText(phoneID, id); !errors.Is(err, ErrNoText) {
		t.Fatalf("duplicate confirmation accepted: %v", err)
	}
	if got := r.Texts(phoneID); len(got) != 0 {
		t.Fatalf("confirmed text still queued: %+v", got)
	}
	if got, err := r.Touch(phoneID, "Phone"); err != nil || len(got) != 1 {
		t.Fatalf("text confirmation disturbed file offers: %+v %v", got, err)
	}
	id, err = r.QueueText(phoneID, "expired")
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.texts[phoneID][0].created = time.Now().Add(-offerFor - time.Second)
	r.mu.Unlock()
	if got := r.Texts(phoneID); len(got) != 0 {
		t.Fatalf("expired message offered: %+v", got)
	}
	if _, err := r.DoneText(phoneID, id); !errors.Is(err, ErrNoText) {
		t.Fatalf("expired message confirmed: %v", err)
	}
	if got, _ := r.Touch(phoneID, "Phone"); len(got) != 1 {
		t.Fatal("text expiry removed file offer")
	}
}

func TestTextsBoundedAndDrop(t *testing.T) {
	r := New()
	r.Touch(phoneID, "Phone")
	for i := 0; i < maxTexts; i++ {
		if _, err := r.QueueText(phoneID, "hello"); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.Texts(phoneID); len(got) != maxPolledTexts {
		t.Fatalf("poll returned %d texts", len(got))
	}
	if _, err := r.QueueText(phoneID, "one too many"); err == nil {
		t.Fatal("accepted over-capacity text")
	}
	r.Drop(phoneID)
	if got := r.Texts(phoneID); len(got) != 0 {
		t.Fatalf("dropped texts still present: %+v", got)
	}
}
