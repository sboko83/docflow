#!/usr/bin/env python3
"""Exercise the live-check shell lifecycle without calling a live agent.

Build, roster, lint and dry-run are stubs here; the real CLI and the
produced plan are checked by check-plan-skill.sh itself during a full run.
"""

import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


SOURCE = Path(__file__).resolve().parent / "check-plan-skill.sh"
ROSTER = """spend: strata, codex, zai · check: strata · trusted: codex
executor: pi | strata/swift-1.5-iq3_s | high
fallback: codex | test-model | high
cheap: pi | strata/swift-1.5-iq3_s | low
review: codex | test-model | high
acceptance: pi | zai/test-model | high
"""
PLAN = """---
id: P001
status: draft
executor: pi | strata/swift-1.5-iq3_s | high
fallback: codex | test-model | high
review: codex | test-model | high
acceptance: pi | zai/test-model | high
---
## Phase 1: Work
### [ ] P001.1.1 Work
### [ ] P001.1.2 Review
Role: review
## Phase 2: Acceptance
### [ ] P001.2.1 Accept
Role: acceptance
"""


class PlanSkillCheckTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="docflow-check-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        checkout = self.root / "checkout"
        (checkout / "tools").mkdir(parents=True)
        skill = checkout / "skills/bsv-docflow-plan/SKILL.md"
        skill.parent.mkdir(parents=True)
        skill.write_text("Test fixture: the agent is stubbed.\n")
        self.script = checkout / "tools/check-plan-skill.sh"
        shutil.copy2(SOURCE, self.script)
        self.stubs = self.root / "stubs"
        self.stubs.mkdir()
        self.work = self.root / "docflow-plan-skill-check"
        self.roster = self.root / "roster.txt"
        self.roster.write_text(ROSTER)
        self.plan = self.root / "plan.md"
        self.plan.write_text(PLAN)
        self.agent_pid = self.root / "agent.pid"
        self.timer_pid = self.root / "timer.pid"
        self.env = dict(
            os.environ,
            TMPDIR=str(self.root),
            PATH=str(self.stubs) + os.pathsep + os.environ["PATH"],
            TEST_STUBS=str(self.stubs),
            TEST_ROSTER=str(self.roster),
            TEST_PLAN=str(self.plan),
            TEST_AGENT_PID=str(self.agent_pid),
            TEST_TIMER_PID=str(self.timer_pid),
            TEST_AGENT_ARGS=str(self.root / "agent.args"),
            TEST_MODE="success",
        )
        self.write_stub("go", """#!/bin/bash
[ "$1" = build ] && [ "$2" = -o ] || exit 99
cp "$TEST_STUBS/docflow" "$3"
""")
        self.write_stub("docflow", """#!/bin/bash
case "$1" in
    roster) cat "$TEST_ROSTER" ;;
    lint|run) exit 0 ;;
    *) exit 99 ;;
esac
""")
        self.write_stub("sleep", """#!/bin/bash
printf '%s\n' "$$" > "$TEST_TIMER_PID"
if [ "$TEST_MODE" = timeout ]; then
    exec /bin/sleep 0.3
fi
exec /bin/sleep "$@"
""")
        self.write_stub("pi", """#!/usr/bin/env python3
import os
import json
from pathlib import Path
import signal
import sys
import time

Path(os.environ['TEST_AGENT_ARGS']).write_text(json.dumps(sys.argv[1:]))
Path(os.environ['TEST_AGENT_PID']).write_text(str(os.getpid()))
Path('docs/plans/P001-test.md').write_text(Path(os.environ['TEST_PLAN']).read_text())
mode = os.environ['TEST_MODE']
if mode == 'timeout':
    signal.signal(signal.SIGTERM, lambda *_: exit(0))
if mode in ('timeout', 'interrupt'):
    while True:
        time.sleep(1)
time.sleep(0.1)
raise SystemExit(7 if mode == 'failure' else 0)
""")

    def write_stub(self, name, text):
        path = self.stubs / name
        path.write_text(text)
        path.chmod(0o755)

    def start(self, *args):
        proc = subprocess.Popen(
            ["/bin/bash", str(self.script), *args],
            env=self.env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        self.addCleanup(self.kill_group, proc)
        return proc

    @staticmethod
    def kill_group(proc):
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        if proc.poll() is None:
            proc.wait()

    def run_check(self, *args):
        proc = self.start(*args)
        output, _ = proc.communicate(timeout=15)
        return proc.returncode, output

    def assert_stopped(self, pid_file):
        self.assertTrue(pid_file.exists(), str(pid_file))
        pid = int(pid_file.read_text())
        with self.assertRaises(ProcessLookupError, msg=f"process {pid} survived"):
            os.kill(pid, 0)

    def test_success_reaps_timer_and_reuses_snapshot(self):
        code, output = self.run_check()
        self.assertEqual(code, 0, output)
        self.assertIn("ok roster-routes", output)
        self.assert_stopped(self.timer_pid)
        self.assert_stopped(self.agent_pid)
        self.roster.write_text("roster changed after the live call\n")
        code, output = self.run_check("--check-only")
        self.assertEqual(code, 0, output)

    def test_agent_failure_cannot_pass_or_be_reused(self):
        self.env["TEST_MODE"] = "failure"
        code, output = self.run_check()
        self.assertEqual(code, 1, output)
        self.assertIn("finished with code 7", output)
        self.assertNotIn("all checks passed", output)
        self.assert_stopped(self.timer_pid)
        code, output = self.run_check("--check-only")
        self.assertEqual(code, 1, output)

    def test_timeout_fails_even_if_agent_handles_term_successfully(self):
        self.env["TEST_MODE"] = "timeout"
        code, output = self.run_check()
        self.assertEqual(code, 1, output)
        self.assertIn("finished with code 124", output)
        self.assert_stopped(self.timer_pid)
        self.assert_stopped(self.agent_pid)
        code, output = self.run_check("--check-only")
        self.assertEqual(code, 1, output)

    def test_interruption_reaps_agent_and_timer(self):
        self.env["TEST_MODE"] = "interrupt"
        proc = self.start()
        deadline = time.monotonic() + 10
        while not (self.agent_pid.exists() and self.timer_pid.exists()):
            if proc.poll() is not None or time.monotonic() >= deadline:
                self.fail("agent and timer did not start")
            time.sleep(0.01)
        proc.send_signal(signal.SIGTERM)
        output, _ = proc.communicate(timeout=5)
        self.assertEqual(proc.returncode, 143, output)
        self.assert_stopped(self.agent_pid)
        self.assert_stopped(self.timer_pid)
        code, output = self.run_check("--check-only")
        self.assertEqual(code, 2, output)

    def test_routes_must_match_requested_default_roster(self):
        self.plan.write_text(PLAN.replace(
            "review: codex | test-model | high",
            "review: pi | strata/swift-1.5-iq3_s | high",
        ))
        code, output = self.run_check()
        self.assertEqual(code, 1, output)
        self.assertIn("FAIL roster-routes", output)

    def test_route_whitespace_is_not_significant(self):
        self.plan.write_text(PLAN.replace(" | ", "|"))
        code, output = self.run_check()
        self.assertEqual(code, 0, output)

    def test_check_only_rejects_unrecorded_exit(self):
        code, output = self.run_check()
        self.assertEqual(code, 0, output)
        (self.work / "agent.exit").unlink()
        code, output = self.run_check("--check-only")
        self.assertEqual(code, 2, output)

    def test_pi_uses_glm_flash(self):
        code, output = self.run_check()
        self.assertEqual(code, 0, output)
        args = json.loads((self.root / "agent.args").read_text())
        self.assertEqual(args[args.index("--model") + 1], "zai/glm-5.3-flash")
        self.assertIn("--no-session", args)
        self.assertIn("--no-mcp", args)
        self.assertIn("--no-extensions", args)
        self.assertNotIn("--permission-mode", args)
        self.assert_stopped(self.timer_pid)

    def test_model_can_be_selected(self):
        code, output = self.run_check("--model", "strata/test-model")
        self.assertEqual(code, 0, output)
        args = json.loads((self.root / "agent.args").read_text())
        self.assertEqual(args[args.index("--model") + 1], "strata/test-model")

    def test_invalid_arguments_fail_before_calling_agent(self):
        for args in (("--agent", "claude"), ("--model",),
                     ("--model", "anthropic/claude-sonnet-5-5"),
                     ("--check-only", "--model", "zai/test"), ("extra",)):
            with self.subTest(args=args):
                code, output = self.run_check(*args)
                self.assertEqual(code, 2, output)
                self.assertFalse(self.agent_pid.exists())


if __name__ == "__main__":
    unittest.main()
