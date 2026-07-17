package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg, err := parseConfig(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report := runPublicTest(ctx, cfg)
	if err := writeReports(report, cfg.JSONOut, cfg.MarkdownOut); err != nil {
		fmt.Fprintf(os.Stderr, "write reports: %v\n", err)
		return 2
	}
	fmt.Printf(
		"Public test %s: status=%s games=%d/%d reconnects=%d/%d p99=%.2fms\n",
		cfg.Preset,
		report.Status,
		report.CleanCompletedGames,
		report.GamesStarted,
		report.ReconnectsSuccessful,
		report.ReconnectsAttempted,
		report.Latency.P99,
	)
	fmt.Printf("Reports: %s and %s\n", cfg.JSONOut, cfg.MarkdownOut)
	if report.Status != "passed" {
		return 1
	}
	return 0
}
