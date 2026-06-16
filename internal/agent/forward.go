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

const copyBufSize = 32 * 1024

// handleStream reads the stream header, looks up the allowlist, dials the
// local address, and runs bidirectional io.Copy until either side closes.
// ctx is the data-channel context; cancelling it aborts in-progress dials
// immediately (C7 fix). dialTimeoutNs is read atomically so reload_config
// updates take effect without reconnecting (C2 fix).
func handleStream(
	ctx context.Context,
	stream *smux.Stream,
	al *Allowlist,
	dialTimeoutNs *atomic.Int64,
	bytesIn, bytesOut *atomic.Int64,
	dialErrors *atomic.Int64,
	log *zap.Logger,
) {
	defer stream.Close()

	br := proto.NewHeaderReader(stream)
	_, portID, err := proto.ReadStreamHeader(br)
	if err != nil {
		log.Warn("read stream header failed", zap.Error(err))
		return
	}

	addr, ok := al.Lookup(portID)
	if !ok {
		log.Warn("port not in allowlist, closing stream", zap.Uint64("port_id", portID))
		return
	}

	dialTimeout := time.Duration(dialTimeoutNs.Load())
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	defer dialCancel()

	local, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		log.Warn("local dial failed", zap.String("addr", addr), zap.Error(err))
		dialErrors.Add(1)
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.CopyBuffer(local, br, make([]byte, copyBufSize))
		bytesIn.Add(n)
		if tc, ok := local.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck
		}
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.CopyBuffer(stream, local, make([]byte, copyBufSize))
		bytesOut.Add(n)
		stream.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}
