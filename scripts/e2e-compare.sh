#!/usr/bin/env bash
# Run tests/e2e and compare the result with docs/build/e2e-baseline.txt.
# The script fails when a test that passed in the baseline does not pass now.
# Set E2E_RUN to a go test -run expression to run a subset.
# Set E2E_WRITE_BASELINE=1 to write the baseline instead of a compare.
set -uo pipefail
cd "$(dirname "$0")/.."
baseline=docs/build/e2e-baseline.txt
out=$(mktemp)
args=(-tags e2e -count=1 -timeout 60m -v)
if [ -n "${E2E_RUN:-}" ]; then args+=(-run "$E2E_RUN"); fi
go test "${args[@]}" ./tests/e2e/... >"$out.log" 2>&1
grep -E '^[[:space:]]*--- (PASS|FAIL|SKIP)' "$out.log" | sed -E 's/^[[:space:]]+//; s/ \([0-9.]+s\)$//' | sort -u >"$out"
if [ "${E2E_WRITE_BASELINE:-}" = "1" ]; then
  cp "$out" "$baseline"
  echo "wrote $baseline: $(grep -c '^--- PASS' "$baseline") pass, $(grep -c '^--- FAIL' "$baseline") fail"
  exit 0
fi
regressed=0
while read -r line; do
  name=${line#--- PASS: }
  if grep -qx -- "--- PASS: $name" "$out"; then continue; fi
  if [ -n "${E2E_RUN:-}" ] && ! grep -q -- ": $name\$" "$out"; then continue; fi
  echo "REGRESSION: $name"
  regressed=1
done < <(grep '^--- PASS' "$baseline")
if [ "$regressed" = "1" ]; then
  echo "log: $out.log"
  exit 1
fi
echo "no regression ($(grep -c '^--- PASS' "$out") pass)"
