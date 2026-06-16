package agent

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/wjzhangq/caddy-ws-tunnel-client/internal/agent/proto"
	"github.com/xtaci/smux"
	"go.uber.org/zap"
)

// runDataChannel establishes a smux session on wsConn and runs the
// AcceptStream loop until ctx is cancelled or the session dies.
// It respects the drain flag: once draining is set, no new streams are
// accepted and the function waits for in-flight streams to finish before
// returning.
func runDataChannel(
	ctx context.Context,
	wsConn net.Conn,
	muxCfg *proto.Mux,
	al *Allowlist,
	dialTimeoutNs *atomic.Int64,
	draining *atomic.Bool,
	activeStreams *atomic.Int32,
	bytesIn, bytesOut *atomic.Int64,
	dialErrors *atomic.Int64,
	log *zap.Logger,
) error {
	smuxCfg := buildSmuxConfig(muxCfg)
	sess, err := smux.Server(wsConn, smuxCfg)
	if err != nil {
		return err
	}
	defer sess.Close()

	// sessionDone is closed when the session closes naturally so the
	// ctx-watcher goroutine can exit without leaking (C3 fix).
	sessionDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sess.Close()
		case <-sessionDone:
		}
	}()
	defer close(sessionDone)

	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		// C5 fix: once draining, stop accepting new streams and wait for
		// in-flight streams to drain before returning.
		if draining.Load() {
			stream.Close()
			// Spin-wait with a short sleep; activeStreams tracks in-flight work.
			for activeStreams.Load() > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(100 * time.Millisecond):
				}
			}
			return nil
		}
		activeStreams.Add(1)
		go func() {
			defer activeStreams.Add(-1)
			handleStream(ctx, stream, al, dialTimeoutNs, bytesIn, bytesOut, dialErrors, log)
		}()
	}
}

// buildSmuxConfig converts proto.Mux into a smux.Config, filling safe defaults.
func buildSmuxConfig(m *proto.Mux) *smux.Config {
	cfg := smux.DefaultConfig()
	if m == nil {
		return cfg
	}
	if m.Version != 0 {
		cfg.Version = m.Version
	}
	if d, err := time.ParseDuration(m.KeepAliveItvl); err == nil && d > 0 {
		cfg.KeepAliveInterval = d
	}
	if d, err := time.ParseDuration(m.KeepAliveTimeout); err == nil && d > 0 {
		cfg.KeepAliveTimeout = d
	}
	if m.MaxFrameSize > 0 {
		cfg.MaxFrameSize = m.MaxFrameSize
	}
	if m.MaxReceiveBuffer > 0 {
		cfg.MaxReceiveBuffer = m.MaxReceiveBuffer
	}
	if m.MaxStreamBuffer > 0 {
		cfg.MaxStreamBuffer = m.MaxStreamBuffer
	}
	return cfg
}

// wsNetConn adapts a coder/websocket connection to net.Conn for smux.
type wsNetConn struct {
	io.ReadWriteCloser
}

func (w *wsNetConn) LocalAddr() net.Addr               { return dummyAddr{} }
func (w *wsNetConn) RemoteAddr() net.Addr              { return dummyAddr{} }
func (w *wsNetConn) SetDeadline(_ time.Time) error     { return nil }
func (w *wsNetConn) SetReadDeadline(_ time.Time) error  { return nil }
func (w *wsNetConn) SetWriteDeadline(_ time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "ws" }
func (dummyAddr) String() string  { return "ws" }
