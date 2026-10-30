#!/usr/bin/env python3
"""Machine verification of the evidence kept by tools/smoke-opencode.sh.

Usage:
  smoke-opencode-check.py main <evidence-dir>
  smoke-opencode-check.py no-such-model <evidence-dir>

Reads what the smoke kept — the throwaway repository, the runner's output
and exit code, the copied events.jsonl and the wrapper's raw attempt
streams — asserts the story of the run and writes summary.tsv with the
time and cost of every unit. The summary is written before the
assertions, so it exists for failed runs too; a missing mandatory
artifact or a failed assertion exits non-zero.

main asserts, for the two smoke units:
- every attempt's route in events.jsonl matches the unit's Executor line
  in the final plan and the route line in the runner output — the first
  unit only opencode low, the second only opencode high, no fallback
  switch anywhere;
- both units done, exactly the two unit commits of the plan;
- one controlled Verify failure in the first unit (verify codes 4 then 0),
  one passing verify in the second;
- the wrapped calls: the second call of the first unit resumes the first
  stream's session via --session and the continuation stream carries the
  same sessionID — a fresh call, a new session or an extra attempt fails;
- the Handoff of each unit in the final plan matches the result JSON of
  its successful attempt (the header alone is not evidence).

no-such-model asserts the isolated refusal scenario: one attempt against
the nonexistent model, refused by the provider error event, exit 7, the
unit stays [~] with no done, no verify and no commit.

Test streams from tools/test-smoke-opencode.sh pass through this checker
too; they stay in the test sandbox and are never presented as live
fixtures.
"""

import json
import os
import re
import subprocess
import sys


def err(message):
    sys.exit("smoke-opencode-check: " + message)


def must_file(path):
    if not os.path.isfile(path):
        err("missing mandatory artifact: " + path)
    return path


def read(path):
    with open(must_file(path), encoding="utf-8") as f:
        return f.read()


def norm(text):
    return " ".join(text.split())


BLOCK = re.compile(r"```json[ \t]*\n(.*?)\n[ \t]*```", re.S)
UNIT = re.compile(r"^### \[(.)\] (\S+) (.+?)\s*$")
ROUTE_LINE = re.compile(r"^(\S+) (.+?) -> (.+?) \| (.+?) \| (.+?)\s*$")
CALL_FILE = re.compile(r"^call-(\d+)\.(argv|stdout|exit)$")


def parse_stream(text):
    """Fold an `opencode run --format json` stream the way the runner's
    decoder does: the last sessionID wins, text folds per messageID with
    the last message winning, and an error event decides is_error — the
    exit code is never a signal."""
    session = None
    message, parts, folded = None, [], ""
    out = {"is_error": False, "error_type": None, "error_message": None}
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except ValueError:
            err("non-JSON line in an attempt stream: " + line[:120])
        if obj.get("sessionID"):
            session = obj["sessionID"]
        kind = obj.get("type")
        part = obj.get("part") if isinstance(obj.get("part"), dict) else {}
        if kind == "text":
            if part.get("messageID") != message:
                message, parts = part.get("messageID"), []
            parts.append(part.get("text") or "")
            folded = "".join(parts)
        elif kind == "error":
            out["is_error"] = True
            detail = obj.get("error")
            if isinstance(detail, dict):
                out["error_type"] = detail.get("type")
                out["error_message"] = detail.get("message")
            else:
                out["error_message"] = detail
    out["session"] = session
    out["text"] = folded
    return out


def result_json(stream):
    """The result the runner would read: the last ```json block of the
    folded assistant text."""
    for block in reversed(BLOCK.findall(stream["text"])):
        try:
            obj = json.loads(block)
        except ValueError:
            continue
        if isinstance(obj, dict):
            return obj
    err("no result JSON block in the successful attempt stream")


def parse_plan(path):
    units, unit, field = [], None, None
    for line in read(path).splitlines():
        m = UNIT.match(line)
        if m:
            unit = {"status": m.group(1), "id": m.group(2), "title": m.group(3),
                    "executor": None, "commit": None, "handoff": []}
            units.append(unit)
            field = None
            continue
        if unit is None:
            continue
        if line.startswith("## "):
            unit, field = None, None
            continue
        if line.startswith("Executor:"):
            unit["executor"] = [p.strip() for p in line[len("Executor:"):].split("|")]
            field = None
        elif line.startswith("Commit:"):
            unit["commit"] = line[len("Commit:"):].strip()
            field = None
        elif line.strip() == "Handoff:":
            field = "handoff"
        elif field == "handoff":
            if line.startswith("- "):
                unit["handoff"].append(line[2:])
            else:
                field = None
    return units


