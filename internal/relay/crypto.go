// Package relay connects the companion to a LolMobil relay (the Cloudflare
// Worker in relay/) so the phone can reach the PC from anywhere, like
// Mimic's Rift. Everything that goes through the relay is end-to-end
// encrypted with a secret only the PC and the paired phones know: the relay
// sees a room id and ciphertext.
//
// The secret (32 random bytes) travels in the pairing link fragment
// (https://<relay>/#k=<secret>), never sent to the relay. From it:
//
//	room id   = hex(SHA-256("lolmobil-room" || secret))[:32]
//	host auth = hex(SHA-256("lolmobil-host" || secret))
//	AES key   = SHA-256("lolmobil-key" || secret)            (AES-256-GCM)
//
// A message on the wire is base64(nonce(12) || ciphertext || tag(16)).
package relay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Keys are derived from the pairing secret.
type Keys struct {
	RoomID   string
	HostAuth string
	aead     cipher.AEAD
}

func derive(label string, secret []byte) []byte {
	h := sha256.New()
	h.Write([]byte(label))
	h.Write(secret)
	return h.Sum(nil)
}

// NewSecret returns a fresh secret, base64url encoded without padding.
func NewSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DeriveKeys parses a base64url secret and derives the relay keys.
func DeriveKeys(secret string) (*Keys, error) {
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("invalid remote secret")
	}
	block, err := aes.NewCipher(derive("lolmobil-key", raw))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keys{
		RoomID:   hex.EncodeToString(derive("lolmobil-room", raw))[:32],
		HostAuth: hex.EncodeToString(derive("lolmobil-host", raw)),
		aead:     aead,
	}, nil
}

// Seal encrypts a message for the wire.
func (k *Keys) Seal(plain []byte) string {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(k.aead.Seal(nonce, nonce, plain, nil))
}

// Open decrypts a wire message.
func (k *Keys) Open(wire string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(wire)
	if err != nil {
		return nil, err
	}
	n := k.aead.NonceSize()
	if len(data) < n+k.aead.Overhead() {
		return nil, errors.New("message too short")
	}
	return k.aead.Open(nil, data[:n], data[n:], nil)
}
