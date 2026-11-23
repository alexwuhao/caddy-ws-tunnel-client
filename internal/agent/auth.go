package agent

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/wjzhangq/caddy-ws-tunnel-client/internal/agent/proto"
)

// newNonce generates a cryptographically random 16-byte hex nonce.
func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// signHello computes HMAC-SHA256(secret, "node|ts|nonce") and returns the hex
// digest. The canonical string matches the server contract
// (caddy-ws-tunnel-plugin modules/tunnel/proto/auth.go).
func signHello(secret, node, nonce string, ts int64) string {
	msg := fmt.Sprintf("%s|%d|%s", node, ts, nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// nowUnix returns current Unix timestamp for hello Ts field.
func nowUnix() int64 {
	return time.Now().Unix()
}

// hmacSigner returns the key used to sign hellos. A dedicated --secret wins;
// otherwise the plaintext --key is used. Signing in both modes keeps a single
// code path: plaintext servers ignore the signature, HMAC servers verify it.
func hmacSigner(key, secret string) string {
	if secret != "" {
		return secret
	}
	return key
}

// helloKey returns the hello.Key field. In HMAC mode the secret must never be
// transmitted, so Key is left empty (the server ignores Key for HMAC nodes).
func helloKey(key, secret string) string {
	if secret != "" {
		return ""
	}
	return key
}

// newSignedHello builds a hello with a fresh ts/nonce/sig. Both the control and
// the data channel hellos must be signed when the server node has hmac_secret
// configured (design §10): the server rejects unsigned data hellos too.
func newSignedHello(key, secret, node, role, session string, channelID int) proto.Hello {
	ts := nowUnix()
	nonce := newNonce()
	return proto.Hello{
		Role:      role,
		Node:      node,
		Key:       helloKey(key, secret),
		Session:   session,
		ChannelID: channelID,
		Ts:        ts,
		Nonce:     nonce,
		Sig:       signHello(hmacSigner(key, secret), node, nonce, ts),
	}
}
