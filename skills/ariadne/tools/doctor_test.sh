#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DOCTOR="$SCRIPT_DIR/doctor.sh"

check() {
  expected="$1"
  input="$2"
  actual="$(printf '%s' "$input" | ARIADNE_DOCTOR_CLASSIFY_ONLY=1 bash "$DOCTOR")"
  if [ "$actual" != "$expected" ]; then
    printf 'expected %s, got %s for %s\n' "$expected" "$actual" "$input" >&2
    exit 1
  fi
}

healthy_services='"qdrant":{"up":true},"ollama":{"up":true},"collection":{"status":"green"},"qdrant_agents":["com.ariadne.qdrant"]'
check healthy "{$healthy_services,\"ok\":true}"
check degraded "{$healthy_services,\"ok\":false,\"maintenance\":{\"consolidate\":{\"status\":\"deferred\"}}}"
check failed "{$healthy_services,\"ok\":false,\"maintenance\":{\"maintenance\":{\"status\":\"failed\"}}}"
check failed '{"qdrant":{"up":false},"ollama":{"up":true},"collection":{"status":"green"}}'

echo "doctor status classification: PASS"
