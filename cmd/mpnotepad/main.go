package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"mpnotepad/internal/config"
	"mpnotepad/internal/server"
	"mpnotepad/internal/store"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		slog.Error(`usage: mpnotepad serve [-addr host:port]`)
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	addrFlag := serveCmd.String("addr", "", "host:port (overrides MPN_ADDR)")
	_ = serveCmd.Parse(os.Args[2:])
	if *addrFlag != "" {
		cfg.Addr = *addrFlag
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv, err := server.New(cfg, log, st)
	if err != nil {
		log.Error("server init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}
