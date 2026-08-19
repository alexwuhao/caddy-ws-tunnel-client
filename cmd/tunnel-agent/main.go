package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/wjzhangq/caddy-ws-tunnel-client/internal/agent"
	"go.uber.org/zap"
)

func main() {
	var (
		urlFlag   = flag.String("url", "", "WebSocket URL (wss://host/ws)")
		keyFlag   = flag.String("key", "", "Auth key")
		nodeFlag  = flag.String("node", "", "Node identifier (derived from key if empty)")
		debugFlag = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()

	wsURL := firstNonEmpty(*urlFlag, os.Getenv("TUNNEL_URL"))
	key := firstNonEmpty(*keyFlag, os.Getenv("TUNNEL_KEY"))

	if wsURL == "" || key == "" {
		fmt.Fprintf(os.Stderr, "Usage: tunnel-agent --url <wss://host/ws> --key <key>\n")
		fmt.Fprintf(os.Stderr, "  or set TUNNEL_URL / TUNNEL_KEY environment variables.\n")
		os.Exit(1)
	}

	node := *nodeFlag
	if node == "" {
		// Derive a stable node name from the key (first 8 chars).
		if len(key) >= 8 {
			node = key[:8]
		} else {
			node = key
		}
	}

	log, err := buildLogger(*debugFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync() //nolint:errcheck

	a := &agent.Agent{
		URL:  wsURL,
		Key:  key,
		Node: node,
		Log:  log,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("tunnel-agent starting",
		zap.String("url", maskURL(wsURL)),
		zap.String("node", node),
	)

	if err := a.Run(ctx); err != nil {
		log.Error("agent exited with error", zap.Error(err))
		os.Exit(1)
	}
	log.Info("tunnel-agent stopped")
}

func buildLogger(debug bool) (*zap.Logger, error) {
	if debug {
		return zap.NewDevelopment()
	}
	return zap.NewProduction()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// maskURL replaces the query string (which may contain keys) for logging.
func maskURL(u string) string {
	if before, _, found := strings.Cut(u, "?"); found {
		return before + "?..."
	}
	return u
}
