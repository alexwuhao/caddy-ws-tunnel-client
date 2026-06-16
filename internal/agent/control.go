package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/wjzhangq/caddy-ws-tunnel-client/internal/agent/proto"
	"go.uber.org/zap"
)

// ErrAuth is returned by runControl when the server rejects our credentials.
// Callers should not retry on this error.
var ErrAuth = errors.New("auth rejected by server")

// ControlResult is returned by runControl after a successful handshake.
type ControlResult struct {
	Session     string
	Heartbeat   time.Duration
	Mux         *proto.Mux
	Channels    int
	DialTimeout *atomic.Int64 // nanoseconds; shared so reload_config updates reach live channels
	Allowlist   *Allowlist
}

// runControl opens the control WS, performs hello/welcome, then enters the
// control loop (ping/pong, stats, reload_config, drain, bye/error).
// It blocks until ctx is cancelled or the connection dies.
// On successful handshake, cb is called with the ControlResult so the caller
// can spawn data channels before the loop starts.
func runControl(
	ctx context.Context,
	wsURL, key, node string,
	draining *atomic.Bool,
	activeStreams *atomic.Int32,
	dataChanCount *atomic.Int32,
	bytesIn, bytesOut *atomic.Int64,
	dialErrors *atomic.Int64,
	log *zap.Logger,
	cb func(ControlResult),
) error {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return fmt.Errorf("dial control ws: %w", err)
	}
	defer conn.CloseNow()

	// Send hello.
	ts := nowUnix()
	nonce := newNonce()
	hello := proto.Hello{
		Role:  proto.RoleControl,
		Node:  node,
		Key:   key,
		Ts:    ts,
		Nonce: nonce,
		Sig:   signHello(key, node, nonce, ts),
	}
	if err := sendMsg(ctx, conn, proto.TypeHello, hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	// Read welcome.
	welcome, err := readWelcome(ctx, conn)
	if err != nil {
		return fmt.Errorf("read welcome: %w", err)
	}
	if welcome.Config == nil {
		return fmt.Errorf("welcome missing config")
	}

	al := &Allowlist{}
	al.Replace(welcome.Config.Ports)

	heartbeat := 15 * time.Second
	if d, err := time.ParseDuration(welcome.Heartbeat); err == nil && d > 0 {
		heartbeat = d
	}

	// dialTimeout is shared with data channels via an atomic so reload_config
	// updates propagate without reconnecting.
	dialTimeoutNs := &atomic.Int64{}
	dialTimeoutNs.Store(int64(10 * time.Second))
	if d, err := time.ParseDuration(welcome.Config.DialTimeout); err == nil && d > 0 {
		dialTimeoutNs.Store(int64(d))
	}

	channels := welcome.Channels
	if channels == 0 {
		channels = 1
	}

	res := ControlResult{
		Session:     welcome.Session,
		Heartbeat:   heartbeat,
		Mux:         welcome.Mux,
		Channels:    channels,
		DialTimeout: dialTimeoutNs,
		Allowlist:   al,
	}
	cb(res)

	statsTicker := time.NewTicker(heartbeat)
	defer statsTicker.Stop()
	pingTicker := time.NewTicker(heartbeat)
	defer pingTicker.Stop()

	for {
		// Use a read deadline of heartbeat/2 to service tickers without a separate goroutine.
		msgCtx, cancel := context.WithTimeout(ctx, heartbeat/2)
		typ, raw, readErr := recvMsg(msgCtx, conn)
		cancel()

		if readErr != nil {
			// Parent cancelled → graceful shutdown.
			if ctx.Err() != nil {
				_ = sendMsg(ctx, conn, proto.TypeBye, proto.Bye{Reason: "shutdown"})
				return nil
			}
			// Read deadline expired (expected heartbeat tick) → service tickers and continue.
			if errors.Is(readErr, context.DeadlineExceeded) {
				select {
				case <-statsTicker.C:
					_ = sendStats(ctx, conn, dataChanCount, activeStreams, bytesIn, bytesOut, dialErrors)
				default:
				}
				select {
				case <-pingTicker.C:
					_ = sendMsg(ctx, conn, proto.TypePing, proto.Ping{Nonce: newNonce(), Ts: nowUnix()})
				default:
				}
				continue
			}
			// Any other error (TCP RST, websocket close, etc.) is a real connection failure.
			return fmt.Errorf("control read: %w", readErr)
		}

		switch typ {
		case proto.TypePing:
			var p proto.Ping
			_ = json.Unmarshal(raw, &p)
			_ = sendMsg(ctx, conn, proto.TypePong, proto.Pong{Nonce: p.Nonce, Ts: nowUnix()})

		case proto.TypePong:
			// RTT measurement: no-op for now.

		case proto.TypeReloadConfig:
			var cfg proto.Config
			if err := json.Unmarshal(raw, &cfg); err != nil {
				log.Warn("reload_config parse error", zap.Error(err))
				continue
			}
			al.Replace(cfg.Ports)
			if d, err := time.ParseDuration(cfg.DialTimeout); err == nil && d > 0 {
				dialTimeoutNs.Store(int64(d))
			}
			log.Info("config reloaded", zap.Int("ports", len(cfg.Ports)))

		case proto.TypeDrain:
			draining.Store(true)
			log.Info("drain received, no longer accepting new streams")

		case proto.TypeBye:
			log.Info("server sent bye, closing")
			return nil

		case proto.TypeError:
			var e proto.Error
			_ = json.Unmarshal(raw, &e)
			log.Error("server error", zap.String("code", e.Code), zap.String("msg", e.Msg))
			if isAuthError(e.Code) {
				return fmt.Errorf("%w: %s %s", ErrAuth, e.Code, e.Msg)
			}

		default:
			// ignore unknown types for forward compatibility
		}

		// Service tickers non-blocking after a successful message read.
		select {
		case <-statsTicker.C:
			_ = sendStats(ctx, conn, dataChanCount, activeStreams, bytesIn, bytesOut, dialErrors)
		default:
		}
		select {
		case <-pingTicker.C:
			_ = sendMsg(ctx, conn, proto.TypePing, proto.Ping{Nonce: newNonce(), Ts: nowUnix()})
		default:
		}
	}
}

