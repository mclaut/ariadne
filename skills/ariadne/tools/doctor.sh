#!/bin/bash
# doctor.sh — full-chain health check for the Ariadne stack.
# Exits 0 when everything is green, 1 when critical checks fail, and 2 when
# services work but observability or accounting is degraded.
set -u

HOME_DIR="${HOME}"
ARIADNE="${HOME_DIR}/.ariadne"
COLLECTION="${ARIADNE_COLLECTION:-ariadne}"
QDRANT="http://localhost:6333"
OLLAMA="http://localhost:11434"
FAIL=0
WARN=0

ok()   { printf "  ✓ %s\n" "$1"; }
bad()  { printf "  ✗ %s\n" "$1"; FAIL=1; }
warn() { printf "  ! %s\n" "$1"; WARN=1; }

resolve_ariadnectl() {
  if [ -n "${ARIADNE_CTL_PATH:-}" ] && [ -x "$ARIADNE_CTL_PATH" ]; then
    printf '%s\n' "$ARIADNE_CTL_PATH"
    return
  fi
  python3 - <<'PY' 2>/dev/null
import glob,json,os,pathlib
candidates=[]
try:
    import tomllib
    with open(os.path.expanduser("~/.codex/config.toml"),"rb") as f:
        command=tomllib.load(f).get("mcp_servers",{}).get("ariadne",{}).get("command","")
        if command: candidates.append(str(pathlib.Path(command).with_name("ariadnectl")))
except Exception: pass
try:
    with open(os.path.expanduser("~/.claude.json")) as f:
        command=json.load(f).get("mcpServers",{}).get("ariadne",{}).get("command","")
        if command: candidates.append(str(pathlib.Path(command).with_name("ariadnectl")))
except Exception: pass
candidates += sorted(glob.glob(os.path.expanduser("~/.ariadne/releases/*/bin/ariadnectl")), reverse=True)
candidates.append(os.path.expanduser("~/.ariadne/bin/ariadnectl"))
for path in candidates:
    if os.path.isfile(path) and os.access(path,os.X_OK):
        print(path); break
PY
}

CTL="$(resolve_ariadnectl)"
ACTIVE_BIN=""
if [ -n "$CTL" ]; then ACTIVE_BIN="$(dirname "$CTL")"; fi

echo "== binaries =="
for b in ariadne ariadnectl import ariadne-hook ariadne-tray; do
  if [ -n "$ACTIVE_BIN" ] && [ -x "$ACTIVE_BIN/$b" ]; then
    ok "$ACTIVE_BIN/$b"
  else
    bad "missing active binary: $b"
  fi
done
if [ -x "$ARIADNE/bin/qdrant" ]; then ok "$ARIADNE/bin/qdrant"; else bad "missing: $ARIADNE/bin/qdrant"; fi
if [ -n "$CTL" ]; then
  RUNTIME_VERSION="$($CTL version 2>/dev/null || true)"
  if [ -n "$RUNTIME_VERSION" ]; then ok "active runtime $RUNTIME_VERSION"; else warn "active runtime does not report its version"; fi
fi

echo "== services =="
if curl -sf --max-time 3 "$QDRANT/healthz" >/dev/null; then
  ok "Qdrant up ($QDRANT)"
else
  bad "Qdrant DOWN — try: $ARIADNE/bin/ariadnectl start"
fi
if curl -sf --max-time 3 "$OLLAMA/api/version" >/dev/null; then
  ok "Ollama up ($OLLAMA)"
else
  bad "Ollama DOWN — try: brew services start ollama"
fi

echo "== embedding model =="
if curl -sf --max-time 5 "$OLLAMA/api/tags" 2>/dev/null | grep -q '"bge-m3'; then
  ok "bge-m3 present"
else
  bad "bge-m3 missing — run: ollama pull bge-m3"
fi

echo "== collection =="
COLL_JSON=$(curl -sf --max-time 5 "$QDRANT/collections/$COLLECTION" 2>/dev/null || true)
if [ -n "$COLL_JSON" ] && printf '%s' "$COLL_JSON" | grep -q '"status":"green"'; then
  PTS=$(printf '%s' "$COLL_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin)["result"]["points_count"])' 2>/dev/null || echo "?")
  ok "collection '$COLLECTION' green, $PTS points"
else
  bad "collection '$COLLECTION' missing or not green"
fi

echo "== binding (must be loopback-only) =="
BIND=$(lsof -nP -iTCP:6333 -sTCP:LISTEN 2>/dev/null | awk 'NR>1{print $9}' | head -1)
case "$BIND" in
  127.0.0.1:*|"[::1]":*) ok "Qdrant bound to $BIND" ;;
  "") warn "cannot determine binding (lsof empty)" ;;
  *) bad "Qdrant bound to $BIND — EXPOSED to the network; set QDRANT__SERVICE__HOST=127.0.0.1" ;;
esac

echo "== MCP registration =="
if python3 - <<'PY' 2>/dev/null
import json,os,sys
commands=[]
try:
    import tomllib
    with open(os.path.expanduser("~/.codex/config.toml"),"rb") as f:
        commands.append(tomllib.load(f).get("mcp_servers",{}).get("ariadne",{}).get("command",""))
except Exception: pass
try:
    with open(os.path.expanduser("~/.claude.json")) as f:
        commands.append(json.load(f).get("mcpServers",{}).get("ariadne",{}).get("command",""))
except Exception: pass
configured=[cmd for cmd in commands if cmd]
sys.exit(0 if configured and all(os.path.isfile(cmd) and os.access(cmd,os.X_OK) for cmd in configured) else 1)
PY
then ok "configured Ariadne MCP commands are executable"
else bad "Ariadne MCP registration is missing or points to a broken executable"
fi

