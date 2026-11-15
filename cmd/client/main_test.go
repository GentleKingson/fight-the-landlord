package main

import (
	"context"
	"testing"

	"github.com/palemoky/fight-the-landlord/internal/update"
)

func TestForceUpgrade_RejectsLatestBelowMinVersionOrDowngrade(t *testing.T) {
	origVersion := version
	origCheck := checkLatestFunc
	origApply := applyUpdateFunc
	origRelaunch := relaunchFunc
	origExit := exitFunc
	defer func() {
		version = origVersion
		checkLatestFunc = origCheck
		applyUpdateFunc = origApply
		relaunchFunc = origRelaunch
		exitFunc = origExit
	}()

	t.Run("latest release below server minVersion does not call Apply", func(t *testing.T) {
		version = "v1.0.0"
		applied := false
		exitCode := 0

		checkLatestFunc = func(_ context.Context, current string) (*update.Result, error) {
			return &update.Result{
				CurrentVersion: current,
				LatestVersion:  "v1.2.0",
				HasUpdate:      true,
				ReleaseURL:     "https://github.com/GentleKingson/fight-the-landlord/releases/tag/v1.2.0",
			}, nil
		}
		applyUpdateFunc = func(context.Context, *update.Result) error {
			applied = true
			return nil
		}
		relaunchFunc = func() error { return nil }
		exitFunc = func(code int) { exitCode = code }

		forceUpgrade("v2.0.0")
		if applied {
			t.Fatal("Apply should not be called when latest release is below minVersion")
		}
		if exitCode != 1 {
			t.Fatalf("exitCode = %d, want 1", exitCode)
		}
	})

	t.Run("latest release meeting minVersion calls Apply and Relaunch", func(t *testing.T) {
		version = "v1.0.0"
		applied := false
		relaunched := false
		exitCode := 0

		checkLatestFunc = func(_ context.Context, current string) (*update.Result, error) {
			return &update.Result{
				CurrentVersion: current,
				LatestVersion:  "v2.1.0",
				HasUpdate:      true,
				ReleaseURL:     "https://github.com/GentleKingson/fight-the-landlord/releases/tag/v2.1.0",
			}, nil
		}
		applyUpdateFunc = func(context.Context, *update.Result) error {
			applied = true
			return nil
		}
		relaunchFunc = func() error {
			relaunched = true
			return nil
		}
		exitFunc = func(code int) { exitCode = code }

		forceUpgrade("v2.0.0")
		if !applied || !relaunched {
			t.Fatalf("applied=%v relaunched=%v, want both true", applied, relaunched)
		}
		if exitCode != 0 {
			t.Fatalf("exitCode = %d, want 0", exitCode)
		}
	})
}