def output_routes(path):
    """Route lines of the runner output: uid -> (title, agent, model, effort)."""
    routes = {}
    for line in read(path).splitlines():
        m = ROUTE_LINE.match(line)
        if m:
            routes.setdefault(m.group(1), []).append(m.groups()[1:])
    return routes


def want_route(unit):
    """The (title, agent, model, effort) tuple a unit line must carry.
    RouteText prints '-' for an empty model or effort."""
    return (unit["title"],) + tuple(p or "-" for p in unit["executor"])


def load_events(path):
    records = []
    for line in read(path).splitlines():
        if not line.strip():
            continue
        try:
            obj = json.loads(line)
        except ValueError:
            err("non-JSON event line: " + line[:120])
        if obj.get("v") != 1:
            err("event line without v: 1: " + line[:120])
        if not isinstance(obj.get("event"), str):
            err("event line without an event name: " + line[:120])
        records.append(obj)
    if not records:
        err("empty events.jsonl")
    return records


def wrapper_calls(dirpath):
    if not os.path.isdir(dirpath):
        err("missing mandatory artifact: " + dirpath)
    found = {}
    for name in os.listdir(dirpath):
        m = CALL_FILE.match(name)
        if m:
            found.setdefault(int(m.group(1)), {})[m.group(2)] = os.path.join(dirpath, name)
    calls = []
    for n in sorted(found):
        parts = found[n]
        for part in ("argv", "stdout", "exit"):
            if part not in parts:
                err("missing mandatory artifact: call-%d.%s" % (n, part))
        argv_lines = read(parts["argv"]).splitlines()
        if not argv_lines or not argv_lines[0].startswith("call "):
            err("call-%d.argv does not start with the call header" % n)
        calls.append({"n": n, "argv": argv_lines[1:],
                      "stdout": read(parts["stdout"]),
                      "exit": read(parts["exit"]).strip()})
    if not calls:
        err("no wrapped opencode calls in " + dirpath)
    return calls


def git_subjects(repo):
    res = subprocess.run(["git", "-C", repo, "log", "--format=%s"],
                         capture_output=True, text=True)
    if res.returncode != 0:
        err("git log failed: " + res.stderr.strip())
    return res.stdout.splitlines()


def events_of(records, event, unit=None):
    return [r for r in records
            if r.get("event") == event and (unit is None or r.get("unit") == unit)]


def write_summary(path, rows):
    lines = ["unit\tstatus\tattempts\tseconds\tcost"]
    for row in rows:
        lines.append("\t".join([row["unit"], row["status"], str(row["attempts"]),
                                "%.1f" % row["seconds"], row["cost"]]))
    text = "\n".join(lines) + "\n"
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    sys.stdout.write(text)


def check_unit_events(uid, unit, records, out, index):
    """Events and output lines of one unit against its Executor triple."""
    if unit["status"] != "x":
        err("%s is [%s] in the final plan, want [x]" % (uid, unit["status"]))
    if not unit["executor"] or len(unit["executor"]) != 3:
        err("%s has no Executor triple in the plan" % uid)
    route = unit["executor"]
    attempts = events_of(records, "attempt", uid)
    want_attempts = 2 if index == 0 else 1
    if len(attempts) != want_attempts:
        err("%s has %d attempts, want %d" % (uid, len(attempts), want_attempts))
    for a in attempts:
        got = [a.get("route", {}).get("agent"), a.get("route", {}).get("model"),
               a.get("route", {}).get("effort")]
        if got != route:
            err("%s attempt %s route %s does not match executor %s" %
                (uid, a.get("n"), "/".join(str(g) for g in got), "/".join(route)))
    lines = output_routes(out).get(uid, [])
    if len(lines) != 1:
        err("%s has %d route lines in the runner output, want 1" % (uid, len(lines)))
    if list(lines[0]) != list(want_route(unit)):
        err("%s output route line %s does not match executor %s" %
            (uid, " | ".join(lines[0]), " | ".join(want_route(unit))))
    verifies = [v.get("exit_code") for v in events_of(records, "verify", uid)]
    want_verifies = [4, 0] if index == 0 else [0]
    if verifies != want_verifies:
        err("%s verify exit codes = %r, want %r" % (uid, verifies, want_verifies))
    commits = events_of(records, "commit", uid)
    if len(commits) != 1 or not commits[0].get("sha"):
        err("%s has %d commit events, want one with a sha" % (uid, len(commits)))
    ends = events_of(records, "unit_end", uid)
    if len(ends) != 1 or ends[0].get("status") != "done":
        err("%s unit_end status = %r, want one done" %
            (uid, [e.get("status") for e in ends]))
    end = ends[0]
    seconds = end.get("seconds")
    if not isinstance(seconds, (int, float)) or seconds <= 0:
        err("%s unit_end seconds = %r, want > 0" % (uid, seconds))
    cost = end.get("cost")
    if not isinstance(cost, (int, float)):
        err("%s unit_end carries no cost" % uid)
    return {"attempts": attempts, "seconds": seconds, "cost": cost}


