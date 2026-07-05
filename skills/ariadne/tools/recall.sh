#!/bin/bash
# recall.sh "query" [limit] — CLI memory recall without MCP (dense-only).
# Embeds the query via Ollama, searches Qdrant REST. For quick checks & hooks.
set -euo pipefail

QUERY="${1:?usage: recall.sh \"query\" [limit]}"
LIMIT="${2:-5}"
COLLECTION="${ARIADNE_COLLECTION:-ariadne}"
OLLAMA="${ARIADNE_OLLAMA:-http://localhost:11434}"
QDRANT="${ARIADNE_QDRANT_REST:-http://localhost:6333}"

python3 - "$QUERY" "$LIMIT" "$COLLECTION" "$OLLAMA" "$QDRANT" <<'PY'
import json, sys, urllib.request

query, limit, coll, ollama, qdrant = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]

def post(url, payload):
    req = urllib.request.Request(url, json.dumps(payload).encode(),
                                 {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)

emb = post(f"{ollama}/api/embed", {"model": "bge-m3", "input": query})["embeddings"][0]
res = post(f"{qdrant}/collections/{coll}/points/query",
           {"query": emb, "using": "dense", "limit": limit, "with_payload": True})

for i, p in enumerate(res["result"]["points"], 1):
    pl = p.get("payload", {})
    loc = "/".join(x for x in (pl.get("wing", ""), pl.get("room", "")) if x)
    text = (pl.get("text", "") or "").replace("\n", " ")
    print(f"[{i}] {p['score']:.3f} {loc}\n    {text[:220]}\n")
PY
