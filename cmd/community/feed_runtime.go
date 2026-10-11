package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/worker"
	"github.com/kasuganosora/community/models"
)

func runtimeValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	if value := strings.TrimSpace(config.C.Other[name]); value != "" {
		return value
	}
	return fallback
}

func feedRuntimeOptions() (string, string, worker.Options, error) {
	role, mode := runtimeValue("COMMUNITY_ROLE", "all"), runtimeValue("FEED_MODE", "shadow")
	if role != "all" && role != "api" && role != "feed-worker" {
		return "", "", worker.Options{}, fmt.Errorf("COMMUNITY_ROLE must be all, api or feed-worker")
	}
	if role == "feed-worker" && mode == "legacy" {
		return "", "", worker.Options{}, fmt.Errorf("feed-worker requires durable or shadow FEED_MODE")
	}
	options := worker.DefaultOptions()
	for _, entry := range []struct {
		name     string
		target   *int
		min, max int
	}{
		{"FEED_SCAN_CONCURRENCY", &options.ScanConcurrency, 1, 16},
		{"FEED_DELIVERY_CONCURRENCY", &options.DeliveryConcurrency, 1, 32},
		{"FEED_BATCH_SIZE", &options.BatchSize, 128, 1024},
	} {
		value, err := strconv.Atoi(runtimeValue(entry.name, strconv.Itoa(*entry.target)))
		if err != nil || value < entry.min || value > entry.max {
			return "", "", options, fmt.Errorf("invalid %s", entry.name)
		}
		*entry.target = value
	}
	if err := feed.ConfigureRuntime(mode); err != nil {
		return "", "", options, err
	}
	options.Shadow = mode == "shadow"
	return role, mode, options, nil
}

func startFeedRuntime(ctx context.Context, role, mode string, options worker.Options) *worker.Worker {
	// Migrations ran before this point. Losing the outbox now must reject the
	// business transaction instead of silently falling back to memory delivery.
	models.RequireFeedOutbox()
	if role == "api" || mode == "legacy" {
		return nil
	}
	w := worker.New(db.GetDB(), options)
	w.Start(ctx)
	return w
}

func waitFeedWorker(stop context.CancelFunc, w *worker.Worker) {
	signalContext, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-signalContext.Done()
	stop()
	if w != nil {
		w.Wait()
	}
}
