package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/wjzhangq/caddy-ws-tunnel-client/internal/agent/proto"
	"go.uber.org/zap"
)

const (
	backoffMin = 1 * time.Second
	backoffMax = 30 * time.Second
)

// Agent owns the full lifecycle: one control channel + N data channels.
type Agent struct {
	URL  string
	Key  string
	Node string
	Log  *zap.Logger

	draining      atomic.Bool
	activeStreams  atomic.Int32
	dataChanCount atomic.Int32
	bytesIn       atomic.Int64
	bytesOut      atomic.Int64
	dialErrors    atomic.Int64
}

// Run connects to the server and maintains the tunnel until ctx is cancelled.
// Auth errors cause immediate exit; all other errors reconnect with exponential
// backoff + jitter.  Backoff resets to backoffMin after each successful connection.
func (a *Agent) Run(ctx context.Context) error {
	delay := backoffMin
	for {
		err := a.connect(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrAuth) {
			a.Log.Error("auth failure, not retrying", zap.Error(err))
			return err
		}
		jitter := time.Duration(rand.Int63n(int64(delay / 2)))
		wait := delay + jitter
		a.Log.Info("reconnecting", zap.Duration("in", wait), zap.NamedError("reason", err))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		delay *= 2
		if delay > backoffMax {
			delay = backoffMax
		}
	}
}

// connect runs one full cycle: handshake → spawn data channels → block until
// control connection dies → tear down data channels.
// On return, delay is reset to backoffMin by the caller (Run).
func (a *Agent) connect(ctx context.Context) error {
	dataCtx, cancelData := context.WithCancel(ctx)
	defer cancelData()

	var dataWg sync.WaitGroup
	var launched atomic.Bool

	err := runControl(
		ctx,
		a.URL, a.Key, a.Node,
		&a.draining,
		&a.activeStreams,
		&a.dataChanCount,
		&a.bytesIn, &a.bytesOut,
		&a.dialErrors,
		a.Log,
		func(res ControlResult) {
			if !launched.CompareAndSwap(false, true) {
				return
			}
			a.dataChanCount.Store(0)
			for i := 0; i < res.Channels; i++ {
				chID := i
				dataWg.Add(1)
				go func() {
					defer dataWg.Done()
					a.maintainDataChan(dataCtx, chID, res)
				}()
			}
		},
	)

	cancelData()
	dataWg.Wait()

	// Reset backoff: this connect() call reached the running loop, so next
	// failure should start fresh from backoffMin.
	return err
}

// maintainDataChan keeps data channel chID alive for the lifetime of dataCtx,
// reconnecting with backoff whenever it drops (C6 fix).
func (a *Agent) maintainDataChan(ctx context.Context, chID int, res ControlResult) {
	delay := backoffMin
	for {
		a.dataChanCount.Add(1)
		runErr := a.runDataChan(ctx, chID, res)
		a.dataChanCount.Add(-1)

		if ctx.Err() != nil {
			return
		}
		if runErr != nil {
			a.Log.Warn("data channel dropped, reconnecting",
				zap.Int("id", chID),
				zap.Error(runErr),
				zap.Duration("in", delay),
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > backoffMax {
			delay = backoffMax
		}
	}
}

func (a *Agent) runDataChan(ctx context.Context, chID int, res ControlResult) error {
	wsConn, err := a.dialDataWS(ctx, res.Session, chID)
	if err != nil {
		return err
	}
	netConn := &wsNetConn{ReadWriteCloser: newWsRWC(ctx, wsConn)}
	return runDataChannel(
		ctx,
		netConn,
		res.Mux,
		res.Allowlist,
		res.DialTimeout,
		&a.draining,
		&a.activeStreams,
		&a.bytesIn, &a.bytesOut,
		&a.dialErrors,
		a.Log,
	)
}

func (a *Agent) dialDataWS(ctx context.Context, session string, chID int) (*websocket.Conn, error) {
	conn, _, err := websocket.Dial(ctx, a.URL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("dial data ws: %w", err)
	}

	hello := proto.Hello{
		Role:      proto.RoleData,
		Node:      a.Node,
		Key:       a.Key,
		Session:   session,
		ChannelID: chID,
	}
	b, err := proto.WrapEnvelope(proto.TypeHello, hello)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("send data hello: %w", err)
	}

	_, msg, err := conn.Read(ctx)
	if err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("read data welcome: %w", err)
	}
	var env proto.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		conn.CloseNow()
		return nil, err
	}
	if env.Type != proto.TypeWelcome {
		conn.CloseNow()
		return nil, fmt.Errorf("expected data welcome, got %q", env.Type)
	}
	return conn, nil
}

// wsRWC adapts a *websocket.Conn to io.ReadWriteCloser using binary frames.
type wsRWC struct {
	ctx  context.Context
	conn *websocket.Conn
	buf  []byte
}

func newWsRWC(ctx context.Context, conn *websocket.Conn) io.ReadWriteCloser {
	return &wsRWC{ctx: ctx, conn: conn}
}

func (w *wsRWC) Read(p []byte) (int, error) {
	for len(w.buf) == 0 {
		_, msg, err := w.conn.Read(w.ctx)
		if err != nil {
			return 0, err
		}
		w.buf = msg
	}
	n := copy(p, w.buf)
	w.buf = w.buf[n:]
	return n, nil
}

func (w *wsRWC) Write(p []byte) (int, error) {
	if err := w.conn.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *wsRWC) Close() error {
	return w.conn.CloseNow()
}

// wsNetConn.net.Conn is verified at compile time.
var _ net.Conn = (*wsNetConn)(nil)
