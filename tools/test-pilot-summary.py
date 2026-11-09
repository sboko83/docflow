#!/usr/bin/env python3
"""Regression checks for passive pilot aggregation; no private data needed."""

import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
import zipfile

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("pilot_summary", Path(__file__).with_name("pilot-summary.py"))
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)


class SummaryTest(unittest.TestCase):
    def archive(self, folder, name, entries):
        path = Path(folder) / name
        with zipfile.ZipFile(path, "w") as archive:
            for entry, data in entries.items():
                archive.writestr(entry, data)
        return path

    def test_missing_is_not_zero_and_projects_without_streams_remain(self):
        with tempfile.TemporaryDirectory() as folder:
            path = self.archive(folder, "data.zip", {
                "interactive.gitlog.txt": "private history",
                "auto/run/one.events.jsonl": '\n'.join(json.dumps(e) for e in [
                    {"event": "attempt", "unit": "P001.1.1"},
                    {"event": "attempt", "unit": "P001.1.1", "replan": True},
                    {"event": "attempt", "close": True},
                    {"event": "verify", "unit": "P001.1.1", "exit_code": 0, "reused": True},
                    {"event": "unit_end", "unit": "P001.1.1", "status": "blocked", "seconds": 2},
                    {"event": "unit_end", "unit": "P001.1.1", "status": "done", "seconds": 3, "cost": 0},
                ]),
            })
            rows = pilot.summarise([path])["projects"]
            auto, interactive = rows
            self.assertEqual((auto["units"], auto["done_units"], auto["attempts"]), (1, 1, 1))
            self.assertEqual(auto["unit_ends"]["blocked"], 1)
            self.assertEqual(auto["seconds"], {"total": 5, "measured_ends": 2, "all_ends": 2})
            self.assertEqual(auto["cost"], {"total": 0, "measured_ends": 1, "all_ends": 2})
            self.assertIsNone(auto["unverified"]["total"])
            self.assertEqual(auto["verify_reused"], 1)
            self.assertIsNone(interactive["units"])
            self.assertIsNone(interactive["cost"]["total"])
            self.assertEqual([r["project"] for r in pilot.summarise([path], True)["projects"]],
                             ["project-1", "project-2"])

    def test_repeated_copies_do_not_double_count_and_conflicts_fail(self):
        with tempfile.TemporaryDirectory() as folder:
            name = "project/run/one.events.jsonl"
            data = '{"event":"unit_end","unit":"P001.1.1","status":"done","seconds":2}'
            first = self.archive(folder, "first.zip", {name: data})
            copy = self.archive(folder, "copy.zip", {name: data})
            row = pilot.summarise([first, copy])["projects"][0]
            self.assertEqual(row["streams"], 1)
            self.assertEqual(row["seconds"]["total"], 2)
            changed = self.archive(folder, "changed.zip", {name: data + '\n{"event":"verify"}'})
            with self.assertRaisesRegex(ValueError, "conflicting copies"):
                pilot.summarise([first, changed])

    def test_malformed_event_fails_instead_of_undercounting(self):
        with tempfile.TemporaryDirectory() as folder:
            path = self.archive(folder, "bad.zip", {"project/run/one.events.jsonl": '[]'})
            with self.assertRaisesRegex(ValueError, "invalid event"):
                pilot.summarise([path])

    def test_pi_timing_and_context_survive_resume_without_double_counting(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "run").mkdir()
            (root / "stats").mkdir()
            unit = "P001.1.1"
            events = [
                {"event": "unit_start", "unit": unit, "ts": 10},
                {"event": "attempt", "unit": unit, "route": {"agent": "pi"}, "log": r".git\docflow\run\one.out"},
                {"event": "attempt", "unit": unit, "route": {"agent": "pi"}, "log": "/old/run/two.out"},
                {"event": "verify", "unit": unit, "ts": 19, "reused": True},
                {"event": "unit_end", "unit": unit, "ts": 20, "status": "blocked", "seconds": 10},
                {"event": "unit_start", "unit": unit, "ts": 30},
                {"event": "attempt", "unit": unit, "route": {"agent": "pi"}, "log": "two.out"},
                {"event": "unit_end", "unit": unit, "ts": 40, "status": "done", "seconds": 10},
            ]

            def message(role, stamp, duration, context=0):
                return {"type": "message_end", "message": {
                    "role": role, "timestamp": stamp, "durationMs": duration,
                    "usage": {"input": context, "cacheRead": 2, "cacheWrite": 3},
                    "content": "private prompt"}}

            messages = [message("assistant", 11000, 2000, 15),
                        message("toolResult", 14000, 3000),
                        message("assistant", 16000, 1000, 35)]
            first = '\n'.join(json.dumps(e) for e in messages)
            second = first + '\n' + json.dumps(message("assistant", 31000, 0, 55))
            (root / "run/one.out").write_text(first, encoding="utf-8")
            (root / "run/two.out").write_text(second, encoding="utf-8")
            (root / "stats" / (unit + ".json")).write_text(json.dumps({
                "unit": unit, "prompted_at": "1970-01-01T00:00:10Z", "proxy_tokens": 10}), encoding="utf-8")
            archive = self.archive(folder, "pilot.zip", {
                "private-project/run/one.events.jsonl": '\n'.join(json.dumps(e) for e in events)})
            report = pilot.summarise([archive], True, {"private-project": root})
            evidence = report["projects"][0]["evidence"]
            self.assertEqual(evidence["model_seconds"], {"total": 3, "measured_ends": 2, "all_ends": 2})
            self.assertEqual(evidence["tool_seconds"], {"total": 3, "measured_ends": 1, "all_ends": 2})
            self.assertIsNone(evidence["gate_seconds"]["total"])
            self.assertEqual([r["model_calls"] for r in evidence["outcomes"]], [2, 1])
            context = evidence["context"]
            self.assertEqual((context["paired_units"], context["all_units"]), (1, 1))
            self.assertEqual(context["units"][0], {
                "unit": unit, "proxy": 10, "requests": 3, "base_ctx": 20, "peak_ctx": 60,
                "sum_ctx": 120, "peak_proxy": 6, "grow_proxy": 4})
            self.assertNotIn("private-project", json.dumps(report))
            self.assertNotIn("private prompt", json.dumps(report))
            self.assertNotIn(str(root), json.dumps(report))

    def test_missing_partial_and_stale_evidence_stays_unknown(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "run").mkdir()
            (root / "stats").mkdir()
            unit = "P001.1.1"
            events = [
                {"event": "unit_start", "unit": unit, "ts": 10},
                {"event": "attempt", "unit": unit, "route": {"agent": "pi"}, "log": "missing.out"},
                {"event": "attempt", "unit": unit, "route": {"agent": "pi"}, "log": "partial.out"},
                {"event": "unit_end", "unit": unit, "ts": 20, "status": "done"}]
            messages = [
                {"type": "message_end", "message": {"role": "assistant", "timestamp": 11000,
                    "durationMs": True, "usage": {"input": 5, "cacheRead": 0, "cacheWrite": 0}}},
                {"type": "message_end", "message": {"role": "toolResult", "timestamp": 12000, "durationMs": -1}},
                {"type": "message_end", "message": {"role": "assistant", "durationMs": 1000}},
            ]
            (root / "run/partial.out").write_text('\n'.join(json.dumps(e) for e in messages)
                                                  + '\n{"type":"message_end",', encoding="utf-8")
            (root / "stats" / (unit + ".json")).write_text(json.dumps({
                "unit": unit, "prompted_at": "2026-11-09T00:00:00Z", "proxy_tokens": 10}), encoding="utf-8")
            archive = self.archive(folder, "pilot.zip", {
                "project/run/one.events.jsonl": '\n'.join(json.dumps(e) for e in events)})
            row = pilot.summarise([archive], logs={"project": root})["projects"][0]["evidence"]
            self.assertIsNone(row["model_seconds"]["total"])
            self.assertIsNone(row["tool_seconds"]["total"])
            self.assertEqual(row["outcomes"][0]["logs"], 1)
            self.assertEqual(row["outcomes"][0]["expected_logs"], 2)
            self.assertEqual(row["outcomes"][0]["model_calls"], 1)
            self.assertEqual(row["outcomes"][0]["timed_model_calls"], 0)
            self.assertEqual(row["context"]["paired_units"], 0)
            without = pilot.summarise([archive])["projects"][0]["evidence"]
            self.assertIsNone(without["model_seconds"]["total"])
            with self.assertRaisesRegex(ValueError, "not present"):
                pilot.summarise([archive], logs={"typo": root})

    def test_non_pi_logs_are_available_without_inventing_durations(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "run").mkdir()
            (root / "run/codex.out").write_text(
                '{"type":"turn.completed","usage":{"input_tokens":99}}', encoding="utf-8")
            unit = "P001.1.1"
            events = [
                {"event": "unit_start", "unit": unit, "ts": 10},
                {"event": "attempt", "unit": unit, "route": {"agent": "codex"}, "log": "codex.out"},
                {"event": "verify", "unit": unit, "seconds": 2},
                {"event": "unit_end", "unit": unit, "ts": 20, "status": "done"},
            ]
            row = pilot.evidence([events], root)
            self.assertEqual((row["outcomes"][0]["logs"], row["outcomes"][0]["pi_logs"]), (1, 0))
            self.assertIsNone(row["model_seconds"]["total"])
            self.assertIsNone(row["tool_seconds"]["total"])
            self.assertEqual(row["gate_seconds"]["total"], 2)
            events[2]["reused"] = True
            self.assertIsNone(pilot.evidence([events], root)["gate_seconds"]["total"])


if __name__ == "__main__":
    unittest.main()
