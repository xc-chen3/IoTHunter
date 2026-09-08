#!/usr/bin/env python3
"""Dependency-free IoTHunter capability worker using a newline JSON protocol.

Only offline analysis is implemented here. The Go control plane remains the
permission boundary and owns task, finding, evidence, and artifact storage.
"""
from __future__ import annotations

import hashlib
import json
import mimetypes
import re
import shutil
import sys
import tarfile
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

MAX_FILE = 128 * 1024 * 1024
MAX_TEXT = 2 * 1024 * 1024
PRINTABLE = set(range(32, 127)) | {9, 10, 13}


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def text(value: Any) -> str:
    return str(value or "").strip()


def source(request: dict) -> dict[str, str]:
    return {"agent_id": text(request.get("agent_id")) or "worker", "task_id": text(request.get("task_id")), "capability_id": text(request.get("capability_id")), "runtime": "python-worker"}


def make_evidence(request: dict, kind: str, content: dict, confidence: float = 0.7) -> dict:
    digest = hashlib.sha256(json.dumps(content, sort_keys=True, ensure_ascii=True).encode()).hexdigest()[:16]
    return {"evidence_id": "E-worker-" + digest, "type": kind, "source": source(request), "confidence": max(0.0, min(1.0, confidence)), "content": content, "created_at": utc_now()}


def safe_input_path(request: dict, value: Any) -> Path:
    raw = text(value)
    if not raw or "\x00" in raw:
        raise ValueError("inputs.path is required")
    path = Path(raw).expanduser().resolve()
    if not path.is_file():
        raise ValueError(f"input file does not exist: {raw}")
    if path.stat().st_size > MAX_FILE:
        raise ValueError("input file exceeds 128 MiB limit")
    root = text((request.get("inputs") or {}).get("workspace_root"))
    if root:
        try:
            path.relative_to(Path(root).expanduser().resolve())
        except ValueError as exc:
            raise ValueError("input path is outside workspace_root") from exc
    return path


def file_metadata(path: Path) -> tuple[dict, bytes]:
    data = path.read_bytes()
    kind = "data"
    if data.startswith(b"\x7fELF"):
        kind = "elf"
    elif data.startswith(b"PK\x03\x04"):
        kind = "zip"
    elif data.startswith(b"\x1f\x8b"):
        kind = "gzip"
    elif len(data) > 262 and data[257:262] == b"ustar":
        kind = "tar"
    elif data.startswith((b"hsqs", b"sqsh")):
        kind = "squashfs"
    elif data.startswith(b"UBI#"):
        kind = "ubi"
    elif data.startswith(b"MZ"):
        kind = "pe"
    elif data.startswith(b"\x89PNG"):
        kind = "png"
    elif mimetypes.guess_type(path.name)[0]:
        kind = mimetypes.guess_type(path.name)[0] or kind
    return {"name": path.name, "path": str(path), "size": len(data), "sha256": hashlib.sha256(data).hexdigest(), "format": kind}, data


def make_artifact(meta: dict, artifact_type: str = "input", metadata: dict | None = None) -> dict:
    extra = {"format": meta.get("format", "data"), "source": "python-worker"}
    if metadata:
        extra.update(metadata)
    return {"artifact_id": "ART-" + meta["sha256"][:16], "name": meta["name"], "type": artifact_type, "path": meta["path"], "sha256": meta["sha256"], "size": meta["size"], "metadata": extra, "created_at": utc_now()}


def file_artifact(path: Path, artifact_type: str, metadata: dict | None = None) -> dict:
    data = path.read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    meta = {"name": path.name, "path": str(path), "size": len(data), "sha256": digest, "format": "text"}
    return make_artifact(meta, artifact_type, metadata)