echo "== Ariadne health =="
STATUS_JSON=""
if [ -n "$CTL" ]; then STATUS_JSON="$($CTL status -json 2>/dev/null || true)"; fi
if [ -z "$STATUS_JSON" ]; then
  bad "ariadnectl status unavailable"
elif printf '%s' "$STATUS_JSON" | python3 -c 'import json,sys;sys.exit(0 if json.load(sys.stdin).get("ok") else 1)' 2>/dev/null; then
  ok "ariadnectl status is healthy"
else
  bad "ariadnectl reports degraded health"
  printf '%s' "$STATUS_JSON" | python3 -c 'import json,sys;[print("    - "+x) for x in json.load(sys.stdin).get("issues",[])]' 2>/dev/null
fi

if [ -n "$STATUS_JSON" ]; then
  COVERAGE="$(printf '%s' "$STATUS_JSON" | python3 -c 'import json,sys; t=json.load(sys.stdin).get("token_metrics",{}).get("all_time",{}); print(str(t.get("delivered_tokens",0))+" "+str(t.get("attribution_percent",0)))' 2>/dev/null || true)"
  DELIVERED="${COVERAGE%% *}"
  PERCENT="${COVERAGE#* }"
  if [ "${DELIVERED:-0}" -gt 0 ] 2>/dev/null && python3 -c "import sys;sys.exit(0 if float('${PERCENT:-0}') < 10 else 1)" 2>/dev/null; then
    warn "token attribution coverage is ${PERCENT}% (${DELIVERED} observed delivery tokens)"
  elif [ "${DELIVERED:-0}" -gt 0 ] 2>/dev/null; then
    ok "token attribution coverage is ${PERCENT}%"
  fi
fi

if [ "$(uname -s)" = "Darwin" ]; then
  echo "== launchd ownership =="
  LAUNCHD="$(launchctl list 2>/dev/null || true)"
  QDRANT_JOBS="$(printf '%s\n' "$LAUNCHD" | awk '$3=="com.ariadne.qdrant" || $3 ~ /^com[.]ariadne[.]qdrant[.]/ {print $3}')"
  QDRANT_COUNT="$(printf '%s\n' "$QDRANT_JOBS" | awk 'NF{n++} END{print n+0}')"
  if [ "$QDRANT_COUNT" -eq 1 ]; then ok "one Ariadne Qdrant job loaded"
  elif [ "$QDRANT_COUNT" -gt 1 ]; then bad "$QDRANT_COUNT Ariadne Qdrant jobs loaded"
  else warn "no Ariadne-owned Qdrant job loaded (the running service may be external)"; fi

  TRAY_LINE="$(printf '%s\n' "$LAUNCHD" | awk '$3=="com.ariadne.tray" || $3 ~ /^com[.]ariadne[.]tray[.]/ {print; n++} END{if(n>1) exit 2}')"
  TRAY_RC=$?
  if [ "$TRAY_RC" -eq 2 ]; then bad "multiple Ariadne tray jobs loaded"
  elif [ -z "$TRAY_LINE" ]; then warn "Ariadne tray job is not loaded"
  elif [ "$(printf '%s\n' "$TRAY_LINE" | awk '{print $1}')" = "-" ]; then warn "Ariadne tray job is loaded but not running"
  else ok "Ariadne tray is running"; fi

  SYNC_LINE="$(printf '%s\n' "$LAUNCHD" | awk '$3=="com.ariadne.sync" || $3 ~ /^com[.]ariadne[.]sync[.]/ {print; n++} END{if(n>1) exit 2}')"
  SYNC_RC=$?
  if [ "$SYNC_RC" -eq 2 ]; then bad "multiple Ariadne maintenance jobs loaded"
  elif [ -z "$SYNC_LINE" ]; then warn "Ariadne maintenance job is not loaded"
  else
    LAST_EXIT="$(printf '%s\n' "$SYNC_LINE" | awk '{print $2}')"
    case "$LAST_EXIT" in 0) ok "maintenance launchd job last exited successfully" ;; -) warn "maintenance launchd job has not completed yet" ;; *) bad "maintenance launchd job last exit: $LAST_EXIT" ;; esac
  fi
fi

echo "== disk =="
FREE_GB=$(df -g "$HOME_DIR" | awk 'NR==2{print $4}')
if [ "${FREE_GB:-0}" -lt 2 ]; then bad "low disk: ${FREE_GB}GB free"; else ok "${FREE_GB}GB free"; fi
DATA_MB=$(du -sm "$ARIADNE/qdrant-data" 2>/dev/null | awk '{print $1}')
BACKUP_MB=$(du -sm "$ARIADNE/backups" 2>/dev/null | awk '{print $1}')
LOG_MB=$(du -sm "$ARIADNE/logs" 2>/dev/null | awk '{print $1}')
ok "storage: data ${DATA_MB:-0}MB · backups ${BACKUP_MB:-0}MB · logs ${LOG_MB:-0}MB"
QDRANT_LOG_MB=$(du -m "$ARIADNE/logs/qdrant.log" 2>/dev/null | awk '{print $1}')
if [ "${QDRANT_LOG_MB:-0}" -gt 25 ]; then warn "qdrant.log is ${QDRANT_LOG_MB}MB; inspect repeated startup failures"; fi

echo ""
if [ "$FAIL" -ne 0 ]; then echo "DOCTOR: problems found ✗"; exit 1; fi
if [ "$WARN" -ne 0 ]; then echo "DOCTOR: services available, observability degraded !"; exit 2; fi
echo "DOCTOR: all green ✓"
exit 0
