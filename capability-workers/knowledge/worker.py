#!/usr/bin/env python3
"""Reference capability worker.

The worker protocol is newline-delimited JSON: one request in, one result out.
It is deliberately dependency-free so it can be run in a container or a
remote worker. Production capabilities should validate the same envelope and
apply their own permission policy before touching a resource.
"""

from __future__ import annotations

import hashlib
import json
import sys
from datetime import datetime, timezone


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def handle(request: dict) -> dict:
    inputs = request.get("inputs") or {}
    query = str(inputs.get("query", "")).strip()
    corpus = inputs.get("corpus") or []
    matches = [
        {"index": index, "value": str(value)}
        for index, value in enumerate(corpus)
        if not query or query.lower() in str(value).lower()
    ]
    digest = hashlib.sha256(json.dumps(matches, sort_keys=True).encode()).hexdigest()
    evidence = {
        "evidence_id": "E-worker-" + digest[:12],
        "finding_id": inputs.get("finding_id", ""),
        "type": "knowledge_search",
        "source": {
            "agent_id": request.get("agent_id", "worker"),
            "task_id": request.get("task_id", ""),
            "capability_id": request.get("capability_id", "knowledge.search"),
        },
        "confidence": 0.5 if matches else 0.1,
        "content": {"query": query, "matches": matches, "generated_at": utc_now()},
    }
    return {
        "request_id": request.get("request_id", ""),
        "capability_id": request.get("capability_id", "knowledge.search"),
        "status": "completed",
        "summary": f"Found {len(matches)} matching knowledge items",
        "evidence": [evidence],
        "confidence": evidence["confidence"],
        "metrics": {"matches": len(matches)},
    }


def main() -> int:
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            print(json.dumps(handle(json.loads(line)), ensure_ascii=True), flush=True)
        except Exception as exc:  # keep the worker protocol alive for the next request
            print(json.dumps({"status": "failed", "error": str(exc)}), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
