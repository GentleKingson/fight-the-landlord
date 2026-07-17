package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	cfg, err := parseConfig(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load test configuration: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	report := runLoad(ctx, cfg)
	stop()
	if err := writeReports(report, cfg.JSONOut, cfg.MarkdownOut); err != nil {
		fmt.Fprintf(os.Stderr, "write load test reports: %v\n", err)
		return 2
	}
	fmt.Printf("Load test %s: %d/%d connections, %d/%d reconnects, p99 %.2fms\n", report.Status,
		report.ConnectionsSuccessful, report.ConnectionsAttempted,
		report.ReconnectsSuccessful, report.ReconnectsAttempted,
		report.LatencyMS.P99,
	)
	fmt.Printf("Reports: %s and %s\n", cfg.JSONOut, cfg.MarkdownOut)
	if report.Status != "passed" {
		return 1
	}
	return 0
}