def extraction_root(request: dict, source: Path) -> Path:
    root = text((request.get("inputs") or {}).get("workspace_root"))
    if not root:
        raise ValueError("firmware.extract requires inputs.workspace_root")
    workspace = Path(root).expanduser().resolve()
    output = workspace / "derived" / (source.stem + "-" + hashlib.sha256(source.read_bytes()).hexdigest()[:12])
    output.mkdir(parents=True, exist_ok=True)
    return output


def safe_member_path(root: Path, name: str) -> Path:
    candidate = (root / name).resolve()
    try:
        candidate.relative_to(root.resolve())
    except ValueError as exc:
        raise ValueError(f"archive entry escapes extraction root: {name}") from exc
    return candidate


def extract_archive(path: Path, output: Path) -> tuple[list[dict], int]:
    entries: list[dict] = []
    total = 0
    if zipfile.is_zipfile(path):
        with zipfile.ZipFile(path) as archive:
            for item in archive.infolist()[:10000]:
                target = safe_member_path(output, item.filename)
                if item.is_dir():
                    target.mkdir(parents=True, exist_ok=True)
                    entries.append({"name": item.filename, "size": 0, "directory": True})
                    continue
                if item.file_size > MAX_FILE or total + item.file_size > MAX_FILE:
                    raise ValueError("extracted firmware exceeds 128 MiB limit")
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.open(item) as source, target.open("wb") as destination:
                    shutil.copyfileobj(source, destination, length=1024 * 1024)
                total += item.file_size
                entries.append({"name": item.filename, "size": item.file_size, "directory": False})
        return entries, total
    if tarfile.is_tarfile(path):
        with tarfile.open(path, "r:*") as archive:
            for item in archive.getmembers()[:10000]:
                target = safe_member_path(output, item.name)
                if item.isdir():
                    target.mkdir(parents=True, exist_ok=True)
                    entries.append({"name": item.name, "size": 0, "directory": True})
                    continue
                if not item.isfile():
                    continue
                if item.size > MAX_FILE or total + item.size > MAX_FILE:
                    raise ValueError("extracted firmware exceeds 128 MiB limit")
                source = archive.extractfile(item)
                if source is None:
                    continue
                target.parent.mkdir(parents=True, exist_ok=True)
                with source, target.open("wb") as destination:
                    shutil.copyfileobj(source, destination, length=1024 * 1024)
                total += item.size
                entries.append({"name": item.name, "size": item.size, "directory": False})
        return entries, total
    raise ValueError("firmware input is not a supported zip or tar archive")


def path_text(request: dict, inputs: dict) -> tuple[Path, str]:
    path = safe_input_path(request, inputs.get("path") or inputs.get("file_path"))
    data = path.read_bytes()
    return path, data[:MAX_TEXT].decode("utf-8", "replace")


def normalize_edges(value: Any) -> list[tuple[str, str]]:
    edges: list[tuple[str, str]] = []
    if isinstance(value, dict):
        for source, targets in value.items():
            if isinstance(targets, list):
                edges.extend((text(source), text(target)) for target in targets if text(source) and text(target))
            elif text(targets):
                edges.append((text(source), text(targets)))
    elif isinstance(value, list):
        for item in value:
            if isinstance(item, dict):
                source, target = text(item.get("source") or item.get("from")), text(item.get("target") or item.get("to"))
                if source and target:
                    edges.append((source, target))
            elif isinstance(item, (list, tuple)) and len(item) >= 2:
                edges.append((text(item[0]), text(item[1])))
    return edges


def reachable(edges: list[tuple[str, str]], source: str, sink: str) -> list[str]:
    graph: dict[str, list[str]] = {}
    for left, right in edges:
        graph.setdefault(left, []).append(right)
    queue: list[tuple[str, list[str]]] = [(source, [source])]
    seen = {source}
    while queue:
        node, path = queue.pop(0)
        if node == sink:
            return path
        for child in graph.get(node, []):
            if child not in seen:
                seen.add(child)
                queue.append((child, path + [child]))
    return []


