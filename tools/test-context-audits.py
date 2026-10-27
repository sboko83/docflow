#!/usr/bin/env python3
"""Regressions for the context and read audits, on synthetic fixtures.

    PYTHONDONTWRITEBYTECODE=1 python3 tools/test-context-audits.py

Covers the two defects the P047.1.4 review found:

  * `tools/context-audit.py --check` compared the skill descriptions with the
    1500 budget at their size *now*, so a repository whose descriptions grow
    past the budget exited 0. A fixture repository that is under the budget
    now and over it projected at 200 plans must exit 1, name the
    `skills:descriptions` row, and mark that row `fail` in `--json --check`;
    the same repository without the growth must exit 0 and mark it `ok`.
  * `tools/read-audit.py` resolved a read path against the working tree, so a
    file the unit's epoch had and the repository deleted later was not a read
    at all. A fixture repository measures such a file from its blob through
    both inputs - a `read` call and a shell command, a glob included - with
    the controls that a `Read:` path and a changed file are not extra and a
    path in neither the commit's tree nor on disk is not counted.

`ShellReadsTest` is the table of shell commands the read audit parses: the
file arguments of `cat`/`head`/`sed`/`grep`, the window their flags name, a
redirection target that is not a read. `AuditableUnitsTest` keeps a unit known
only from a Codex or `claude -p` log in the report, as `skipped`.

The last class replays the review's own case on this repository's history:
`tests/helpers.py` at the P011.1.1 commit, absent from the working tree,
measured as 601 proxy tokens. The unit's tool calls are synthetic: no
personal transcript is read and no live agent is called.

Needs `docflow` in `PATH` or `bin/docflow` of this checkout.
"""

import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

# Importing tools/read-audit.py through importlib writes tools/__pycache__
# beside it, and `docflow commit` runs `git add -A`.
sys.dont_write_bytecode = True

REPO = Path(__file__).resolve().parents[1]
CONTEXT_AUDIT = REPO / "tools" / "context-audit.py"
READ_AUDIT = REPO / "tools" / "read-audit.py"

# The size the P047.1.4 review measured for tests/helpers.py at the P011.1.1
# commit: 2405 bytes, 601 proxy tokens.
LEGACY_BYTES = 2405
LEGACY_TOKENS = 601
SKILL_BUDGET = 1500

SCRATCH = Path(tempfile.mkdtemp(prefix="context-audits-"))
# Transcripts of a project that has none: an audit of a fixture repository
# must not pick up the machine's own agent sessions.
EMPTY_PROJECTS = SCRATCH / "claude-projects"
EMPTY_PROJECTS.mkdir()
KEEPERS = [tempfile.TemporaryDirectory(prefix="context-audits-repo-",
                                       dir=str(SCRATCH)) for _ in range(2)]

# A git that ignores the developer's global config: no signing, no hooks, no
# borrowed identity, no includeIf from the machine the test runs on.
GIT_ENV = dict(os.environ,
               GIT_CONFIG_GLOBAL=os.devnull,
               GIT_CONFIG_SYSTEM=os.devnull,
               GIT_CONFIG_NOSYSTEM="1",
               HOME=str(SCRATCH),
               PYTHONDONTWRITEBYTECODE="1")


def docflow_path():
    """The CLI the audits run: `DOCFLOW`, then `PATH`, then this checkout's
    `bin/docflow`."""
    for candidate in (os.environ.get("DOCFLOW"), shutil.which("docflow"),
                      REPO / "bin" / "docflow", REPO / "bin" / "docflow.exe"):
        if candidate and Path(candidate).exists():
            return str(candidate)
    raise unittest.SkipTest(
        "docflow not found: put it in PATH, set DOCFLOW, or build it with "
        "go build -o bin/docflow ./cmd/docflow")


