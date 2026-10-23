package outbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rocket/internal/fsx"
)

const onlineFor = 12 * time.Second
const offerFor = 30 * time.Minute
const maxOffers = 256
const maxFileSize = 1 << 40

var ErrNoOffer = errors.New("file is no longer available")

type Phone struct {
	ID       string
	Name     string
	LastSeen time.Time
}

type Offer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

type item struct {
	Offer
	path    string
	created time.Time
}

// Registry is a transient per-PC outbox. Paths remain on the original disk;
// only phones with Rocket open report themselves online. IDs are labels, not auth.
type Registry struct {
	mu     sync.Mutex
	phones map[string]Phone
	items  map[string][]item
}

func New() *Registry {
	return &Registry{phones: make(map[string]Phone), items: make(map[string][]item)}
}

func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}

func (r *Registry) Touch(id, name string) ([]Offer, error) {
	if !ValidID(id) || name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, errors.New("invalid phone identity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phones[id] = Phone{ID: id, Name: name, LastSeen: time.Now()}
	r.cleanupLocked()
	result := make([]Offer, 0, min(len(r.items[id]), 32))
	for _, it := range r.items[id] {
		if len(result) == 32 {
			break
		}
		result = append(result, it.Offer)
	}
	return result, nil
}

func (r *Registry) Phones() []Phone {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	result := make([]Phone, 0, len(r.phones))
	for _, phone := range r.phones {
		if now.Sub(phone.LastSeen) < onlineFor {
			result = append(result, phone)
		}
	}
	return result
}

func (r *Registry) Queue(device string, paths []string) (string, error) {
	if !ValidID(device) || len(paths) == 0 || len(paths) > 256 {
		return "", errors.New("select 1–256 files and an available phone")
	}
	files := make([]item, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("not an absolute path: %q", path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxFileSize {
			return "", fmt.Errorf("not a supported regular file: %q", path)
		}
		name, err := fsx.SafeName(info.Name())
		if err != nil || name != info.Name() {
			return "", fmt.Errorf("unsupported filename: %q", info.Name())
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return "", err
		}
		files = append(files, item{Offer: Offer{ID: hex.EncodeToString(token[:]), Name: name, Size: info.Size(), Mtime: info.ModTime().Unix()}, path: path, created: time.Now()})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupLocked()
	phone, ok := r.phones[device]
	if !ok || time.Since(phone.LastSeen) >= onlineFor {
		return "", errors.New("phone is offline; open Rocket on the destination phone")
	}
	if len(r.items[device])+len(files) > maxOffers {
		return "", errors.New("too many files already waiting for this phone")
	}
	r.items[device] = append(r.items[device], files...)
	return "", nil // The desktop localizes the queued status for the OS UI language.
}

func (r *Registry) Open(device, id string) (*os.File, os.FileInfo, error) {
	if !ValidID(device) || !ValidID(id) {
		return nil, nil, ErrNoOffer
	}
	r.mu.Lock()
	var found *item
	for _, it := range r.items[device] {
		if it.ID == id && time.Since(it.created) < offerFor {
			copy := it
			found = &copy
			break
		}
	}
	r.mu.Unlock()
	if found == nil {
		return nil, nil, ErrNoOffer
	}
	f, err := os.Open(found.path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != found.Size || info.ModTime().Unix() != found.Mtime {
		f.Close()
		return nil, nil, ErrNoOffer
	}
	return f, info, nil
}

func (r *Registry) Done(device, id string) error {
	if !ValidID(device) || !ValidID(id) {
		return ErrNoOffer
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, it := range r.items[device] {
		if it.ID == id {
			list := r.items[device]
			r.items[device] = append(list[:i], list[i+1:]...)
			return nil
		}
	}
	return ErrNoOffer
}

func (r *Registry) cleanupLocked() {
	for id, entries := range r.items {
		kept := entries[:0]
		for _, it := range entries {
			if time.Since(it.created) < offerFor {
				kept = append(kept, it)
			}
		}
		r.items[id] = kept
	}
	for id, phone := range r.phones {
		if time.Since(phone.LastSeen) > 24*time.Hour {
			delete(r.phones, id)
			delete(r.items, id)
		}
	}
}