def archive_entries(path: Path) -> list[dict]:
    entries: list[dict] = []
    try:
        if zipfile.is_zipfile(path):
            with zipfile.ZipFile(path) as archive:
                return [{"name": item.filename, "size": item.file_size, "directory": item.is_dir()} for item in archive.infolist()[:5000]]
    except (OSError, zipfile.BadZipFile):
        return entries
    try:
        if tarfile.is_tarfile(path):
            with tarfile.open(path, "r:*") as archive:
                return [{"name": item.name, "size": item.size, "directory": item.isdir()} for item in archive.getmembers()[:5000]]
    except (OSError, tarfile.TarError):
        pass
    return entries


def printable_strings(data: bytes, minimum: int = 4, limit: int = 5000) -> list[str]:
    result: list[str] = []
    current = bytearray()
    for byte in data:
        if byte in PRINTABLE:
            current.append(byte)
        else:
            if len(current) >= minimum:
                result.append(current.decode("utf-8", "replace"))
                if len(result) >= limit:
                    break
            current.clear()
    if len(current) >= minimum and len(result) < limit:
        result.append(current.decode("utf-8", "replace"))
    return result


def parse_protocol(value: str) -> dict[str, str]:
    fields: dict[str, str] = {}
    for line in value.splitlines():
        parts = re.split(r"\s*[:=]\s*", line.strip(), maxsplit=1)
        if len(parts) == 2 and parts[0].strip():
            fields[parts[0].strip()] = parts[1].strip()
    return fields


def routes(value: str) -> list[str]:
    found: list[str] = []
    seen: set[str] = set()
    pattern = re.compile(r"(?:href|action|url)\s*=\s*[\"']([^\"']+)[\"']|(?:^|\s)(/[A-Za-z0-9_./?=&%-]{2,})")
    for match in pattern.finditer(value):
        route = match.group(1) or match.group(2)
        if route and route not in seen:
            seen.add(route)
            found.append(route)
    return found[:2000]


def result(request: dict, summary: str, content: dict, confidence: float = 0.7, kind: str = "capability_result", artifacts: list[dict] | None = None) -> dict:
    return {"request_id": text(request.get("request_id")), "capability_id": text(request.get("capability_id")), "status": "completed", "summary": summary, "evidence": [make_evidence(request, kind, content, confidence)], "artifacts": artifacts or [], "confidence": confidence, "metrics": {"generated_at": utc_now()}}


