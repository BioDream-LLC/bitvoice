package store

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"path/filepath"
	"slices"
	"time"
)

// Device is a phone running the BitVoice app, paired with an extension.
type Device struct {
	ID         string    `json:"id"`
	Extension  string    `json:"extension"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform"` // ios or android
	PushToken  string    `json:"pushToken,omitempty"`
	SecretHash string    `json:"secretHash"` // SHA-256 of the device's secret: the secret is never stored
	Created    time.Time `json:"created"`
	LastSeen   time.Time `json:"lastSeen,omitzero"`
	LastPush   time.Time `json:"lastPush,omitzero"`
	PushError  string    `json:"pushError,omitempty"`
}

func (s *Store) devicesPath() string { return filepath.Join(s.dir, "devices.json") }

func (s *Store) loadDevices() []Device {
	var ds []Device
	_ = s.readDoc(s.devicesPath(), &ds)
	return ds
}

// Devices returns the devices of an extension, or every device for "".
func (s *Store) Devices(ext string) []Device {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	out := []Device{}
	for _, d := range s.loadDevices() {
		if ext == "" || d.Extension == ext {
			out = append(out, d)
		}
	}
	return out
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// AddDevice pairs a device; secret is what the device will prove itself with.
func (s *Store) AddDevice(d Device, secret string) (Device, error) {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	d.SecretHash, d.Created = hashSecret(secret), time.Now().UTC()
	ds := append(s.loadDevices(), d)
	return d, s.writeDoc(s.devicesPath(), ds)
}

// UpdateDevice changes a device in place.
func (s *Store) UpdateDevice(id string, f func(*Device)) (Device, error) {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	ds := s.loadDevices()
	i := slices.IndexFunc(ds, func(d Device) bool { return d.ID == id })
	if i < 0 {
		return Device{}, ErrNotFound
	}
	f(&ds[i])
	return ds[i], s.writeDoc(s.devicesPath(), ds)
}

// CheckDevice returns the device when the secret is right.
func (s *Store) CheckDevice(id, secret string) (Device, bool) {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	want := hashSecret(secret)
	for _, d := range s.loadDevices() {
		if d.ID == id && subtle.ConstantTimeCompare([]byte(d.SecretHash), []byte(want)) == 1 {
			return d, true
		}
	}
	return Device{}, false
}

// DeleteDevice unpairs a device.
func (s *Store) DeleteDevice(id string) error {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	ds := s.loadDevices()
	i := slices.IndexFunc(ds, func(d Device) bool { return d.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.writeDoc(s.devicesPath(), slices.Delete(ds, i, i+1))
}
