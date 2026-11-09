#!/usr/bin/env python3
"""Summarise run streams in private pilot ZIPs without extracting them.

Usage: python3 tools/pilot-summary.py [--anonymize] report-data.zip ...
Only event streams and project names are read; git logs are not printed.
Missing measurements stay null, with coverage counts beside each total.
"""

import argparse
import hashlib
import json
from pathlib import PurePosixPath
import sys
import zipfile


def summarise(paths, anonymize=False):
    projects, seen = {}, {}

    def project(name):
        return projects.setdefault(name, {"streams": 0, "events": []})

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
                for line_no, line in enumerate(data.decode("utf-8-sig").splitlines(), 1):
                    if not line.strip():
                        continue
                    event = json.loads(line)
                    if not isinstance(event, dict) or not isinstance(event.get("event"), str):
                        raise ValueError("invalid event: %s:%d" % (name, line_no))
                    row["events"].append(event)

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
        })
    return {"projects": result}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--anonymize", action="store_true", help="replace project names with numbered labels")
    parser.add_argument("archives", nargs="+", help="pilot ZIP archives")
    args = parser.parse_args()
    try:
        report = summarise(args.archives, args.anonymize)
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        parser.exit(1, "pilot-summary: %s\n" % error)
    json.dump(report, sys.stdout, ensure_ascii=False, indent=2)
    print()


if __name__ == "__main__":
    main()
