#!/usr/bin/env python3
"""Summarise run streams in private pilot ZIPs without extracting them.

Usage: python3 tools/pilot-summary.py [--anonymize] report-data.zip ...
Optional --logs PROJECT=DIR reads local docflow run/ and stats/ evidence.
Only numeric measurements are reported; prompts and git logs are not printed.
Missing measurements stay null, with coverage counts beside each total.
"""

import argparse
from datetime import datetime
import hashlib
import json
import math
from pathlib import Path, PurePosixPath
import re
import statistics
import sys
import zipfile


def number(value):
    return (isinstance(value, (int, float)) and not isinstance(value, bool)
            and math.isfinite(value) and value >= 0)


def pi_messages(path):
    """Read completed messages once, ignoring deltas and duplicate turn events."""
    if not path.is_file():
        return None
    messages = []
    with path.open(encoding="utf-8-sig") as source:
        for line in source:
            if '"message_end"' not in line:
                continue
            try:
                event = json.loads(line)
            except ValueError:
                continue  # A killed agent can leave a truncated final line.
            if not isinstance(event, dict) or event.get("type") != "message_end":
                continue
            message = event.get("message")
            if not isinstance(message, dict):
                continue
            if message.get("role") not in ("assistant", "toolResult"):
                continue
            if not number(message.get("timestamp")):
                continue
            usage = message.get("usage")
            context = None
            if isinstance(usage, dict):
                values = [usage.get(key) for key in ("input", "cacheRead", "cacheWrite")]
                if all(number(value) for value in values):
                    context = sum(values)
            messages.append({"role": message["role"], "timestamp": message["timestamp"],
                             "call": message.get("toolCallId"),
                             "duration": message.get("durationMs"), "context": context})
    return messages


def evidence(streams, directory):
    """Associate Pi messages with outcome windows; never infer missing gate time."""
    outcomes, contexts, windows, cache = [], {}, {}, {}
    for events in streams:
        active = {}
        for event in events:
            unit = event.get("unit")
            kind = event["event"]
            if kind == "unit_start" and unit:
                active[unit] = {"start": event.get("ts"), "attempts": [], "gates": []}
            state = active.get(unit)
            if state is not None and kind == "attempt" and not event.get("replan") and not event.get("close"):
                state["attempts"].append(event)
            if state is not None and kind == "verify":
                state["gates"].append(event)
            if kind != "unit_end" or not unit:
                continue
            state = active.pop(unit, None)
            row = {"unit": unit, "model_seconds": None, "tool_seconds": None,
                   "gate_seconds": None, "model_calls": 0, "timed_model_calls": 0,
                   "tool_calls": 0, "timed_tool_calls": 0,
                   "logs": 0, "pi_logs": 0, "expected_logs": 0}
            outcomes.append(row)
            if state is None or not number(state["start"]) or not number(event.get("ts")):
                continue
            start, end = state["start"], event["ts"]
            if end < start:
                continue
            windows.setdefault(unit, []).append((start, end))
            seen = set()
            for attempt in state["attempts"]:
                row["expected_logs"] += 1
                route = attempt.get("route", {})
                if directory is None or not isinstance(attempt.get("log"), str):
                    continue
                # Event paths can come from another OS; only the basename is used.
                path = directory / "run" / PurePosixPath(attempt["log"].replace("\\", "/")).name
                if not path.is_file():
                    continue
                row["logs"] += 1
                if route.get("agent") != "pi":
                    continue
                if path not in cache:
                    cache[path] = pi_messages(path)
                messages = cache[path]
                if messages is None:
                    continue
                row["pi_logs"] += 1
                for message in messages:
                    if not start <= message["timestamp"] / 1000 <= end:
                        continue  # Resume logs may contain earlier session history.
                    key = (message["role"], message["timestamp"], message["call"])
                    if key in seen:
                        continue
                    seen.add(key)
                    role = "model" if message["role"] == "assistant" else "tool"
                    row[role + "_calls"] += 1
                    if number(message["duration"]):
                        row["timed_" + role + "_calls"] += 1
                        field = role + "_seconds"
                        row[field] = (row[field] or 0) + message["duration"] / 1000
                    if role == "model" and number(message["context"]):
                        contexts.setdefault(unit, {})[key] = message
            # Reused verify events do not measure the agent's earlier gate.
            gates = state["gates"]
            if gates and all(g.get("reused", False) is False and number(g.get("seconds")) for g in gates):
                row["gate_seconds"] = sum(g["seconds"] for g in gates)

    pairs = []
    for unit, messages in sorted(contexts.items()):
        if directory is None or not re.fullmatch(r"P[0-9]+\.[0-9]+\.[0-9]+", unit):
            continue
        path = directory / "stats" / (unit + ".json")
        if not path.is_file():
            continue
        record = json.loads(path.read_text(encoding="utf-8-sig"))
        if not isinstance(record, dict) or record.get("unit") != unit:
            continue
        proxy = record.get("proxy_tokens")
        try:
            stamp = datetime.fromisoformat(record.get("prompted_at", "").replace("Z", "+00:00"))
            if stamp.tzinfo is None:
                continue
            stamp = stamp.timestamp()
        except (TypeError, ValueError, AttributeError):
            continue
        if not number(proxy) or proxy == 0 or not any(a - 2 <= stamp <= b for a, b in windows[unit]):
            continue  # A later rerun's stats must not be paired with old logs.
        values = [m["context"] for m in sorted(messages.values(), key=lambda m: m["timestamp"])]
        pairs.append({"unit": unit, "proxy": proxy, "requests": len(values),
                      "base_ctx": values[0], "peak_ctx": max(values), "sum_ctx": sum(values),
                      "peak_proxy": max(values) / proxy,
                      "grow_proxy": (max(values) - values[0]) / proxy})

    def total(field):
        values = [row[field] for row in outcomes if number(row[field])]
        return {"total": sum(values) if values else None,
                "measured_ends": len(values), "all_ends": len(outcomes)}

    return {"model_seconds": total("model_seconds"), "tool_seconds": total("tool_seconds"),
            "gate_seconds": total("gate_seconds"), "outcomes": outcomes,
            "context": {"paired_units": len(pairs), "all_units": len({r["unit"] for r in outcomes}),
                        "median_peak_proxy": statistics.median(p["peak_proxy"] for p in pairs) if pairs else None,
                        "median_grow_proxy": statistics.median(p["grow_proxy"] for p in pairs) if pairs else None,
                        "units": pairs}}