func readWelcome(ctx context.Context, conn *websocket.Conn) (*proto.Welcome, error) {
	typ, raw, err := recvMsg(ctx, conn)
	if err != nil {
		return nil, err
	}
	if typ != proto.TypeWelcome {
		return nil, fmt.Errorf("expected welcome, got %q", typ)
	}
	var w proto.Welcome
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func sendStats(
	ctx context.Context,
	conn *websocket.Conn,
	dataChanCount *atomic.Int32,
	activeStreams *atomic.Int32,
	bytesIn, bytesOut *atomic.Int64,
	dialErrors *atomic.Int64,
) error {
	return sendMsg(ctx, conn, proto.TypeStats, proto.Stats{
		Channels:        int(dataChanCount.Load()),
		Streams:         int(activeStreams.Load()),
		BytesIn:         bytesIn.Load(),
		BytesOut:        bytesOut.Load(),
		LocalDialErrors: dialErrors.Load(),
	})
}

func sendMsg(ctx context.Context, conn *websocket.Conn, typ string, payload any) error {
	b, err := proto.WrapEnvelope(typ, payload)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageBinary, b)
}

func recvMsg(ctx context.Context, conn *websocket.Conn) (typ string, payload json.RawMessage, err error) {
	_, b, err := conn.Read(ctx)
	if err != nil {
		return "", nil, err
	}
	var env proto.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return "", nil, err
	}
	return env.Type, env.Payload, nil
}

func isAuthError(code string) bool {
	switch code {
	case "auth_failed", "invalid_key", "forbidden":
		return true
	}
	return false
}
