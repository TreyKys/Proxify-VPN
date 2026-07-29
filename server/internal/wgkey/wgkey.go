// Package wgkey validates WireGuard keys.
//
// The control plane never sees a private key — the device generates its own
// keypair and sends only the public half. This package exists so a malformed or
// hostile key is rejected at the API boundary rather than being written into a
// `wg set` command on an edge box.
package wgkey

import (
	"encoding/base64"
	"errors"
)

const KeyLen = 32

var ErrInvalidKey = errors.New("wgkey: invalid public key")

// Validate accepts a standard base64-encoded 32-byte WireGuard key and returns
// it in canonical form. Re-encoding is deliberate: it means whatever we store
// and later hand to the edge agent is a value we produced, not attacker-chosen
// text that merely decoded successfully.
func Validate(key string) (string, error) {
	if len(key) != 44 {
		return "", ErrInvalidKey
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != KeyLen {
		return "", ErrInvalidKey
	}
	// The all-zero key is a valid encoding but never a real public key; it is a
	// classic "does this thing validate anything" probe.
	var zero [KeyLen]byte
	if string(raw) == string(zero[:]) {
		return "", ErrInvalidKey
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
