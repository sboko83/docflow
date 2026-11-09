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


if __name__ == "__main__":
    unittest.main()