def run_tool(tool, args, cwd, docflow):
    """Run one audit tool over a fixture repository: the tools take the
    repository as their working directory. `--check` exits 1 on purpose, so
    the exit code is the caller's to judge."""
    argv = [sys.executable, str(tool)] + list(args)
    if docflow:
        argv += ["--docflow", docflow]
    return subprocess.run(argv, cwd=str(cwd), env=GIT_ENV,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def stdout_of(result):
    # The tools print in text mode, which is CRLF on Windows: normalize, so
    # line-anchored assertions hold on every OS.
    return result.stdout.decode("utf-8", "replace").replace("\r\n", "\n")


def stderr_of(result):
    return result.stderr.decode("utf-8", "replace").replace("\r\n", "\n")


def fixture_text(nbytes):
    """Lines of a fixed width summing to exactly `nbytes`: a whole-file read
    then measures `nbytes / 4` proxy tokens and a line window is predictable."""
    line = "# " + "x" * 43
    step = len(line) + 1
    lines = [line] * (nbytes // step)
    tail = nbytes - step * len(lines)
    if tail:
        lines.append("#" * (tail - 1))
    text = "".join(item + "\n" for item in lines)
    assert len(text.encode("utf-8")) == nbytes, "the fixture text is the measure"
    return text


def window(text, count):
    """Bytes of the first `count` lines of `text`."""
    return len("".join(text.splitlines(keepends=True)[:count]).encode("utf-8"))


def plans(first, last):
    return ["P%03d" % number for number in range(first, last + 1)]


def archive_entry(plan):
    """An archive entry as the projection reads it: an id and a `## Gotchas`
    body of a fixed size, linking nothing."""
    return ("---\nid: %s\ntitle: Fixture plan %s\nclosed: 2026-01-01\n---\n"
            "## Outcome\n\nClosed by the fixture.\n\n## Gotchas\n\n"
            "- A fixture gotcha of a fixed size for %s.\n" % (plan, plan, plan))


def skill(description):
    return ("---\nname: alpha\ndescription: %s\n---\n\nBody of the skill.\n"
            % description)


def design_runner():
    """A design topic whose `L10-12` range names one archive link and whose
    whole file names two: the fixture plan `Read:`s the whole file and that
    range, which is what makes a whole-file `Read:` cost more than a range.
    The links are to entries closed before the growth window, so their
    `## Gotchas` do not grow and the ranged prompt stays in its budget."""
    return "\n".join([
        "# Runner", "", "The runner drives a unit.", "Filler.", "Filler.",
        "Filler.", "Filler.", "Filler.", "Filler.", "## Cycle",
        "The cycle is [P034](../archive/P034-fixture.md).", "Filler.",
        "## Start",
        "The start is [P033](../archive/P033-fixture.md).",
    ]) + "\n"


def design_cycle():
    """Long enough for the fixture plan's `L77-126` and `L94-126` items."""
    lines = ["# Cycle", "", "The unit cycle.", "Filler."]
    while len(lines) < 120:
        lines.append("Filler line %d." % len(lines))
    lines.append("The close is [P033](../archive/P033-fixture.md).")
    while len(lines) < 130:
        lines.append("Filler line %d." % len(lines))
    return "\n".join(lines) + "\n"


DOCFLOW_MD = """---
docflow: 3
read_budget: 40000
start_read: README.md, docs/design/README.md
start_budget: 4000
---
## Queue

## Frozen

## Backlog
- A backlog line.
"""

README_MD = "# Fixture\n\nThe start read of the fixture repository.\n"
DESIGN_README_MD = "# Design index\n\n- [Runner](runner.md)\n"
USAGE_MD = "# Usage\n\nHow to use the fixture.\n"
DESIGN_FORMAT_MD = "# Format\n\nThe plan format of the fixture.\n"

SHORT_DESC = "Alpha skill of the fixture."
LONG_DESC = ("Alpha skill of the fixture, grown over the plans closed after the "
             "window plan: the description sits in every agent's context, so its "
             "bytes per plan are what the budget is measured against, and this "
             "one crosses the 1500 token budget at two hundred plans while "
             "staying under it now.")


class Fixture:
    """A temporary git repository: the audits measure the epoch of a commit,
    so the history is part of the fixture and is built commit by commit."""

    def __init__(self, name, keeper):
        self.root = Path(keeper.name) / name
        self.git_dir = self.root / ".git"
        self.commits = {}
        self.root.mkdir(parents=True)
        self.git("init", "-q")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.com")

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root)] + [str(a) for a in args],
                              env=GIT_ENV, stdout=subprocess.PIPE,
                              stderr=subprocess.STDOUT, check=True)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        # LF on every OS: the audits measure blob bytes, and Windows text
        # mode would add one byte per line to every fixture.
        path.write_text(text, encoding="utf-8", newline="\n")
        return text

    def remove(self, name):
        self.git("rm", "-q", "--ignore-unmatch", name)

    def commit(self, subject):
        """Record the working tree and return the short sha of the commit. An
        empty commit is allowed: the scenarios differ by one file each, and
        the control's last commit changes nothing."""
        self.git("add", "-A")
        self.git("commit", "-q", "--no-gpg-sign", "--allow-empty", "-m", subject)
        return self.git("rev-parse", "--short", "HEAD").stdout.decode().strip()

    def docflow_doc(self):
        """The files every audit needs at the repository root, and nothing
        that grows: the tests vary one input each."""
        self.write("docs/docflow.md", DOCFLOW_MD)
        self.write("README.md", README_MD)
        self.write("docs/design/README.md", DESIGN_README_MD)
        self.write("docs/design/runner.md", design_runner())
        self.write("docs/design/cycle.md", design_cycle())
        self.write("docs/design/format.md", DESIGN_FORMAT_MD)
        self.write("docs/usage.md", USAGE_MD)
        # The context audit builds its fixture plan into a copy of the tracked
        # tree: `docs/plans` has to exist there.
        self.write("docs/plans/.gitkeep", "")

    def pi_log(self, unit, attempt, calls):
        """A `pi -p --mode json` log - the runner's own telemetry, which makes
        a unit auditable on its own, with no agent session behind it. `calls`
        is a list of `(toolName, args)`."""
        lines = ["$ pi -p --mode json  # the fixture's synthetic log"]
        for index, (name, args) in enumerate(calls):
            lines.append(json.dumps({"type": "tool_execution_start",
                                     "toolCallId": "c%d" % index,
                                     "toolName": name, "args": args}))
        path = self.git_dir / "docflow" / "run"
        path.mkdir(parents=True, exist_ok=True)
        (path / ("%s.%d.out" % (unit, attempt))).write_text(
            "\n".join(lines) + "\n", encoding="utf-8", newline="\n")


