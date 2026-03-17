// Command imap-server runs an IMAP server backed by DynamoDB and S3.
// Messages are ingested by SES receipt rules; this server provides
// read access to them via the IMAP protocol.
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/soapergem/ses-imap/internal/config"
	imapserver "github.com/soapergem/ses-imap/internal/imap"
	"github.com/soapergem/ses-imap/internal/store"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Configure structured logging.
	var logLevel slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	slog.Info("starting IMAP server", "log_level", cfg.LogLevel)

	ctx := context.Background()

	st, err := store.New(ctx, cfg)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}

	// Ensure the default mailbox exists.
	if _, err := st.EnsureMailbox(ctx, cfg.DefaultMailbox); err != nil {
		log.Fatalf("failed to ensure default mailbox: %v", err)
	}

	// Create SSM client for Parameter Store auth.
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}
	ssmClient := ssm.NewFromConfig(awsCfg)
	auth := store.NewAuth(cfg, ssmClient)

	srv := imapserver.NewServer(cfg, st, auth)

	// Start health check endpoint.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	go func() {
		slog.Info("health check listening", "addr", cfg.HealthAddr)
		if err := http.ListenAndServe(cfg.HealthAddr, nil); err != nil {
			log.Fatalf("health check server error: %v", err)
		}
	}()

	// Handle graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		if err := srv.Close(); err != nil {
			log.Printf("error closing server: %v", err)
		}
	}()

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("IMAP server error: %v", err)
	}
}
