#!/usr/bin/env bash
# Runs a command and, when it fails, republishes its failing lines as workflow
# error annotations. Annotations stay readable through the public checks API
# when the job logs are not.
set -o pipefail
title=$1
shift
log=$(mktemp)
"$@" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
if [ "$status" -ne 0 ]; then
  grep -E -- '--- FAIL|^FAIL|panic:|_test\.go:[0-9]+|\.go:[0-9]+:[0-9]+:|error:|undefined|timed out|fatal|No such file' "$log" | head -180 > "$log.hits" || true
  [ -s "$log.hits" ] || tail -40 "$log" > "$log.hits"
  split -l 20 "$log.hits" "$log.part."
  n=0
  for part in "$log.part."*; do
    n=$((n + 1))
    [ "$n" -gt 9 ] && break
    message=$(sed -e 's/%/%25/g' -e 's/\r//g' "$part" | awk 'BEGIN { ORS = "%0A" } { print }')
    echo "::error title=$title ($n)::$message"
  done
fi
exit "$status"
