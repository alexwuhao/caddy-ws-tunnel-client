package agent

import (
	"testing"
)

// TestSignHelloKnownVector pins the canonical wire format shared with the
// server (HMAC-SHA256 over "node|ts|nonce"). The expected digest was produced
// independently with:
//
//	printf 'node1|1700000000|nonce123' | openssl dgst -sha256 -hmac secret
func TestSignHelloKnownVector(t *testing.T) {
	const want = "4eb403dcebaf0eae263f4a021c5bed38d698fc413fb916429accde867cbb5947"
	got := signHello("secret", "node1", "nonce123", 1700000000)
	if got != want {
		t.Fatalf("signHello = %q, want %q", got, want)
	}
}

func TestHmacSignerAndHelloKey(t *testing.T) {
	// Plaintext posture: sign with key (server ignores the sig) and send key.
	if got := hmacSigner("k", ""); got != "k" {
		t.Fatalf("hmacSigner(plaintext) = %q, want k", got)
	}
	if got := helloKey("k", ""); got != "k" {
		t.Fatalf("helloKey(plaintext) = %q, want k", got)
	}
	// HMAC posture: sign with the secret and never transmit it.
	if got := hmacSigner("k", "s3cret"); got != "s3cret" {
		t.Fatalf("hmacSigner(hmac) = %q, want secret", got)
	}
	if got := helloKey("k", "s3cret"); got != "" {
		t.Fatalf("helloKey(hmac) = %q, want empty (secret must stay off the wire)", got)
	}
}

func TestNewNonce(t *testing.T) {
	a, b := newNonce(), newNonce()
	if len(a) != 32 {
		t.Fatalf("nonce length = %d, want 32 hex chars (16 bytes)", len(a))
	}
	if a == b {
		t.Fatal("two nonces must differ")
	}
}

func TestNewSignedHelloControl(t *testing.T) {
	h := newSignedHello("key12345", "s3cret", "node1", "control", "", 0)
	if h.Role != "control" || h.Node != "node1" {
		t.Fatalf("hello role/node = %q/%q", h.Role, h.Node)
	}
	if h.Key != "" {
		t.Fatalf("hello.Key = %q, want empty in HMAC mode", h.Key)
	}
	if h.Nonce == "" || h.Ts == 0 {
		t.Fatalf("hello missing ts/nonce: ts=%d nonce=%q", h.Ts, h.Nonce)
	}
	if want := signHello("s3cret", "node1", h.Nonce, h.Ts); h.Sig != want {
		t.Fatalf("hello.Sig = %q, want %q", h.Sig, want)
	}
	if h.Session != "" || h.ChannelID != 0 {
		t.Fatalf("control hello must not carry session/channel: %+v", h)
	}
}

func TestNewSignedHelloDataKeepsBinding(t *testing.T) {
	h := newSignedHello("key12345", "s3cret", "node1", "data", "sess-abc", 3)
	if h.Role != "data" || h.Session != "sess-abc" || h.ChannelID != 3 {
		t.Fatalf("data hello lost binding: %+v", h)
	}
	if want := signHello("s3cret", "node1", h.Nonce, h.Ts); h.Sig != want {
		t.Fatalf("data hello signature mismatch")
	}
	// Fresh nonce per hello: a data-channel reconnect must not replay.
	h2 := newSignedHello("key12345", "s3cret", "node1", "data", "sess-abc", 3)
	if h.Nonce == h2.Nonce {
		t.Fatal("data hellos must use fresh nonces")
	}
}

func TestNewSignedHelloPlaintextMode(t *testing.T) {
	h := newSignedHello("plain-key", "", "node1", "control", "", 0)
	if h.Key != "plain-key" {
		t.Fatalf("plaintext hello.Key = %q, want plain-key", h.Key)
	}
	if h.Sig == "" {
		t.Fatal("plaintext mode still sends a signature (ignored by the server)")
	}
	if want := signHello("plain-key", "node1", h.Nonce, h.Ts); h.Sig != want {
		t.Fatalf("plaintext signature should be keyed by the plaintext key")
	}
}