def check_main(evdir):
    repo = os.path.join(evdir, "repo")
    if not os.path.isdir(repo):
        err("missing mandatory artifact: " + repo)
    if read(os.path.join(evdir, "run.exit")).strip() != "0":
        err("run.exit is not 0")
    out = os.path.join(evdir, "run.stdout")
    records = load_events(os.path.join(evdir, "run.events.jsonl"))
    plan = parse_plan(os.path.join(repo, "docs", "plans", "P001-smoke.md"))
    units = [u for u in plan if u["id"].startswith("P001.1.")]
    if len(units) != 2:
        err("the plan has %d units, want 2" % len(units))
    calls = wrapper_calls(os.path.join(evdir, "opencode-calls"))

    ends = events_of(records, "run_end")
    if not ends or ends[-1].get("exit_code") != 0:
        err("run_end = %r, want exit_code 0" % (ends[-1:] or None))
    if events_of(records, "route_switch"):
        err("the run switched routes: fallback must not happen")
    if "switching to" in read(out):
        err("the runner output contains a route switch")

    rows, cursor = [], 0
    for index, unit in enumerate(units):
        uid = unit["id"]
        checked = check_unit_events(uid, unit, records, out, index)
        attempts = checked["attempts"]
        unit_calls = calls[cursor:cursor + len(attempts)]
        cursor += len(attempts)
        if len(unit_calls) != len(attempts):
            err("%s: %d wrapped calls for %d attempts" %
                (uid, len(unit_calls), len(attempts)))
        for c in unit_calls:
            if c["exit"] != "0":
                err("%s wrapped call %d exited %s, want 0" % (uid, c["n"], c["exit"]))
        streams = [parse_stream(c["stdout"]) for c in unit_calls]
        if "--session" in unit_calls[0]["argv"]:
            err("%s attempt 1 must open its own session, the argv resumes one" % uid)
        if not streams[0]["session"]:
            err("%s attempt 1 stream carries no sessionID" % uid)
        if len(unit_calls) == 2:
            sid = streams[0]["session"]
            second = unit_calls[1]
            if "--session" not in second["argv"] or sid not in second["argv"]:
                err("%s attempt 2 does not resume session %s: argv %r" %
                    (uid, sid, second["argv"]))
            if streams[1]["session"] != sid:
                err("%s attempt 2 continues session %s, want the resumed %s" %
                    (uid, streams[1]["session"], sid))
        result = result_json(streams[-1])
        if result.get("status") != "done":
            err("%s successful attempt result status = %r, want done" %
                (uid, result.get("status")))
        expected = [norm(h) for h in result.get("handoff") or [] if norm(h)]
        expected += ["Unverified: " + norm(u)
                     for u in result.get("unverified") or [] if norm(u)]
        if not expected:
            err("%s result JSON carries no handoff items" % uid)
        got = [norm(h) for h in unit["handoff"]]
        if not got:
            err("%s has no Handoff items in the final plan: the header alone "
                "is not evidence" % uid)
        if got != expected:
            err("%s plan Handoff %r does not match the result JSON of the "
                "successful attempt %r" % (uid, got, expected))
        rows.append({"unit": uid, "status": "done", "attempts": len(attempts),
                     "seconds": checked["seconds"], "cost": "%.4f" % checked["cost"]})
    if cursor != len(calls):
        err("%d wrapped calls for %d attempts: extra or missing attempts" %
            (len(calls), cursor))
    subjects = git_subjects(repo)
    want_subjects = sorted(["init"] + [u["commit"] for u in units])
    if sorted(subjects) != want_subjects:
        err("commits %s are not exactly the plan commits %s" %
            (subjects, want_subjects))
    write_summary(os.path.join(evdir, "summary.tsv"), rows)


