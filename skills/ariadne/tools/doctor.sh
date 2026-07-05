#!/bin/bash
# doctor.sh — full-chain health check for the Ariadne stack.
# Exits 0 when everything is green, 1 when anything critical is broken.
set -u

HOME_DIR="${HOME}"
ARIADNE="${HOME_DIR}/.ariadne"
COLLECTION="${ARIADNE_COLLECTION:-ariadne}"
QDRANT="http://localhost:6333"
OLLAMA="http://localhost:11434"
FAIL=0

ok()   { printf "  ✓ %s\n" "$1"; }
bad()  { printf "  ✗ %s\n" "$1"; FAIL=1; }
warn() { printf "  ! %s\n" "$1"; }

echo "== binaries =="
for b in ariadne ariadnectl qdrant; do
  if [ -x "$ARIADNE/bin/$b" ]; then ok "$ARIADNE/bin/$b"; else bad "missing: $ARIADNE/bin/$b"; fi
done

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
d=json.load(open(os.path.expanduser("~/.claude.json")))
cmd=d.get("mcpServers",{}).get("ariadne",{}).get("command","")
sys.exit(0 if os.path.isfile(cmd) and os.access(cmd,os.X_OK) else 1)
PY
then ok "mcpServers.ariadne registered and executable"
else bad "mcpServers.ariadne missing/broken in ~/.claude.json"
fi

echo "== disk =="
FREE_GB=$(df -g "$HOME_DIR" | awk 'NR==2{print $4}')
if [ "${FREE_GB:-0}" -lt 2 ]; then bad "low disk: ${FREE_GB}GB free"; else ok "${FREE_GB}GB free"; fi

echo ""
if [ "$FAIL" -eq 0 ]; then echo "DOCTOR: all green ✓"; else echo "DOCTOR: problems found ✗"; fi
exit $FAIL
