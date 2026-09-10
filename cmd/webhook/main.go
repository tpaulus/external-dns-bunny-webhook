package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/sethvargo/go-envconfig"
	"github.com/thejerf/suture/v4"
	"github.com/tpaulus/external-dns-bunny-webhook/internal/bunny"
	"github.com/tpaulus/external-dns-bunny-webhook/internal/health"
	"github.com/tpaulus/external-dns-bunny-webhook/internal/webhook"
)

const (
	serviceName = "external-dns-bunny-webhook"
)

type Options struct {
	LogFormat string          `env:"LOG_FORMAT, default=text"`
	LogLevel  string          `env:"LOG_LEVEL, default=info"`
	Bunny     bunny.Options   `env:", prefix=BUNNY_"`
	Health    health.Options  `env:", prefix=HEALTH_"`
	Webhook   webhook.Options `env:", prefix=WEBHOOK_"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var opts Options
	envconfig.MustProcess(ctx, &opts)

	log := createLogger(opts)
	slog.SetDefault(log)

	sup := suture.NewSimple(serviceName)

	health := &health.Server{Options: opts.Health}
	sup.Add(health)

	sup.Add(&webhook.Server{
		Options:     opts.Webhook,
		Provider:    bunny.NewProvider(bunny.NewDNSClient(cleanhttp.DefaultPooledClient(), opts.Bunny.APIKey), opts.Bunny),
		HealthyFunc: health.SetHealthy,
	})

	slog.InfoContext(ctx, "Starting external-dns-bunny-webhook")

	err := sup.Serve(ctx)
	switch {
	case errors.Is(err, context.Canceled):
		slog.Info("Shutdown complete.")
	case err != nil:
		slog.Error("Unexpected shutdown.", slog.Any("error", err))
	}
}

func createLogger(opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{
		Level: determineLogLevel(opts),
	}

	var handler slog.Handler
	switch strings.ToLower(opts.LogFormat) {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, handlerOpts)
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, handlerOpts)
	}

	return slog.New(handler)
}

func determineLogLevel(opts Options) slog.Leveler {
	switch strings.ToLower(opts.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