def check_no_such_model(evdir):
    repo = os.path.join(evdir, "repo")
    if not os.path.isdir(repo):
        err("missing mandatory artifact: " + repo)
    if read(os.path.join(evdir, "run.exit")).strip() != "7":
        err("run.exit is not 7")
    out = os.path.join(evdir, "run.stdout")
    records = load_events(os.path.join(evdir, "run.events.jsonl"))
    plan = parse_plan(os.path.join(repo, "docs", "plans", "P001-nosuch.md"))
    units = [u for u in plan if u["id"].startswith("P001.1.")]
    if len(units) != 1:
        err("the plan has %d units, want 1" % len(units))
    unit = units[0]
    if unit["status"] != "~":
        err("%s is [%s] in the plan, want [~] (stays in progress)" %
            (unit["id"], unit["status"]))
    if unit["handoff"]:
        err("%s has Handoff items although nothing was done" % unit["id"])
    calls = wrapper_calls(os.path.join(evdir, "opencode-calls"))

    ends = events_of(records, "run_end")
    if not ends or ends[-1].get("exit_code") != 7:
        err("run_end = %r, want exit_code 7" % (ends[-1:] or None))
    if events_of(records, "route_switch"):
        err("the run switched routes: the scenario has no fallback")
    if "switching to" in read(out):
        err("the runner output contains a route switch")
    attempts = events_of(records, "attempt", unit["id"])
    if len(attempts) != 1:
        err("%s has %d attempts, want exactly 1" % (unit["id"], len(attempts)))
    route = attempts[0].get("route", {})
    got = [route.get("agent"), route.get("model"), route.get("effort")]
    if got != unit["executor"]:
        err("%s attempt route %s does not match executor %s" %
            (unit["id"], "/".join(str(g) for g in got), " | ".join(unit["executor"])))
    if events_of(records, "verify", unit["id"]):
        err("%s ran a verify although the attempt was refused" % unit["id"])
    if events_of(records, "commit", unit["id"]):
        err("%s committed although the attempt was refused" % unit["id"])
    ends = events_of(records, "unit_end", unit["id"])
    if len(ends) != 1 or ends[0].get("status") != "failed":
        err("%s unit_end status = %r, want failed" %
            (unit["id"], [e.get("status") for e in ends]))
    seconds = ends[0].get("seconds")
    if not isinstance(seconds, (int, float)) or seconds <= 0:
        err("%s unit_end seconds = %r, want > 0" % (unit["id"], seconds))
    lines = output_routes(out).get(unit["id"], [])
    if len(lines) != 1 or list(lines[0]) != list(want_route(unit)):
        err("%s output route line %s does not match executor %s" %
            (unit["id"], " | ".join(lines[0]) if lines else "<none>",
             " | ".join(want_route(unit))))
    if len(calls) != 1:
        err("%d wrapped calls, want exactly 1" % len(calls))
    call = calls[0]
    stream = parse_stream(call["stdout"])
    if not stream["is_error"]:
        err("the refused attempt stream carries no error event")
    if "--model" not in call["argv"]:
        err("the wrapped call argv has no --model: %r" % call["argv"])
    write_summary(os.path.join(evdir, "summary.tsv"),
                  [{"unit": unit["id"], "status": "failed", "attempts": 1,
                    "seconds": seconds,
                    "cost": "%.4f" % ends[0].get("cost", 0.0)}])


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("main", "no-such-model"):
        err("usage: smoke-opencode-check.py main|no-such-model <evidence-dir>")
    evdir = sys.argv[2]
    if not os.path.isdir(evdir):
        err("missing mandatory artifact: " + evdir)
    if sys.argv[1] == "main":
        check_main(evdir)
    else:
        check_no_such_model(evdir)


if __name__ == "__main__":
    main()
