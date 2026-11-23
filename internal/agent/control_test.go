package agent

import (
	"testing"

	"github.com/wjzhangq/caddy-ws-tunnel-client/internal/agent/proto"
)

// TestPongTimestampEchoesServerTs: the server measures RTT as now - pong.Ts and
// sends its ping Ts in UnixNano, so the client must echo it verbatim. Using the
// client's own (second-resolution) clock produced ~1.8e12 ms "RTT" values.
func TestPongTimestampEchoesServerTs(t *testing.T) {
	const serverTs = int64(1795426702463531000) // UnixNano
	if got := pongTimestamp(proto.Ping{Nonce: "n", Ts: serverTs}); got != serverTs {
		t.Fatalf("pongTimestamp = %d, want echoed %d", got, serverTs)
	}
}

// TestPongTimestampFallback: a ping without Ts must not produce pong.Ts == 0,
// otherwise the server would compute a wildly large RTT.
func TestPongTimestampFallback(t *testing.T) {
	got := pongTimestamp(proto.Ping{Nonce: "n"})
	if got <= 0 {
		t.Fatalf("pongTimestamp(zero) = %d, want a positive fallback", got)
	}
	if got < 1_000_000_000 { // sanity: seconds-scale fallback is fine, 0/negative is not
		t.Fatalf("pongTimestamp fallback = %d, suspiciously small", got)
	}
}