def summarise(paths, anonymize=False, logs=None):
    projects, seen = {}, {}

    def project(name):
        return projects.setdefault(name, {"streams": 0, "events": [], "runs": []})

    for path in paths:
        with zipfile.ZipFile(path) as archive:
            for name in sorted(archive.namelist()):
                parts = PurePosixPath(name).parts
                if name.endswith(".gitlog.txt"):
                    project(PurePosixPath(name).name[:-len(".gitlog.txt")])
                if not name.endswith(".events.jsonl"):
                    continue
                if len(parts) < 3 or parts[-2] != "run":
                    raise ValueError("unrecognised stream layout: " + name)
                key = (parts[-3], parts[-1])
                data = archive.read(name)
                digest = hashlib.sha256(data).digest()
                if key in seen:
                    if seen[key] != digest:
                        raise ValueError("conflicting copies of stream: " + name)
                    continue
                seen[key] = digest
                row = project(key[0])
                row["streams"] += 1
                events = []
                for line_no, line in enumerate(data.decode("utf-8-sig").splitlines(), 1):
                    if not line.strip():
                        continue
                    event = json.loads(line)
                    if not isinstance(event, dict) or not isinstance(event.get("event"), str):
                        raise ValueError("invalid event: %s:%d" % (name, line_no))
                    row["events"].append(event)
                    events.append(event)
                row["runs"].append(events)

    logs = logs or {}
    if set(logs) - set(projects):
        raise ValueError("--logs project is not present in the archives")

    result = []
    for index, (name, row) in enumerate(sorted(projects.items()), 1):
        events = row["events"]
        ends = [e for e in events if e["event"] == "unit_end" and e.get("unit")]
        verifies = [e for e in events if e["event"] == "verify" and e.get("unit")]
        units = {e["unit"] for e in ends}

        def measurement(key):
            values = [e[key] for e in ends if isinstance(e.get(key), (int, float))
                      and not isinstance(e[key], bool)]
            return {"total": sum(values) if values else None,
                    "measured_ends": len(values), "all_ends": len(ends)}

        result.append({
            "project": "project-%d" % index if anonymize else name,
            "streams": row["streams"],
            "units": len(units) if ends else None,
            "done_units": len({e["unit"] for e in ends if e.get("status") == "done"}) if ends else None,
            "unit_ends": {status: sum(e.get("status") == status for e in ends)
                          for status in ("done", "blocked", "failed", "error") } if ends else None,
            "attempts": sum(e["event"] == "attempt" and bool(e.get("unit"))
                            and not e.get("replan") and not e.get("close") for e in events) if ends else None,
            "replans": sum(e["event"] == "replan" for e in events) if events else None,
            "route_switch_events": sum(e["event"] == "route_switch" for e in events) if events else None,
            "verifies": len(verifies) if events else None,
            "verify_failures": sum(e.get("exit_code", 0) != 0 for e in verifies) if verifies else None,
            "verify_reused": sum(e.get("reused") is True for e in verifies) if verifies else None,
            "seconds": measurement("seconds"),
            "cost": measurement("cost"),
            "test_changes": measurement("test_changes"),
            "unverified": measurement("unverified"),
            "evidence": evidence(row["runs"], Path(logs[name]) if name in logs else None),
        })
    return {"projects": result}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--anonymize", action="store_true", help="replace project names with numbered labels")
    parser.add_argument("--logs", action="append", default=[], metavar="PROJECT=DIR",
                        help="local docflow directory containing run/ and stats/ (repeatable)")
    parser.add_argument("archives", nargs="+", help="pilot ZIP archives")
    args = parser.parse_args()
    try:
        logs = {}
        for value in args.logs:
            name, separator, directory = value.partition("=")
            if not separator or not name or not directory or not Path(directory).is_dir() or name in logs:
                raise ValueError("--logs requires a unique PROJECT and an existing DIR")
            logs[name] = directory
        report = summarise(args.archives, args.anonymize, logs)
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        parser.exit(1, "pilot-summary: %s\n" % error)
    json.dump(report, sys.stdout, ensure_ascii=False, indent=2)
    print()


if __name__ == "__main__":
    main()