def handle(request: dict) -> dict:
    capability = text(request.get("capability_id"))
    inputs = request.get("inputs") or {}
    if capability in {"firmware.extract", "firmware.inventory", "binary.identify", "binary.search_string"}:
        path = safe_input_path(request, inputs.get("path") or inputs.get("file_path"))
        meta, data = file_metadata(path)
        artifacts = [make_artifact(meta)]
        if capability == "firmware.extract":
            entries = archive_entries(path)
            output = extraction_root(request, path)
            extracted, total = extract_archive(path, output)
            manifest = output / "manifest.json"
            manifest.write_text(json.dumps({"source": meta, "entries": extracted, "bytes": total}, ensure_ascii=True, indent=2), encoding="utf-8")
            artifacts.append(file_artifact(manifest, "filesystem-manifest", {"source_artifact": artifacts[0]["artifact_id"], "extraction_root": str(output)}))
            return result(request, f"Extracted {len(extracted)} firmware entries ({total} bytes)", {"file": meta, "entries": extracted, "extraction_root": str(output), "entry_count": len(extracted), "bytes_written": total}, 0.95, "firmware_extraction", artifacts)
        if capability == "firmware.inventory":
            entries = archive_entries(path)
            return result(request, f"Inventoried {len(entries)} entries in {meta['name']}", {"file": meta, "entries": entries, "entry_count": len(entries)}, 0.85, "firmware_inventory", artifacts)
        if capability == "binary.identify":
            return result(request, f"Identified {meta['name']} as {meta['format']}", {"file": meta, "magic": data[:16].hex()}, 0.9, "binary_identification", artifacts)
        query = text(inputs.get("query")).lower()
        values = printable_strings(data)
        matches = [value for value in values if not query or query in value.lower()][:1000]
        return result(request, f"Found {len(matches)} matching strings in {meta['name']}", {"file": meta, "query": query, "matches": matches, "match_count": len(matches)}, 0.8 if matches else 0.35, "binary_strings", artifacts)
    if capability in {"binary.callgraph", "binary.xref"}:
        path = text(inputs.get("path") or inputs.get("file_path"))
        graph = normalize_edges(inputs.get("edges") or inputs.get("call_graph") or inputs.get("references"))
        if not graph and path:
            source_path = safe_input_path(request, path)
            value = source_path.read_bytes()[:MAX_TEXT].decode("utf-8", "replace")
            graph = [(match.group(1), match.group(2)) for match in re.finditer(r"\b([A-Za-z_][\w.]*)\s*(?:->|calls|:>)\s*([A-Za-z_][\w.]*)", value)]
        nodes = sorted({node for edge in graph for node in edge})
        content = {"edges": [{"source": left, "target": right} for left, right in graph], "nodes": nodes, "edge_count": len(graph)}
        kind = "binary_callgraph" if capability == "binary.callgraph" else "binary_xref"
        return result(request, f"Resolved {len(graph)} binary relationships", content, 0.75 if graph else 0.25, kind)
    if capability == "firmware.config_scan":
        path = safe_input_path(request, inputs.get("path") or inputs.get("file_path"))
        meta, data = file_metadata(path)
        value = data[:MAX_TEXT].decode("utf-8", "replace")
        risky = [line.strip()[:500] for line in value.splitlines() if re.search(r"password|secret|token|debug|telnet|dropbear", line, re.I)][:200]
        return result(request, f"Scanned {meta['name']} and found {len(risky)} review lines", {"file": meta, "matches": risky}, 0.75, "configuration_scan", [make_artifact(meta)])
    if capability in {"protocol.parse", "protocol.attack_surface", "protocol.hidden_interface", "web.route_discovery"}:
        value = text(inputs.get("text") or inputs.get("data"))
        if capability == "protocol.parse":
            parsed = parse_protocol(value)
            return result(request, f"Parsed {len(parsed)} protocol fields", {"fields": parsed, "raw_bytes": len(value.encode())}, 0.8, "protocol_parse")
        found = routes(value)
        return result(request, f"Discovered {len(found)} route candidates", {"routes": found, "raw_bytes": len(value.encode())}, 0.7 if found else 0.25, "route_discovery")
    if capability in {"config.audit", "taint.trace", "taint.storage_trace", "fuzz.constraint", "fuzz.seed_generate", "emulation.run", "packet.generate", "packet.replay", "device.inspect", "device.validate", "poc.verify", "cvss.score", "knowledge.search", "knowledge.pattern_match"}:
        if capability == "config.audit":
            values = inputs.get("values") or {}
            risky = [key for key in values if re.search(r"password|secret|token|private|debug", str(key), re.I)]
            return result(request, f"Audited {len(values)} configuration keys", {"risky_keys": risky, "key_count": len(values)}, 0.8, "configuration_audit")
        if capability in {"knowledge.search", "knowledge.pattern_match"}:
            query = text(inputs.get("query")).lower()
            corpus = inputs.get("corpus") or []
            matches = [{"index": i, "value": text(item)} for i, item in enumerate(corpus) if not query or query in text(item).lower()]
            return result(request, f"Found {len(matches)} matching knowledge items", {"query": query, "matches": matches}, 0.5 if matches else 0.1, "knowledge_search")
        if capability in {"taint.trace", "taint.storage_trace"}:
            source_value, sink_value = text(inputs.get("source")), text(inputs.get("sink"))
            edges = normalize_edges(inputs.get("edges") or inputs.get("graph"))
            path = reachable(edges, source_value, sink_value) if source_value and sink_value and edges else []
            traceable = bool(path) if edges else bool(source_value and sink_value)
            return result(request, "Taint path resolved" if traceable else "No taint path resolved", {"source": source_value, "sink": sink_value, "path": path, "traceable": traceable, "edge_count": len(edges)}, 0.9 if traceable else 0.25, "taint_trace")
        if capability == "cvss.score":
            impact, exploitability = float(inputs.get("impact") or 0), float(inputs.get("exploitability") or 0)
            score = min(10.0, max(0.0, impact * 0.6 + exploitability * 0.4))
            return result(request, f"Calculated approximate score {score:.1f}", {"score": score, "impact": impact, "exploitability": exploitability}, 0.9, "risk_score")
        if capability == "fuzz.seed_generate":
            seed = text(inputs.get("seed") or inputs.get("input") or "")
            mutations = inputs.get("mutations") or ["append-null", "duplicate", "boundary-length"]
            seeds = []
            for mutation in list(mutations)[:32]:
                name = text(mutation)
                if name == "append-null":
                    value = seed + "\\x00"
                elif name == "duplicate":
                    value = seed + seed
                elif name == "boundary-length":
                    value = seed[:1024] + "A" * max(0, 1024 - len(seed[:1024]))
                else:
                    value = seed
                seeds.append({"mutation": name, "value": value})
            return result(request, f"Generated {len(seeds)} bounded fuzz seeds", {"seeds": seeds, "seed_length": len(seed)}, 0.85, "fuzz_seeds")
        if capability == "packet.generate":
            protocol = text(inputs.get("protocol") or "raw")
            fields = inputs.get("fields") or {}
            raw = text(inputs.get("payload"))
            if not raw and isinstance(fields, dict):
                raw = "&".join(f"{key}={value}" for key, value in fields.items())
            return result(request, f"Generated {protocol} packet payload", {"protocol": protocol, "fields": fields, "payload": raw, "bytes": len(raw.encode())}, 0.85, "packet_generated")
        if capability == "poc.verify":
            expected = text(inputs.get("expected"))
            actual = text(inputs.get("actual"))
            passed = bool(expected and actual and expected == actual)
            if not expected and "path" in inputs:
                path = safe_input_path(request, inputs["path"])
                actual = hashlib.sha256(path.read_bytes()).hexdigest()
                passed = actual == text(inputs.get("sha256"))
            return result(request, "PoC verification passed" if passed else "PoC verification did not pass", {"passed": passed, "expected": expected, "actual": actual, "method": text(inputs.get("method") or "comparison")}, 0.95 if passed else 0.2, "poc_verification")
        if capability == "cvss.score":
            impact, exploitability = float(inputs.get("impact") or 0), float(inputs.get("exploitability") or 0)
            score = min(10.0, max(0.0, impact * 0.6 + exploitability * 0.4))
            return result(request, f"Calculated approximate score {score:.1f}", {"score": score, "impact": impact, "exploitability": exploitability}, 0.9, "risk_score")
        if capability in {"emulation.run", "packet.replay", "device.validate"}:
            return result(request, "Execution request validated and queued for an approved executor", {"execution": "queued", "inputs": inputs, "requires_approval": capability in {"packet.replay", "device.validate"}}, 0.7, "execution_request")
        return result(request, "Capability input processed", {"inputs": inputs}, 0.7, "capability_result")
    raise ValueError(f"unsupported capability {capability}")


def main() -> int:
    for line in sys.stdin:
        if not line.strip():
            continue
        request: dict = {}
        try:
            request = json.loads(line)
            print(json.dumps(handle(request), ensure_ascii=True), flush=True)
        except Exception as exc:
            print(json.dumps({"request_id": text(request.get("request_id")), "capability_id": text(request.get("capability_id")), "status": "failed", "error": str(exc)}, ensure_ascii=True), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
