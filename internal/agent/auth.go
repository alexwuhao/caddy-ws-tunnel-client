package agent

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// newNonce generates a cryptographically random 16-byte hex nonce.
func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// signHello computes HMAC-SHA256(key, "node|ts|nonce") and returns the hex digest.
// Both sides must use the same format; server verifies with the same scheme.
func signHello(key, node, nonce string, ts int64) string {
	msg := fmt.Sprintf("%s|%d|%s", node, ts, nonce)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// nowUnix returns current Unix timestamp for hello Ts field.
func nowUnix() int64 {
	return time.Now().Unix()
}
