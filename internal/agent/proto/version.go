// Package proto mirrors the server-side contract from
// github.com/wjzhangq/caddy-ws-tunnel-plugin/modules/tunnel/proto.
// Field names and wire formats must stay byte-for-byte identical.
package proto

// ProtocolVersion is the stream-header version byte (design §6.2).
const ProtocolVersion uint8 = 1
