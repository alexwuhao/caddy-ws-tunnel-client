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

// version is overridden at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	var (
		urlFlag     = flag.String("url", "", "WebSocket URL (wss://host/ws)")
		keyFlag     = flag.String("key", "", "Auth key (plaintext mode)")
		secretFlag  = flag.String("secret", "", "HMAC secret; signs every hello and keeps the secret off the wire (requires --node)")
		nodeFlag    = flag.String("node", "", "Node identifier (derived from key if empty; required with --secret)")
		debugFlag   = flag.Bool("debug", false, "Enable debug logging")
		versionFlag = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Printf("tunnel-agent %s\n", version)
		return
	}

	wsURL := firstNonEmpty(*urlFlag, os.Getenv("TUNNEL_URL"))
	key := firstNonEmpty(*keyFlag, os.Getenv("TUNNEL_KEY"))
	secret := firstNonEmpty(*secretFlag, os.Getenv("TUNNEL_SECRET"))

	if wsURL == "" || (key == "" && secret == "") {
		fmt.Fprintf(os.Stderr, "Usage: tunnel-agent --url <wss://host/ws> (--key <key> | --secret <secret> --node <node>)\n")
		fmt.Fprintf(os.Stderr, "  or set TUNNEL_URL + TUNNEL_KEY / TUNNEL_SECRET environment variables.\n")
		os.Exit(1)
	}

	node := *nodeFlag
	if node == "" {
		if secret != "" {
			// The node name cannot be derived from the secret (the predecessor
			// behavior derived it from the plaintext key); require it explicitly.
			fmt.Fprintf(os.Stderr, "--node is required when --secret / TUNNEL_SECRET is set.\n")
			os.Exit(1)
		}
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
		URL:    wsURL,
		Key:    key,
		Secret: secret,
		Node:   node,
		Log:    log,
	}

	// os.Interrupt covers Ctrl+C on Windows; SIGTERM covers Unix service stops.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("tunnel-agent starting",
		zap.String("url", maskURL(wsURL)),
		zap.String("node", node),
		zap.Bool("hmac", secret != ""),
		zap.String("version", version),
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