class SkillDescriptionBudgetTest(unittest.TestCase):
    """The budget check compares the projection at `--plans`, not the size
    of the moment."""

    @classmethod
    def setUpClass(cls):
        cls.docflow = docflow_path()
        cls.breach = cls.build("budget-breach", grow=True)
        cls.control = cls.build("budget-control", grow=False)

    @classmethod
    def build(cls, name, grow):
        """34 entries closed before the window plan, 5 after it: the window
        the projection divides by. `grow` lengthens the one skill's
        description in the last commit, so its per-plan growth is measured."""
        fixture = Fixture(name, KEEPERS[0])
        fixture.docflow_doc()
        fixture.write("skills/alpha/SKILL.md", skill(SHORT_DESC))
        for plan in plans(1, 34):
            fixture.write("docs/archive/%s-fixture.md" % plan, archive_entry(plan))
        fixture.commit("P999.1.0: fixture base")
        for plan in plans(35, 39):
            fixture.write("docs/archive/%s-fixture.md" % plan, archive_entry(plan))
            fixture.commit("%s: close fixture plan" % plan)
        if grow:
            fixture.write("skills/alpha/SKILL.md", skill(LONG_DESC))
        fixture.commit("P999.1.1: final fixture commit")
        return fixture

    def rows(self, fixture, args, expect=0):
        """The rows of one run. `expect` is the exit code: `--check` exits 1
        when a budget is breached, which is the point of the breach
        scenarios and not of the control."""
        result = run_tool(CONTEXT_AUDIT, args, fixture.root, self.docflow)
        self.assertEqual(result.returncode, expect,
                         "%s: %s" % (" ".join(args),
                                     (stdout_of(result) + stderr_of(result))[:2000]))
        return json.loads(stdout_of(result))

    def row(self, fixture, name, args=("--json",), expect=0):
        rows = self.rows(fixture, args, expect)
        for item in rows:
            if item["name"] == name:
                return item
        raise AssertionError("no %s row: %s" % (name, [r["name"] for r in rows]))

    def test_growth_crosses_the_budget_at_the_target_plans(self):
        """The fixture is under the budget now and over it projected: the
        current size alone passed, which is the defect the review named."""
        row = self.row(self.breach, "skills:descriptions")
        self.assertLess(row["tokens"], SKILL_BUDGET,
                        "the fixture must start under the budget, it is %d"
                        % row["tokens"])
        self.assertGreater(row["tokens_at200"], SKILL_BUDGET,
                           "the projection must cross the budget, it is %d"
                           % row["tokens_at200"])
        self.assertGreater(row["per_plan"], 0, "the growth must be measured")

    def test_check_fails_the_projected_budget(self):
        """`--check` exits 1 and reports the descriptions row, in the text
        the operator reads."""
        result = run_tool(CONTEXT_AUDIT, ("--check",), self.breach.root, self.docflow)
        self.assertEqual(result.returncode, 1, stdout_of(result)[:2000])
        output = stdout_of(result)
        self.assertIn("FAIL skills:descriptions", output)
        self.assertIn("skill descriptions at 200 plans vs %d" % SKILL_BUDGET, output)
        self.assertRegex(output,
                         r"(?m)^  skills:descriptions: .*\bat 200 plans \d+ tokens, "
                         r"limit %d$" % SKILL_BUDGET)

    def test_json_check_marks_the_descriptions_row_fail(self):
        """The JSON `check` field agrees with the exit code and the text, and
        it is the only failing row: the other budgets hold in the same
        repository."""
        by_name = {row["name"]: row
                   for row in self.rows(self.breach, ("--json", "--check"), expect=1)}
        self.assertEqual(by_name["skills:descriptions"]["check"], "fail")
        self.assertGreater(by_name["skills:descriptions"]["tokens_at200"],
                           SKILL_BUDGET)
        self.assertEqual(by_name["skills/alpha/SKILL.md#description"]["check"],
                         "n/a")
        self.assertEqual(by_name["start-reads"]["check"], "ok")
        self.assertEqual(by_name["prompt:regular:ranged"]["check"], "ok")
        self.assertEqual(sorted(name for name, row in by_name.items()
                                if row["check"] == "fail"), ["skills:descriptions"])

    def test_control_projection_in_budget_passes(self):
        """The same repository with a description that does not grow: exit 0
        and the row `ok` - the check is the projection, not a blanket
        failure."""
        for args in (("--check",), ("--json", "--check")):
            result = run_tool(CONTEXT_AUDIT, args, self.control.root, self.docflow)
            self.assertEqual(result.returncode, 0, stdout_of(result)[:2000])
        row = self.row(self.control, "skills:descriptions",
                       ("--json", "--check"), expect=0)
        self.assertEqual(row["check"], "ok")
        self.assertEqual(row["per_plan"], 0)
        self.assertLess(row["tokens"], SKILL_BUDGET)
        self.assertLess(row["tokens_at200"], SKILL_BUDGET)

    def test_descriptions_over_budget_now_fail_whatever_the_projection(self):
        """A shrinking projection must not save a repository already over the
        budget: the fixture's descriptions shrink per plan and are over the
        budget today, so the row fails, on the `now` criterion."""
        fixture = Fixture("budget-now", KEEPERS[1])
        fixture.docflow_doc()
        for index, name in enumerate(("alpha", "beta")):
            fixture.write("skills/%s/SKILL.md" % name, skill("d" * 6000))
        fixture.write("docs/archive/P001-fixture.md", archive_entry("P001"))
        fixture.commit("P999.1.0: fixture base")
        fixture.write("docs/archive/P035-fixture.md", archive_entry("P035"))
        fixture.commit("P035: close fixture plan")
        fixture.write("docs/archive/P036-fixture.md", archive_entry("P036"))
        for name in ("alpha", "beta"):
            fixture.write("skills/%s/SKILL.md" % name, skill("d" * 3050))
        fixture.commit("P036: close fixture plan")
        by_name = {row["name"]: row for row in
                   self.rows(fixture, ("--json", "--check"), expect=1)}
        self.assertGreater(by_name["skills:descriptions"]["tokens"], SKILL_BUDGET)
        self.assertLess(by_name["skills:descriptions"]["tokens_at200"],
                        SKILL_BUDGET, "the fixture's projection must be under")
        self.assertEqual(by_name["skills:descriptions"]["check"], "fail")
        result = run_tool(CONTEXT_AUDIT, ("--check",), fixture.root, self.docflow)
        self.assertEqual(result.returncode, 1, stdout_of(result)[:2000])
        self.assertIn("skill descriptions now vs %d" % SKILL_BUDGET,
                      stdout_of(result))

    def test_start_reads_over_budget_now_fail_whatever_the_projection(self):
        """The same rule holds for every budget row: README.md is over
        `start_budget` today and shrinks per plan, so its projection is under
        the budget and the row still fails, on the `now` criterion."""
        fixture = Fixture("start-now", KEEPERS[1])
        fixture.docflow_doc()
        fixture.write("README.md", "# Fixture\n\n" + "r" * 20000 + "\n")
        fixture.write("docs/archive/P001-fixture.md", archive_entry("P001"))
        fixture.commit("P999.1.0: fixture base")
        fixture.write("docs/archive/P035-fixture.md", archive_entry("P035"))
        fixture.commit("P035: close fixture plan")
        fixture.write("docs/archive/P036-fixture.md", archive_entry("P036"))
        fixture.write("README.md", "# Fixture\n\n" + "r" * 17000 + "\n")
        fixture.commit("P036: close fixture plan")
        by_name = {row["name"]: row for row in
                   self.rows(fixture, ("--json", "--check"), expect=1)}
        start = by_name["start-reads"]
        self.assertGreater(start["tokens"], start["limit"])
        self.assertLess(start["tokens_at200"], start["limit"],
                        "the fixture's projection must be under")
        self.assertEqual(start["check"], "fail")
        result = run_tool(CONTEXT_AUDIT, ("--check",), fixture.root, self.docflow)
        self.assertEqual(result.returncode, 1, stdout_of(result)[:2000])
        self.assertIn("start reads now vs start_budget", stdout_of(result))


