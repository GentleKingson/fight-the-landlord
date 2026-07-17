#!/usr/bin/env bash

set -euo pipefail

profile="${1:-coverage.out}"
[[ -s "$profile" ]] || { printf 'coverage profile not found: %s\n' "$profile" >&2; exit 1; }

# Thresholds below were measured from the immutable upstream main revision.
baseline_sha="cd3409d1ef69aed68cb5db62b4127d25609db5ce"
printf 'Coverage baseline: %s\n' "$baseline_sha"

awk '
  BEGIN {
    # Values are the unrounded main-branch baseline, truncated by 0.01 point so
    # formatting differences cannot turn an unchanged profile into a failure.
    minimum["__total__"] = 51.03
    minimum["github.com/palemoky/fight-the-landlord/internal/config"] = 91.14
    minimum["github.com/palemoky/fight-the-landlord/internal/game/match"] = 75.87
    minimum["github.com/palemoky/fight-the-landlord/internal/game/room"] = 71.25
    minimum["github.com/palemoky/fight-the-landlord/internal/game/rule"] = 90.30
    minimum["github.com/palemoky/fight-the-landlord/internal/protocol/codec"] = 59.80
    minimum["github.com/palemoky/fight-the-landlord/internal/protocol/convert/payload"] = 73.01
    minimum["github.com/palemoky/fight-the-landlord/internal/server"] = 75.57
    minimum["github.com/palemoky/fight-the-landlord/internal/server/handler"] = 59.36
    minimum["github.com/palemoky/fight-the-landlord/internal/server/session"] = 81.12
  }
  NR == 1 { next }
  {
    location = $1
    # Test harnesses and locally installed Web dependencies are not shipped Go
    # application code and were not present in the pinned main baseline.
    if (location ~ /\/tests\/load\// || location ~ /\/web\/node_modules\//) {
      next
    }
    sub(":[^:]*$", "", location)
    package = location
    sub("/[^/]+$", "", package)
    statements = $2 + 0
    count = $3 + 0
    package_total[package] += statements
    total += statements
    if (count > 0) {
      package_covered[package] += statements
      covered += statements
    }
  }
  END {
    actual["__total__"] = 100 * covered / total
    for (package in package_total) {
      actual[package] = 100 * package_covered[package] / package_total[package]
    }
    failed = 0
    for (package in minimum) {
      label = package == "__total__" ? "total" : package
      if (!(package in actual)) {
        printf "coverage check failed: package missing from profile: %s\n", package > "/dev/stderr"
        failed = 1
        continue
      }
      printf "%-88s %6.2f%% (minimum %.2f%%)\n", label, actual[package], minimum[package]
      if (actual[package] + 0.000001 < minimum[package]) {
        printf "coverage regression: %s is %.2f%%, below %.2f%%\n", label, actual[package], minimum[package] > "/dev/stderr"
        failed = 1
      }
    }
    exit failed
  }
' "$profile"

printf 'Coverage thresholds passed\n'