class HistoricalReadTest(unittest.TestCase):
    """A file that exists only at the unit's commit is a read, through both
    inputs: a direct read call - pi's `path` and Claude's `file_path` - and a
    shell command."""

    @classmethod
    def setUpClass(cls):
        cls.docflow = docflow_path()
        cls.helper = fixture_text(LEGACY_BYTES)
        cls.fixture = cls.build()
        cls.external = SCRATCH / "external-notes.md"
        cls.external.write_text(fixture_text(44), encoding="utf-8", newline="\n")
        cls.fixture.pi_log("P999.1.1", 1, [
            ("read", {"path": "docs/kept.md"}),
            ("read", {"path": "tests/legacy.py"}),
            ("read", {"path": "docs/never-existed.md"}),
            ("bash", {"command": "cat tests/legacy.py"}),
            ("bash", {"command": "grep -l helper tests/legacy.py"}),
            ("bash", {"command": "cat src/main.py"}),
            ("bash", {"command": "head -n 2 tests/*.py"}),
            ("bash", {"command": "cat docs/never-existed.md"}),
            ("bash", {"command": "cat notes/tmp.md"}),
        ])
        cls.fixture.pi_log("P999.1.2", 1, [
            ("read", {"path": "tests/legacy.py"}),
            # A backslash path would not survive a real shell either: shlex
            # eats the separators, so the fixture says it the way bash does.
            ("bash", {"command": "cat %s" % cls.external.as_posix()}),
        ])
        cls.fixture.pi_log("P999.1.3", 1, [
            ("bash", {"command": "head -n 2 tests/*.py"}),
        ])
        cls.fixture.pi_log("P999.1.4", 1, [
            ("Read", {"file_path": "tests/legacy.py"}),
        ])
        result = run_tool(READ_AUDIT, ("--json",), cls.fixture.root, cls.docflow)
        if result.returncode:
            raise AssertionError("read-audit failed: %s" % stderr_of(result)[:2000])
        cls.data = json.loads(stdout_of(result))
        cls.units = {row["unit"]: row for row in cls.data["units"]}
        cls.merged = {unit: {item["path"]: item["tokens"]
                             for item in row["extra_files"]}
                      for unit, row in cls.units.items()}

    @classmethod
    def build(cls):
        """A unit whose commit carries a test helper that a later commit
        deletes: the helper is a file of the unit's epoch only."""
        fixture = Fixture("read-history", KEEPERS[1])
        fixture.docflow_doc()
        fixture.write("docs/kept.md", "# Kept\n\nNamed by the unit's `Read:`.\n")
        fixture.write("src/main.py", "# the file the unit changes\n")
        fixture.write("tests/legacy.py", cls.helper)
        fixture.write("docs/plans/P999-read-audit.md", """---
id: P999
title: Read audit fixture
status: active
---
## Phase 1: Work

### [x] P999.1.1 Work
Read:
- docs/kept.md
Do:
- Nothing, the fixture unit.
Verify: `true`
Depends: -
Commit: P999.1.1: fixture work

### [x] P999.1.2 Audit
Read:
- docs/kept.md
Do:
- Nothing, the fixture unit.
Verify: `true`
Depends: P999.1.1
Commit: P999.1.2: fixture audit

### [x] P999.1.3 Glob
Read:
- docs/kept.md
Do:
- Nothing, the fixture unit.
Verify: `true`
Depends: P999.1.2
Commit: P999.1.3: fixture glob

### [x] P999.1.4 Claude read
Read:
- docs/kept.md
Do:
- Nothing, the fixture unit.
Verify: `true`
Depends: P999.1.3
Commit: P999.1.4: fixture claude read
""")
        fixture.commit("P999.1.0: fixture base")
        fixture.write("src/main.py", "# the file the unit changes\n# a second line\n")
        fixture.commits["P999.1.1"] = fixture.commit("P999.1.1: fixture work")
        fixture.write("docs/other.md", "# Added later\n")
        fixture.commits["P999.1.2"] = fixture.commit("P999.1.2: fixture audit")
        fixture.write("docs/note.md", "# Note\n")
        fixture.commits["P999.1.3"] = fixture.commit("P999.1.3: fixture glob")
        fixture.write("docs/claude-note.md", "# Claude note\n")
        fixture.commits["P999.1.4"] = fixture.commit("P999.1.4: fixture claude read")
        fixture.remove("tests/legacy.py")
        fixture.commit("P999.1.5: drop the legacy helper")
        # Never committed: measured from the working tree, as an external file is.
        fixture.write("notes/tmp.md", fixture_text(100))
        return fixture

    def test_historical_file_is_an_extra_read_of_its_blob_size(self):
        """The helper is in neither the unit's `Read:` nor its changes, and it
        is gone from the working tree: all three reads of it land in the extra
        reads at the epoch's sizes."""
        unit = self.units["P999.1.1"]
        self.assertEqual(unit["anchor"], "unit")
        self.assertEqual(unit["commit"], self.fixture.commits["P999.1.1"])
        self.assertEqual(set(self.merged["P999.1.1"]),
                         {"tests/legacy.py", "notes/tmp.md"})
        self.assertEqual(
            self.merged["P999.1.1"]["tests/legacy.py"],
            (2 * LEGACY_BYTES + window(self.helper, 2)
             + len(b"tests/legacy.py") + 1) // 4,
            "the read, the `cat`, the glob's `head -n 2` and the summary "
            "`grep -l` measure the blob of the unit's commit, not the missing "
            "working-tree file")

    def test_one_read_of_the_epoch_file_costs_601_proxy_tokens(self):
        """A single whole read of the epoch-only file costs 601 proxy tokens:
        the number the P047.1.4 review measured for `tests/helpers.py`."""
        unit = self.units["P999.1.2"]
        self.assertEqual(unit["anchor"], "unit")
        self.assertEqual(unit["commit"], self.fixture.commits["P999.1.2"])
        self.assertEqual(self.merged["P999.1.2"]["tests/legacy.py"], LEGACY_TOKENS)

    def test_read_list_paths_and_changed_files_are_not_extra(self):
        """The controls: the `Read:` path is in scope, the file the unit
        changed is its own artifact, a path in no tree and not on disk is not
        a read at all."""
        unit = self.units["P999.1.1"]
        self.assertEqual((unit["refs"], unit["read_list"]), (1, 1))
        self.assertNotIn("docs/kept.md", self.merged["P999.1.1"])
        self.assertNotIn("src/main.py", self.merged["P999.1.1"])
        self.assertEqual(unit["calls"], 7,
                         "the two reads of a path that exists nowhere add no call")

    def test_shell_glob_over_a_deleted_file_is_a_read(self):
        """`glob` walks the disk, where the file is gone: the commit's file
        list supplies the match, and the read costs the window `head -n 2`
        names, not the file. The path reaches the aggregate top list over the
        three units that read it."""
        unit = self.units["P999.1.1"]
        self.assertEqual(unit["extra"], 5)
        self.assertEqual(unit["extra_tokens"],
                        (2 * LEGACY_BYTES + window(self.helper, 2)
                         + len(b"tests/legacy.py") + 1 + 100) // 4)
        globber = self.units["P999.1.3"]
        self.assertEqual(globber["commit"], self.fixture.commits["P999.1.3"])
        self.assertEqual(globber["calls"], 1, "the glob resolved to the one "
                                              "file of the epoch")
        self.assertEqual(self.merged["P999.1.3"],
                         {"tests/legacy.py": window(self.helper, 2) // 4})
        top = {item["path"]: item for item in self.data["top"]}
        self.assertEqual(top["tests/legacy.py"],
                         {"path": "tests/legacy.py", "units": 4,
                          "tokens": (4 * LEGACY_BYTES + 2 * window(self.helper, 2)
                                      + len(b"tests/legacy.py") + 1) // 4})

    def test_the_claude_read_input_is_measured_like_pi_s(self):
        """The direct input of the other agent: Claude's `Read` names the file
        `file_path`, and the epoch's file resolves from the unit's commit at
        the same 601 proxy tokens."""
        unit = self.units["P999.1.4"]
        self.assertEqual(unit["commit"], self.fixture.commits["P999.1.4"])
        self.assertEqual(unit["calls"], 1)
        self.assertEqual(self.merged["P999.1.4"], {"tests/legacy.py": LEGACY_TOKENS})

    def test_external_and_uncommitted_files_are_still_measured(self):
        """The rules the fix must not break: a path outside the repository is
        measured by its absolute path, an uncommitted file by the working
        tree."""
        self.assertEqual(self.merged["P999.1.2"][str(self.external)], 44 // 4)
        self.assertEqual(self.merged["P999.1.1"]["notes/tmp.md"], 100 // 4)
        self.assertEqual(self.data["skipped_units"], 0)
        self.assertEqual({row["source"] for row in self.data["units"]}, {"log"})
        self.assertEqual({row["unit"] for row in self.data["units"]},
                         {"P999.1.1", "P999.1.2", "P999.1.3", "P999.1.4"})
        self.assertEqual(self.units["P999.1.2"]["extra_tokens"],
                        (LEGACY_BYTES + 44) // 4)

    def test_the_text_row_names_the_recovered_reads(self):
        """The report a human reads carries the epoch commit and the recovered
        extra path."""
        result = run_tool(READ_AUDIT, (), self.fixture.root, self.docflow)
        self.assertEqual(result.returncode, 0, stdout_of(result)[:2000])
        output = stdout_of(result)
        self.assertRegex(
            output,
            r"(?m)^unit P999\.1\.1 source log commit %s read_list 1 reads 7 "
            r"extra 5 tokens \d+ extra_tokens \d+ files: tests/legacy\.py \d+"
            % self.fixture.commits["P999.1.1"])
        self.assertIn("unmeasured units 0", output)


class ShellReadsTest(unittest.TestCase):
    """`bash_reads` on a table of shell commands: which words are files, and
    the window each read names. The files live on disk in a scratch root; the
    tree argument adds one file only the measured commit has."""

    CASES = [
        ("cat a.md", [("a.md", None)]),
        ("head -n 20 a.md", [("a.md", ("lines", 1, 20))]),
        ("head -20 a.md", [("a.md", ("lines", 1, 20))]),
        ("head -n20 a.md", [("a.md", ("lines", 1, 20))]),
        ("head --lines=7 a.md", [("a.md", ("lines", 1, 7))]),
        ("head -c 100 a.md", [("a.md", ("bytes", 100))]),
        ("sed -n '10,12p' a.md", [("a.md", ("lines", 10, 12))]),
        ("sed -n 40p a.md", [("a.md", ("lines", 40, 40))]),
        ("sed -n '/x/,$p' a.md", [("a.md", None)]),
        ("grep -n a.md a.md", [("a.md", None)]),
        ("grep -o word a.md", [("a.md", None)]),
        ("grep -rn word docs/b.md", [("docs/b.md", None)]),
        ("grep -l word a.md", [("a.md", ("grep", "names"))]),
        ("grep -L word a.md", [("a.md", ("grep", "names"))]),
        ("grep --files-with-matches word a.md", [("a.md", ("grep", "names"))]),
        ("grep -c word a.md", [("a.md", ("grep", "count"))]),
        ("grep --count word a.md", [("a.md", ("grep", "count"))]),
        ("grep -q word a.md", [("a.md", ("grep", "quiet"))]),
        ("grep -il word a.md", [("a.md", ("grep", "names"))]),
        ("grep -lq word a.md", [("a.md", ("grep", "quiet"))]),
        ("grep -lc word a.md", [("a.md", ("grep", "names"))]),
        ("grep -m 5 -c word a.md", [("a.md", ("grep", "count"))]),
        ("cat docs/*.md", [("docs/b.md", None), ("docs/gone.md", None)]),
        ("FOO=1 cat a.md", [("a.md", None)]),
        ("sudo cat a.md", [("a.md", None)]),
        ("cat < a.md", [("a.md", None)]),
        ("cat a.md > out.txt", [("a.md", None)]),
        ("cat a.md >> out.txt 2>/dev/null", [("a.md", None)]),
        ("cat a.md >out.txt", [("a.md", None)]),
        ("true && cat a.md; head -2 docs/b.md",
         [("a.md", None), ("docs/b.md", ("lines", 1, 2))]),
        # `cd` is not followed: a word resolves against the repository root.
        ("cd docs && cat ../a.md", []),
        ("cat $(ls) `ls` missing.md", []),
        ("wc -l a.md", []),
        ("cat 'unclosed", []),
    ]

    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("read_audit", str(READ_AUDIT))
        cls.audit = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.audit)
        cls.root = str(SCRATCH / "shell-reads")
        for name in ("a.md", "docs/b.md", "docs/sub/c.md", "out.txt"):
            path = Path(cls.root) / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("line\n", encoding="utf-8", newline="\n")

    def test_the_table(self):
        tree = {"docs/gone.md", "docs/sub/c.md"}
        for command, want in self.CASES:
            with self.subTest(command=command):
                self.assertEqual(self.audit.bash_reads(command, self.root, tree), want)

    def test_a_glob_without_a_tree_reads_the_disk(self):
        """The working tree of a unit in progress has no commit: the glob
        matches the disk under the root, whatever the current directory."""
        self.assertEqual(self.audit.bash_reads("cat docs/*.md", self.root),
                         [("docs/b.md", None)])

    def test_a_summary_grep_delivers_one_line_per_file(self):
        """The `grep` flags that bound the output to names or counts bound the
        read: `-q` delivers nothing, `-l`/`-c` one output line of the file,
        whatever its size."""
        self.assertEqual(self.audit.grep_size("docs/b.md", "quiet"), 0)
        self.assertEqual(self.audit.grep_size("docs/b.md", "names"),
                         len("docs/b.md") + 1)
        self.assertEqual(self.audit.grep_size("docs/b.md", "count"),
                         len("docs/b.md") + 16)


class AuditableUnitsTest(unittest.TestCase):
    """A unit `docflow stats` knows only from a `codex exec` or `claude -p`
    log (source `tokens`) is audited - and lands in `skipped` when it has no
    tool calls - instead of vanishing from the report."""

    def test_every_stats_unit_and_every_pi_log_is_audited(self):
        spec = importlib.util.spec_from_file_location("read_audit", str(READ_AUDIT))
        audit = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(audit)
        rows = [{"unit": "P001.1.2", "source": "tokens"},
                {"unit": "P001.1.10", "source": "transcript"},
                {"unit": "P001.1.1", "source": "-"}]
        logs = {"P002.1.1": ["P002.1.1.1.out"]}
        self.assertEqual(audit.auditable_units(rows, logs, 0),
                         ["P001.1.1", "P001.1.2", "P001.1.10", "P002.1.1"])
        self.assertEqual(audit.auditable_units(rows, logs, 2),
                         ["P001.1.10", "P002.1.1"])


class RealHistoryP011Test(unittest.TestCase):
    """The P011.1.1 case of the P047.1.4 review, on this repository's history.

    The tool calls are synthetic: the audit is asked what it makes of a read
    of `tests/helpers.py` at the unit's commit - in that commit, not in the
    working tree - and at what size. No transcript of anyone is read.
    """

    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location("read_audit", str(READ_AUDIT))
        cls.audit = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.audit)
        cls.root = str(REPO)
        cls.commit = cls.audit.unit_commit(cls.root, "P011.1.1")
        if not cls.commit:
            raise unittest.SkipTest(
                "the P011.1.1 commit is not in this history: the review's case "
                "needs it, pass a history that has it")
        cls.tree = cls.audit.tree_paths(cls.root, cls.commit)
        if "tests/helpers.py" not in cls.tree:
            raise unittest.SkipTest("tests/helpers.py is not in the P011.1.1 commit")
        cls.anchor = cls.audit.unit_anchor(cls.root, "P011.1.1")

    def measure(self, calls):
        return self.audit.audit_unit(self.root, "P011.1.1", calls, self.anchor)

    def test_helpers_py_resolves_only_against_the_unit_commit(self):
        """The premise of the defect: the working tree has no such file, the
        unit's commit does."""
        self.assertIsNone(self.audit.resolve(self.root, "tests/helpers.py"))
        self.assertEqual(self.audit.resolve(self.root, "tests/helpers.py", self.tree),
                         "tests/helpers.py")
        self.assertIn("tests/helpers.py",
                      self.audit.expand_paths("tests/*.py", self.root, self.tree))

    def test_helpers_py_read_restores_601_proxy_tokens(self):
        """The measurement the review said was lost: 601 proxy tokens - the
        blob of the epoch, not 0 - through both inputs, a direct read and a
        shell `cat`."""
        row = self.measure([(0, "read", "tests/helpers.py", None, None, None, "c1"),
                            (1, "bash", None, None, None, "cat tests/helpers.py", "c2")])
        self.assertEqual(row["anchor"], "unit")
        self.assertEqual(row["commit"], self.commit[:7])
        self.assertEqual([item["path"] for item in row["extra_files"]],
                         ["tests/helpers.py"])
        self.assertEqual(row["extra_tokens"], 2 * LEGACY_TOKENS)
        single = self.measure([(0, "read", "tests/helpers.py", None, None, None, "c1")])
        self.assertEqual(single["extra_files"][0]["tokens"], LEGACY_TOKENS)


def tearDownModule():
    """`docflow commit` runs `git add -A`: importing an audit module leaves
    `tools/__pycache__` beside it, and that must not ride into a commit."""
    shutil.rmtree(REPO / "tools" / "__pycache__", ignore_errors=True)
    for keeper in KEEPERS:
        keeper.cleanup()
    shutil.rmtree(SCRATCH, ignore_errors=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
