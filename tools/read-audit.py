#!/usr/bin/env python3
"""Read audit: what an agent read beyond the `Read:` list of its unit.

Run from the repository root:

    python3 tools/read-audit.py             # every recorded unit
    python3 tools/read-audit.py --limit 5   # the 5 newest units
    python3 tools/read-audit.py --json      # the same rows as JSON

Units: every unit `docflow stats` reports (Claude Code sessions under
`~/.claude/projects/<project>` are found the way `internal/stats` finds
them) plus every unit with a pi `.out` log in
`<git-dir>/docflow/run/` (`pi -p --mode json`). The log of a runner attempt on
Claude (`claude -p`) and on Codex (`codex exec --json`) reports no per-unit
tool calls this tool reads, so a unit known only from those logs lands in
`skipped`.

Per unit the inputs are: the `Read:` items of the plan file as of the unit's
commit, the files the agent read (Claude `Read` calls and pi `read` calls,
plus `cat`/`sed`/`head`/`grep` file arguments in `Bash` and `bash` calls) and
the files the unit's commit changed. A read of a file named by `Read:` is in
scope, a read of a file the unit changed is its own artifact, every other read
is extra. Scope is by file: a `Read:` item that names a line range puts the
whole file in scope, so reads past the range are not counted as extra.

Size: the file at the unit's commit; `offset`/`limit` of a read call narrow it
to the requested lines. A unit whose commit history lost - the public-release
squash, a replanned unit - is measured at the commit that closed its plan, a
unit in progress at the working tree; the `unit` row names the commit. When the
path is not in that commit (an untracked file, a copy under /tmp, a file of
another repository) the working-tree file is measured. A path names a file when
the measured commit's tree has it or the working tree has it: the tool asks the
commit's file list first, so a file of the unit's epoch that the repository
deleted later is measured from its blob, and a path in neither is not a read.
A shell glob expands over the commit's tree as well as over the disk. A read of
one file delivers at most `OUTPUT_CAP` bytes of `LINES_CAP` lines, the output
cap of the `read` and `bash` tools: a shell read is measured as the window its
flags name (`head -n N`, `head -c N`, `sed -n 'A,Bp'`) and, for `cat` and for
a `grep` that prints matches, as the file size capped by it; a summary
`grep` (`-l`/`-L`, `-c`, `-q`) prints no matches, so the files it names are
measured as their one output line - the name, or the name with a count - or
as nothing; a command naming several files shares that cap between them.

Proxy tokens are bytes / 4, the same proxy as `docflow lint` and
tools/context-audit.py.

Rows:
  unit        one unit: where its tool calls came from (`log` a pi `.out`
              log, `window` a Claude transcript window, `session` the agent
              session the runner recorded in docflow/stats), the size of its
              `Read:` list, its read calls, the extra calls, the tokens read,
              the extra tokens, the commit the reads are measured against, and
              the three largest extra paths
  top         the `--top` paths by extra tokens summed over the units, with
              the number of units that read them
  share       extra reads over all reads, in calls and in tokens
  skipped     units with neither a transcript window nor a pi `.out` log, and
              units whose plan file history does not have (no `Read:` list to
              compare against)
  base_ctx    the median `base_ctx` of every `--jump` plan, the size of
              `AGENTS.md`, `CLAUDE.md`, the skill descriptions and the
              `start_read` files at the commit of that plan's median unit, the
              jump between the first of those plans and the rest, and the
              inputs of the jump that moved by more than 2k tokens
"""

import argparse
import fnmatch
import glob
import json
import os
import posixpath
import re
import shlex
import subprocess
import sys
from collections import defaultdict

TOKEN_DIVISOR = 4
TOP_N = 15
EXTRA_FILES_PER_UNIT = 3
BASE_CTX_DELTA = 2000
DEFAULT_JUMP = "P035,P036,P041"
READ_COMMANDS = ("cat", "sed", "head", "grep")
# A read of a file reaches the model through the tool's own output cap: `read`
# truncates at 2000 lines / 50 KB and the `bash` tool truncates its output the
# same way, so no read of one file delivers more, whatever the file size.
OUTPUT_CAP = 50000
LINES_CAP = 2000
HEAD_LINES_RE = re.compile(r"^-(n|c)[0-9]+$")
PLAIN_LINES_RE = re.compile(r"^-[0-9]+$")
NUMBER_RE = re.compile(r"^[0-9]+$")
SED_RANGE_RE = re.compile(r"^([0-9]+)(?:,([0-9]+))?p$")
GREP_SUMMARY_LONG = {"--quiet": "quiet", "--silent": "quiet",
                     "--files-with-matches": "names",
                     "--files-without-match": "names",
                     "--files-without-matches": "names", "--count": "count"}
GREP_SUMMARY_SHORT = {"q": "quiet", "l": "names", "L": "names", "c": "count"}
GREP_VALUE_FLAGS = ("-e", "--regexp", "-f", "--file", "-m", "--max-count",
                    "-A", "-B", "-C", "--after-context", "--before-context",
                    "--context", "--include", "--exclude", "--exclude-dir")
SHELL_SPLIT_RE = re.compile(r"\|\||&&|[|\n;]")
REDIRECT_RE = re.compile(r"^[0-9&]*>>?[|]?$")
ASSIGN_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")
PREFIX_COMMANDS = ("sudo", "env", "time", "command", "nice", "nohup")
# Mirrors stats.commandPattern, stats.endPattern, stats.resultIDPattern.
PROMPT_RE = re.compile(r"\bdocflow\s+prompt(?:\s+(P[0-9]+\.[0-9]+\.[0-9]+))?")
END_RE = re.compile(r"\bdocflow\s+(?:finish|commit)\s+(P[0-9]+\.[0-9]+\.[0-9]+)")
RESULT_ID_RE = re.compile(r"^#\s+(P[0-9]+\.[0-9]+\.[0-9]+)")
PI_LOG_RE = re.compile(r"^(P[0-9]+\.[0-9]+\.[0-9]+)\.([0-9]+)\.out$")
RANGE_RE = re.compile(r"^P[0-9]+\.[0-9]+\.[0-9]+$")
UNIT_HEAD_RE = re.compile(r"^#{1,6}[ \t]+\[[ ~!xX-]\][ \t]*(P[0-9]+\.[0-9]+\.[0-9]+)\b")
HEADING_RE = re.compile(r"^#{1,6}[ \t]+\S")
# Unit fields in the fixed order of docs/design/format.md.
KEY_RE = re.compile(r"^(Read|Do|Verify|Traps|Depends|Executor|Model|Role|Commit|"
                    r"Handoff|Unverified|Blocked)\b[ \t]*(\([^\)]*\))?[ \t]*:")
META_RE = re.compile(r"^([A-Za-z0-9_]+):[ \t]*(.*)$")
REF_SPLIT_RE = re.compile(r"^(.+?)(?::L[0-9]+(?:-[0-9]+)?)?(?:#\S*)?[.,;:]?\Z")
SNAPSHOT = {}
TREE = {}


def tokens(nbytes):
    return int(nbytes) // TOKEN_DIVISOR


def git_out(root, *args, **kwargs):
    return subprocess.run(["git", "-C", root] + list(args),
                          input=kwargs.get("stdin"),
                          stdout=subprocess.PIPE, stderr=subprocess.DEVNULL).stdout


def git_text(root, *args, **kwargs):
    return git_out(root, *args, **kwargs).decode("utf-8", "replace")


def read_text(path):
    try:
        with open(path, "rb") as handle:
            return handle.read().decode("utf-8", "replace")
    except (IOError, OSError):
        return None


def frontmatter(text):
    """(meta, first body line index). Quoted values lose their quotes."""
    lines = text.splitlines(keepends=True)
    if not lines or lines[0].strip() != "---":
        return {}, 0
    for index in range(1, len(lines)):
        if lines[index].strip() == "---":
            meta = {}
            for line in lines[1:index]:
                match = META_RE.match(line.rstrip("\n"))
                if match:
                    meta[match.group(1)] = match.group(2).strip().strip('"')
            return meta, index + 1
    return {}, 0


def start_files(meta):
    return [item.strip() for item in meta.get("start_read", "").split(",")
            if item.strip()]


def snapshot(root, commit, prefixes):
    """path -> text of every blob tracked under `prefixes` at `commit`, empty
    when there is no commit: the working tree is measured then."""
    if not commit:
        return {}
    key = (commit, tuple(sorted(set(prefixes))))
    cached = SNAPSHOT.get(key)
    if cached is not None:
        return cached
    listing = git_text(root, "ls-tree", "-r", commit, "--", *sorted(set(prefixes)))
    entries = []
    for line in listing.splitlines():
        meta, _, path = line.partition("\t")
        parts = meta.split()
        if len(parts) == 3 and parts[1] == "blob":
            entries.append((parts[2], path))
    blobs = {}
    if entries:
        raw = git_out(root, "cat-file", "--batch",
                      stdin=("\n".join(sha for sha, _ in entries) + "\n").encode())
        position = 0
        for _, path in entries:
            newline = raw.find(b"\n", position)
            if newline < 0:
                break
            header = raw[position:newline].split()
            position = newline + 1
            if len(header) != 3 or header[1] != b"blob":
                continue
            size = int(header[2])
            blobs[path] = raw[position:position + size].decode("utf-8", "replace")
            position += size + 1
    SNAPSHOT[key] = blobs
    return blobs


def span_bytes(text, offset, limit):
    """Bytes a `read` call delivers: the `offset`/`limit` window of a text -
    the whole text when neither is given - capped by the tool's own output
    limit, `LINES_CAP` lines of `OUTPUT_CAP` bytes."""
    return window_bytes(text, (offset or 1) - 1 if (offset or limit) else 0,
                        None if not limit else (offset or 1) - 1 + int(limit))


def window_bytes(text, start, end):
    """Bytes of the lines `start`..`end` of a text, capped by the output
    limit: lines past `LINES_CAP` and bytes past `OUTPUT_CAP` never reach the
    model."""
    lines = text.splitlines(keepends=True)
    first = max(0, min(start, len(lines)))
    last = len(lines) if end is None else max(first, min(end, len(lines)))
    last = min(last, first + LINES_CAP)
    return min(len("".join(lines[first:last]).encode("utf-8")), OUTPUT_CAP)


def short(sha):
    return sha[:7] if sha else "-"


def unit_sort(unit):
    return [int(part) for part in re.findall(r"[0-9]+", unit)]


def plan_id_of(unit):
    return unit.split(".")[0]


def unit_commit(root, unit):
    """The newest commit whose subject starts with the unit id, over every
    ref: the commits of a plan closed before the public-release squash live
    outside `HEAD`."""
    pattern = re.compile(r"^%s[: \t]" % re.escape(unit))
    for line in git_text(root, "log", "--all", "--format=%H\x01%s").splitlines():
        sha, _, subject = line.partition("\x01")
        if pattern.match(subject.strip()):
            return sha
    return None


def plan_close_commit(root, plan):
    """The commit that archived `plan`: the oldest one adding its entry in
    docs/archive. The history before the public release is squashed, so the
    commits of a unit of an early plan do not exist and the state of the tree
    at the plan's close is the nearest one."""
    found = git_text(root, "log", "--all", "--reverse", "--diff-filter=A",
                     "--format=%H", "--", "docs/archive/%s-*.md" % plan).split()
    return found[0] if found else None


def unit_anchor(root, unit):
    """(commit, kind): the state of the tree the unit's reads are measured
    against. The unit's own commit when history has it, the commit that closed
    its plan when the unit's commit is squashed away or the unit was replanned,
    the working tree for a unit in progress."""
    commit = unit_commit(root, unit)
    if commit:
        return commit, "unit"
    close = plan_close_commit(root, plan_id_of(unit))
    if close:
        return close, "close"
    return None, "worktree"


def changed_paths(root, commit):
    """Files a unit's commit changed; for a unit without a commit, the working
    tree changes of the unit in progress."""
    if commit:
        listing = git_text(root, "show", "--name-only", "--pretty=format:", commit)
    else:
        listing = (git_text(root, "diff", "--name-only", "HEAD") + "\n"
                   + git_text(root, "ls-files", "--others", "--exclude-standard"))
    return set(line.strip() for line in listing.splitlines() if line.strip())


def plan_text(root, unit, commit):
    """The plan file of `unit` at `commit`, at `commit^` when that commit is
    the plan's close (the plan file leaves `docs/plans` there), the working
    tree copy when there is no commit."""
    plan = plan_id_of(unit)
    for at in ([commit, commit + "^"] if commit else []):
        paths = sorted(path for path in
                       git_text(root, "ls-tree", "--name-only", "-r", at,
                                "--", "docs/plans/").splitlines()
                       if os.path.basename(path).startswith(plan + "-"))
        if paths:
            text = git_text(root, "show", "%s:%s" % (at, paths[0]))
            if text:
                return text
    folder = os.path.join(root, "docs", "plans")
    try:
        names = sorted(name for name in os.listdir(folder) if name.startswith(plan + "-"))
    except (IOError, OSError):
        return None
    return read_text(os.path.join(folder, names[0])) if names else None


def ref_path(item):
    """The file part of a `Read:` item: `docs/a.md:L10-12` and `docs/a.md#x`
    give `docs/a.md`, prose gives its first word, which resolves to `None`."""
    token = item.strip().strip("`").strip()
    if not token:
        return None
    match = REF_SPLIT_RE.match(token.split()[0].strip("`"))
    return match.group(1) if match else None


def unit_read_refs(text, unit):
    """The `Read:` items of `unit` in a plan file, `None` when the plan names
    no such unit. A list item continues on an indented line."""
    if text is None:
        return None
    lines = text.splitlines()
    start = None
    for index, line in enumerate(lines):
        match = UNIT_HEAD_RE.match(line)
        if match and match.group(1) == unit:
            start = index + 1
            break
    if start is None:
        return None
    items, in_read, open_item = [], False, False
    for line in lines[start:]:
        if HEADING_RE.match(line):
            break
        stripped = line.strip()
        if stripped == "Read:":
            in_read, open_item = True, False
            continue
        if KEY_RE.match(stripped):
            in_read, open_item = False, False
            continue
        if not in_read:
            continue
        if stripped.startswith("- "):
            items.append(stripped[2:])
            open_item = True
        elif stripped and open_item and line[:1].isspace():
            items[-1] += " " + stripped
        else:
            open_item = False
    return items


def first_line(text):
    lines = text.splitlines()
    return lines[0] if lines else ""


def result_text(content):
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return ""
    parts = []
    for block in content:
        if isinstance(block, dict) and isinstance(block.get("text"), str):
            parts.append(block["text"])
    return "\n".join(parts)


def positive(value):
    return value if isinstance(value, int) and value > 0 else None


def add_tool(calls, request, name, input_, tool_id):
    """Normalize an agent tool call to a tuple
    (request, kind, path, offset, limit, command, tool_id): `read` names a
    file, `bash` a command; every other tool is skipped."""
    if not isinstance(input_, dict):
        input_ = {}
    kind = name.lower() if isinstance(name, str) else ""
    if kind == "read":
        path = input_.get("file_path")
        if not isinstance(path, str):
            path = input_.get("path")
        if isinstance(path, str):
            calls.append((request, "read", path, positive(input_.get("offset")),
                          positive(input_.get("limit")), None, tool_id))
        return
    if kind == "bash":
        command = input_.get("command")
        if isinstance(command, str):
            calls.append((request, "bash", None, None, None, command, tool_id))


def claude_transcript(path):
    """Tool calls of one Claude transcript in order, each tagged with its
    request index, plus `tool_use_id` -> first line of its result.

    Mirrors `internal/stats`.readTranscript: one request per `message.id`
    (a message written on several lines counts once), a line that is not one
    JSON object is skipped silently."""
    calls, results, keys = [], {}, {}
    request = -1
    try:
        handle = open(path, "rb")
    except (IOError, OSError):
        return calls, results
    with handle:
        for number, raw in enumerate(handle, 1):
            if b'"tool_' not in raw:
                continue
            try:
                entry = json.loads(raw.decode("utf-8", "replace"))
            except ValueError:
                continue
            if not isinstance(entry, dict) or not isinstance(entry.get("message"), dict):
                continue
            message = entry["message"]
            blocks = message.get("content")
            if not isinstance(blocks, list):
                continue
            if entry.get("type") == "user":
                for block in blocks:
                    if not isinstance(block, dict) or block.get("type") != "tool_result":
                        continue
                    tool_id = block.get("tool_use_id")
                    if isinstance(tool_id, str) and tool_id not in results:
                        results[tool_id] = first_line(
                            result_text(block.get("content")).strip())
                continue
            if entry.get("type") != "assistant":
                continue
            key = message.get("id")
            if not (isinstance(key, str) and key):
                key = "line%d" % number
            if key not in keys:
                keys[key] = True
                request += 1
            for block in blocks:
                if isinstance(block, dict) and block.get("type") == "tool_use":
                    add_tool(calls, request, block.get("name"), block.get("input"),
                             block.get("id"))
    return calls, results


def pi_log(path):
    """Tool calls of one `pi -p --mode json` log: every call is its own
    request, the whole log belongs to one unit."""
    calls = []
    try:
        handle = open(path, "rb")
    except (IOError, OSError):
        return calls
    with handle:
        for raw in handle:
            if b"tool_execution_start" not in raw:
                continue
            try:
                event = json.loads(raw.decode("utf-8", "replace"))
            except ValueError:
                continue
            if (isinstance(event, dict)
                    and event.get("type") == "tool_execution_start"):
                add_tool(calls, len(calls), event.get("toolName"), event.get("args"),
                         event.get("toolCallId"))
    return calls


def interactive_windows(calls, results):
    """unit -> the calls of that unit's window in one transcript: from the
    `bash` call of `docflow prompt <unit>` to `docflow finish|commit <unit>`
    inclusive, open to the end of the transcript. Mirrors
    `internal/stats`.interactiveWindows; `docflow prompt` without an id takes
    the id from the first line of its result."""
    starts, windows = {}, {}
    for call in calls:
        if call[1] != "bash":
            continue
        for match in PROMPT_RE.finditer(call[5]):
            unit = match.group(1)
            if not unit:
                found = RESULT_ID_RE.match(results.get(call[6], ""))
                unit = found.group(1) if found else None
            if unit and unit not in starts:
                starts[unit] = call[0]
        for match in END_RE.finditer(call[5]):
            unit = match.group(1)
            if unit in starts and unit not in windows:
                windows[unit] = [item for item in calls
                                 if starts[unit] <= item[0] <= call[0]]
    for unit, start in starts.items():
        if unit not in windows:
            windows[unit] = [item for item in calls if item[0] >= start]
    return windows


def tree_paths(root, commit):
    """Every blob path tracked at `commit`, cached: the files the measured
    epoch had. The working tree is not the epoch - a test helper deleted by a
    later plan, a design topic renamed, an early `tests/` suite - so a path
    the unit read and the disk lost is still a file."""
    if not commit:
        return set()
    cached = TREE.get(commit)
    if cached is None:
        cached = set(git_text(root, "ls-tree", "-r", "--name-only", commit).splitlines())
        TREE[commit] = cached
    return cached


def glob_match(pattern, path):
    """Shell glob match per path component: `fnmatch`'s `*` crosses `/`, a
    shell's does not, so a flat match would make `docs/*` name `docs/a/b.md`.
    `**` matches one component, as bash without `globstar` does."""
    pattern_parts, path_parts = pattern.split("/"), path.split("/")
    if len(pattern_parts) != len(path_parts):
        return False
    return all(fnmatch.fnmatchcase(part, expression)
               for part, expression in zip(path_parts, pattern_parts))


def resolve(root, token, tree=None):
    """Repository-relative path of a token, the absolute path when it lives
    outside the repository, `None` when it names no file. `tree` is the file
    list of the measured commit: a path in it resolves without touching the
    disk, so a file of the unit's epoch that the working tree no longer has is
    a read; without `tree` only the disk decides (the working tree of a unit in
    progress)."""
    if not token or token == "-" or "$" in token or "`" in token:
        return None
    candidate = os.path.expanduser(token)
    if not os.path.isabs(candidate):
        candidate = os.path.normpath(os.path.join(root, candidate))
    prefix = root + os.sep
    relative = candidate[len(prefix):] if candidate.startswith(prefix) else None
    if relative is not None:
        # Git tree paths are always `/`-separated; `normpath` made the
        # candidate OS-native, which on Windows never matches them.
        relative = relative.replace(os.sep, "/")
        if tree is not None and relative in tree:
            return relative
    if not os.path.isfile(candidate):
        return None
    # A file outside the repository is reported by its OS-canonical path.
    return relative if relative is not None else os.path.normpath(candidate)


def expand_paths(word, root, tree):
    """The paths a shell word names: the working-tree glob matches first, then
    the commit's files the same pattern matches - `glob` walks the disk, so a
    file of the epoch that the tree lost matches nothing there. A word without
    glob magic names itself. Paths are resolved, duplicates of both sources
    counted once: the shell expanded them into one argument list."""
    if not glob.has_magic(word):
        resolved = resolve(root, word, tree)
        return [resolved] if resolved else []
    pattern = os.path.expanduser(word)
    disk_pattern = pattern if os.path.isabs(pattern) else os.path.join(root, pattern)
    # The tree side matches `/`-separated repository paths: the pattern as
    # typed, relative to the root, which is what `os.path.join(root, path)`
    # gave it when every separator was already `/`.
    repo_pattern = pattern
    if os.path.isabs(pattern):
        try:
            repo_pattern = os.path.relpath(pattern, root)
        except ValueError:  # another drive: no repository path can match
            repo_pattern = None
    if repo_pattern is not None:
        repo_pattern = posixpath.normpath(repo_pattern.replace(os.sep, "/"))
    seen, out = set(), []
    for candidate in (sorted(glob.glob(disk_pattern))
                      + [path for path in sorted(tree or ())
                         if repo_pattern is not None
                         and glob_match(repo_pattern, path)]):
        resolved = resolve(root, candidate, tree)
        if resolved and resolved not in seen:
            seen.add(resolved)
            out.append(resolved)
    return out


def shell_size(text, bound):
    """Bytes a shell read of `text` delivers: the window its flags name
    (`head -n N`, `head -c N`, `sed -n 'A,Bp'`), the whole text for `cat` and
    for a `grep` that prints matches, whose output size no flag bounds - a
    summary `grep` (`-l`/`-c`/`-q`) delivers `grep_size` instead; every window
    is capped by the `bash` tool's output limit."""
    if bound and bound[0] == "bytes":
        return min(len(text.encode("utf-8")), bound[1], OUTPUT_CAP)
    if bound and bound[0] == "lines":
        return window_bytes(text, bound[1] - 1, bound[2])
    return min(len(text.encode("utf-8")), OUTPUT_CAP)


def head_bound(options):
    """The window `head` names: `-n N`/`-N` N lines, `-c N` N bytes."""
    lines, nbytes = None, None
    for index, item in enumerate(options):
        value = options[index + 1] if index + 1 < len(options) and NUMBER_RE.match(
            options[index + 1]) else None
        if item in ("-n", "--lines") and value:
            lines = int(value)
        elif item in ("-c", "--bytes") and value:
            nbytes = int(value)
        elif item.startswith("--lines=") and item.split("=")[-1].isdigit():
            lines = int(item.split("=")[-1])
        elif item.startswith("--bytes=") and item.split("=")[-1].isdigit():
            nbytes = int(item.split("=")[-1])
        elif HEAD_LINES_RE.match(item) or PLAIN_LINES_RE.match(item):
            digits = int(re.findall(r"[0-9]+", item)[0])
            if item.startswith("-c"):
                nbytes = digits
            elif item.startswith("-n"):
                lines = digits
            elif PLAIN_LINES_RE.match(item):
                lines = digits
    if nbytes:
        return ("bytes", nbytes)
    return ("lines", 1, lines) if lines else None


def sed_bound(script):
    """The line window a `sed` script prints: `10,12p`, `40p`; a range by
    pattern or `$` names no boundable window."""
    match = SED_RANGE_RE.match(script or "")
    if not match:
        return None
    start = int(match.group(1))
    end = int(match.group(2)) if match.group(2) else start
    return ("lines", start, end) if end >= start else None


def grep_bound(options):
    """The summary window `grep` names: `-q` prints nothing, `-l`/`-L` the
    name of every file it reads, `-c` its count - `-q` wins over `-l`, `-l`
    over `-c`, as in grep. Bundled short flags (`-il`, `-lc`) count, a flag
    whose value is the next word (`-e pattern`, `-A N`) skips it, and `--`
    ends the flags."""
    modes = []
    index = 0
    while index < len(options):
        item = options[index]
        if item == "--":
            break
        if item in GREP_VALUE_FLAGS:
            index += 2
            continue
        if item.startswith("--"):
            mode = GREP_SUMMARY_LONG.get(item)
            if mode:
                modes.append(mode)
        elif re.match(r"^-[A-Za-z]+$", item):
            modes.extend(GREP_SUMMARY_SHORT[letter] for letter in item[1:]
                         if letter in GREP_SUMMARY_SHORT)
        index += 1
    for mode in ("quiet", "names", "count"):
        if mode in modes:
            return ("grep", mode)
    return None


def grep_size(path, mode):
    """Bytes a summary `grep` delivers per file it names: nothing with `-q`,
    the file's one output line with `-l`/`-L`/`-c` - its name, or its name
    with a count. The pattern is not evaluated, so every named file is
    charged its line, matched or not."""
    if mode == "quiet":
        return 0
    return min(len(path.encode("utf-8")) + (16 if mode == "count" else 1),
               OUTPUT_CAP)


def bash_reads(command, root, tree=None):
    """(path, bound) for every file named as an argument of
    `cat`/`sed`/`head`/`grep` in a shell command. The first non-flag argument
    of `sed` and `grep` is the script and the pattern, not a file. `tree` is
    the file list of the measured commit: its paths resolve, and a glob matches
    them, as the working tree's do."""
    found = []
    for segment in SHELL_SPLIT_RE.split(command):
        try:
            argv = shlex.split(segment)
        except ValueError:
            continue
        index = 0
        while index < len(argv) and (ASSIGN_RE.match(argv[index])
                                     or argv[index] in PREFIX_COMMANDS):
            index += 1
        if index + 1 >= len(argv):
            continue
        program = os.path.basename(argv[index])
        if program not in READ_COMMANDS:
            continue
        options = argv[index + 1:]
        # `> out.txt` splits into two words: the target is written, not read.
        targets = {position + 1 for position, item in enumerate(options)
                   if REDIRECT_RE.match(item)}
        args = [item for position, item in enumerate(options)
                if not item.startswith("-") and "<" not in item and ">" not in item
                and item != "/dev/null" and position not in targets]
        bound = None
        if program in ("sed", "grep") and args:
            if program == "sed":
                bound = sed_bound(args[0])
            else:
                bound = grep_bound(options)
            args = args[1:]
        elif program == "head":
            bound = head_bound(options)
        for item in args:
            for path in expand_paths(item, root, tree):
                found.append((path, bound))
    return found


def claude_projects_dir(root):
    """Mirrors stats.ClaudeProjectsDir."""
    configured = os.environ.get("DOCFLOW_CLAUDE_PROJECTS")
    if configured:
        return os.path.expanduser(configured)
    return os.path.join(os.path.expanduser("~"), ".claude", "projects",
                        re.sub(r"[^A-Za-z0-9]", "-", root))


def stats_rows(root, docflow):
    """`docflow stats` rows: unit, source, base_ctx."""
    output = subprocess.run([docflow, "stats"], cwd=root, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE).stdout.decode("utf-8", "replace")
    rows = []
    for line in output.splitlines()[1:]:
        parts = line.split()
        if len(parts) == 9 and RANGE_RE.match(parts[0]):
            rows.append({"unit": parts[0], "proxy": parts[1],
                         "base_ctx": parts[4], "source": parts[8]})
    return rows


def unit_sessions(git_dir):
    """unit -> the session ids recorded by the runner in docflow/stats."""
    out = defaultdict(list)
    for path in glob.glob(os.path.join(git_dir, "docflow", "stats", "P*.json")):
        text = read_text(path)
        try:
            data = json.loads(text) if text else {}
        except ValueError:
            data = {}
        raw = data.get("sessions") if isinstance(data, dict) else None
        if isinstance(raw, str):
            raw = [raw]
        if isinstance(raw, list):
            out[os.path.basename(path)[:-len(".json")]] = [
                item for item in raw if isinstance(item, str)]
    return out


def pi_logs(git_dir):
    """unit -> the `pi -p --mode json` attempt logs in docflow/run."""
    out = defaultdict(list)
    for path in sorted(glob.glob(os.path.join(git_dir, "docflow", "run", "*.out"))):
        match = PI_LOG_RE.match(os.path.basename(path))
        if not match:
            continue
        first = read_text(path)
        if first is not None and first.split("\n", 1)[0].startswith("$ pi"):
            out[match.group(1)].append(path)
    return out


def auditable_units(rows, logs, limit):
    """Units to audit: every unit `docflow stats` reports, plus those with a
    pi log, in plan order, the newest `limit` when asked. A unit known only
    from a `codex exec` or `claude -p` log has no tool calls this tool reads
    and is counted in `skipped`, not dropped."""
    units = set(row["unit"] for row in rows)
    units.update(logs)
    ordered = sorted(units, key=unit_sort)
    return ordered[-limit:] if limit else ordered


def audit_unit(root, unit, calls, anchor):
    """Classify the calls of one unit measured at `anchor`: in-scope reads and
    extra reads. Paths resolve against the file list of that commit, so a file
    the epoch had and the working tree lost is measured from its blob. One tool
    call is one group: a `bash` command naming ten files delivers them through
    one output cap, so the sizes of a group are scaled down to it together."""
    commit, kind = anchor
    tree = tree_paths(root, commit)
    refs = unit_read_refs(plan_text(root, unit, commit), unit)
    read_paths = set()
    for item in refs or []:
        path = ref_path(item)
        resolved = resolve(root, path, tree) if path else None
        if resolved:
            read_paths.add(resolved)
    changed = changed_paths(root, commit)

    groups = []
    for call in calls:
        if call[1] == "read":
            path = resolve(root, call[2], tree)
            if path:
                groups.append([(path, call[3], call[4], None)])
        else:
            items = [(path, None, None, bound) for path, bound in
                     bash_reads(call[5], root, tree)]
            if items:
                groups.append(items)
    tracked = sorted(set(path for group in groups for path, _, _, _ in group
                         if not os.path.isabs(path)))
    blobs = snapshot(root, commit, tracked) if tracked and commit else {}
    texts = {}

    def text_of(path):
        if os.path.isabs(path):
            return read_text(path)
        if path not in texts:
            text = blobs.get(path)
            texts[path] = text if text is not None else read_text(
                os.path.join(root, path))
        return texts[path]

    extra, extra_tokens, all_tokens = [], 0, 0
    for group in groups:
        sizes = []
        for path, offset, limit, bound in group:
            if bound is not None and bound[0] == "grep":
                size = grep_size(path, bound[1])
            else:
                text = text_of(path)
                if text is None:
                    size = 0
                elif bound is not None:
                    size = shell_size(text, bound)
                else:
                    size = span_bytes(text, offset, limit)
            sizes.append((path, size))
        total = sum(size for _, size in sizes)
        scale = float(OUTPUT_CAP) / total if len(sizes) > 1 and total > OUTPUT_CAP else 1.0
        for path, size in sizes:
            delivered = int(size * scale)
            all_tokens += delivered
            if path in read_paths or path in changed:
                continue
            extra.append((path, delivered))
            extra_tokens += delivered

    merged = defaultdict(int)
    for path, size in extra:
        merged[path] += size
    return {"unit": unit, "commit": short(commit), "anchor": kind,
            "read_list": len(read_paths),
            "refs": len(refs) if refs is not None else -1,
            "calls": sum(len(group) for group in groups), "extra": len(extra),
            "tokens": tokens(all_tokens), "extra_tokens": tokens(extra_tokens),
            "extra_files": sorted(({"path": path, "tokens": tokens(size)}
                                   for path, size in merged.items()),
                                  key=lambda item: (-item["tokens"], item["path"]))
            [:EXTRA_FILES_PER_UNIT],
            "extra_by_path": dict(merged)}


def base_ctx_report(root, rows, plans):
    """Explain the jump of `base_ctx` between plans by the size of the docflow
    inputs at the commit of each plan's median unit."""
    per_plan = defaultdict(list)
    for row in rows:
        if row["source"] == "transcript" and row["base_ctx"].isdigit():
            per_plan[plan_id_of(row["unit"])].append((int(row["base_ctx"]), row["unit"]))
    picks = []
    for plan in plans:
        values = sorted(per_plan.get(plan, []))
        if not values:
            continue
        committed = [(base, unit, unit_commit(root, unit)) for base, unit in values]
        landed = [item for item in committed if item[2]]
        if landed:
            base, unit, commit = landed[len(landed) // 2]
            origin = "unit"
        else:
            base, unit = values[len(values) // 2]
            commit, origin = plan_close_commit(root, plan), "plan close"
        picks.append({"plan": plan, "unit": unit, "base_ctx": base, "units": len(values),
                      "commit": commit, "origin": origin})
    lines, data = [], {"plans": picks, "inputs": {}, "jumps": []}
    for pick in picks:
        lines.append("base_ctx %s median %d units %d unit %s commit %s%s" % (
            pick["plan"], pick["base_ctx"], pick["units"], pick["unit"],
            short(pick["commit"]),
            " (" + pick["origin"] + ")" if pick["origin"] != "unit" else ""))
    usable = [pick for pick in picks if pick["commit"]]
    if len(usable) < 2:
        lines.append("base_ctx: not explained by docflow inputs")
        return lines, data

    names = ["AGENTS.md", "CLAUDE.md", "skills_descriptions", "start_reads"]
    for pick in usable:
        blobs = snapshot(root, pick["commit"], ["AGENTS.md", "CLAUDE.md", "skills"])
        meta, _ = frontmatter(snapshot(root, pick["commit"], ["docs/docflow.md"]).get(
            "docs/docflow.md", ""))
        starts = start_files(meta)
        sizes = {
            "AGENTS.md": len(blobs.get("AGENTS.md", "").encode("utf-8")),
            "CLAUDE.md": len(blobs.get("CLAUDE.md", "").encode("utf-8")),
            "skills_descriptions": sum(
                len(frontmatter(text)[0].get("description", "").encode("utf-8"))
                for path, text in blobs.items() if path.endswith("/SKILL.md")),
            "start_reads": sum(len(text.encode("utf-8")) for text in
                               snapshot(root, pick["commit"], starts).values())
            if starts else 0,
        }
        data["inputs"][pick["plan"]] = dict(
            (name, tokens(sizes[name])) for name in names)
    for name in names:
        lines.append("base_ctx input %s %s" % (
            name, " ".join("%s %d" % (pick["plan"], data["inputs"][pick["plan"]][name])
                           for pick in usable)))

    first = usable[0]
    unexplained = False
    for pick in usable[1:]:
        jump = pick["base_ctx"] - first["base_ctx"]
        inputs = sum(data["inputs"][pick["plan"]][name] - data["inputs"][first["plan"]][name]
                     for name in names)
        changed = [(name, data["inputs"][pick["plan"]][name] - data["inputs"][first["plan"]][name])
                   for name in names]
        changed = [item for item in changed if abs(item[1]) > BASE_CTX_DELTA]
        lines.append("base_ctx jump %s -> %s real %+d inputs %+d" % (
            first["plan"], pick["plan"], jump, inputs))
        for name, delta in sorted(changed, key=lambda item: -abs(item[1])):
            lines.append("base_ctx changed %s %+d (%s -> %s)" % (
                name, delta, first["plan"], pick["plan"]))
        data["jumps"].append({"from": first["plan"], "to": pick["plan"],
                              "real": jump, "inputs": inputs,
                              "changed": [{"input": name, "tokens": delta}
                                          for name, delta in changed]})
        if not changed:
            unexplained = True
    if unexplained:
        lines.append("base_ctx: not explained by docflow inputs")
    return lines, data


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--json", action="store_true", help="print the rows as JSON")
    parser.add_argument("--limit", type=int, help="only the N newest units")
    parser.add_argument("--top", type=int, default=TOP_N,
                        help="paths in the top list (default %d)" % TOP_N)
    parser.add_argument("--jump", default=DEFAULT_JUMP,
                        help="plans whose base_ctx jump is explained, comma separated "
                             "(default %s)" % DEFAULT_JUMP)
    parser.add_argument("--docflow", help="docflow binary (default: PATH, then bin/docflow)")
    args = parser.parse_args()

    root = os.path.abspath(os.getcwd())
    if not os.path.isfile(os.path.join(root, "docs", "docflow.md")):
        sys.exit("run from the repository root: no docs/docflow.md here")
    docflow = args.docflow
    if not docflow:
        found = subprocess.run(["sh", "-c", "command -v docflow"],
                               stdout=subprocess.PIPE).stdout.decode().strip()
        candidate = os.path.join(root, "bin", "docflow")
        docflow = found or (candidate if os.path.exists(candidate) else None)
    if not docflow:
        sys.exit("docflow not in PATH and no bin/docflow: build it with "
                 "go build -o bin/docflow ./cmd/docflow")
    git_dir = git_text(root, "rev-parse", "--git-dir").strip()
    if not os.path.isabs(git_dir):
        git_dir = os.path.join(root, git_dir)

    rows = stats_rows(root, docflow)
    logs = pi_logs(git_dir)
    sessions = unit_sessions(git_dir)
    transcripts = {}
    for path in glob.glob(os.path.join(claude_projects_dir(root), "*.jsonl")):
        transcripts[os.path.basename(path)[:-len(".jsonl")]] = claude_transcript(path)
    runner_sessions = set()
    for names in sessions.values():
        runner_sessions.update(names)

    windows = defaultdict(list)
    for name, (calls, results) in transcripts.items():
        if name in runner_sessions:
            continue
        for unit, window in interactive_windows(calls, results).items():
            windows[unit].extend(window)

    skipped, unmeasured = 0, 0
    per_unit, per_path = [], defaultdict(lambda: [0, 0])
    totals = {"calls": 0, "extra": 0, "tokens": 0, "extra_tokens": 0}
    for unit in auditable_units(rows, logs, args.limit):
        calls, origins = [], []
        if logs.get(unit):
            for path in logs[unit]:
                calls.extend(pi_log(path))
            origins.append("log")
        if windows.get(unit):
            calls.extend(windows[unit])
            origins.append("window")
        recorded = [name for name in sessions.get(unit, []) if name in transcripts]
        if recorded:
            origins.append("session")
            for name in recorded:
                calls.extend(transcripts[name][0])
        if not calls:
            skipped += 1
            continue
        record = audit_unit(root, unit, calls, unit_anchor(root, unit))
        record["source"] = "+".join(origins)
        if record["refs"] < 0:
            unmeasured += 1
            per_unit.append(record)
            continue
        for path, size in record.pop("extra_by_path").items():
            per_path[path][0] += size
            per_path[path][1] += 1
        for key in ("calls", "extra", "tokens", "extra_tokens"):
            totals[key] += record[key]
        per_unit.append(record)

    top = sorted(((size[0], size[1], path) for path, size in per_path.items()),
                 key=lambda item: (-item[0], item[2]))[:args.top]
    share_calls = 100.0 * totals["extra"] / max(1, totals["calls"])
    share_tokens = 100.0 * totals["extra_tokens"] / max(1, totals["tokens"])
    base_lines, base_data = base_ctx_report(root, rows, args.jump.split(","))

    if args.json:
        print(json.dumps({
            "units": per_unit,
            "top": [{"path": path, "tokens": tokens(size), "units": count}
                    for size, count, path in top],
            "share": {"reads": totals["extra"], "all_reads": totals["calls"],
                      "reads_pct": round(share_calls, 1),
                      "tokens": totals["extra_tokens"],
                      "all_tokens": totals["tokens"],
                      "tokens_pct": round(share_tokens, 1)},
            "skipped_units": skipped,
            "base_ctx": base_data,
        }, indent=1))
        return

    for record in per_unit:
        print("unit %s source %s commit %s%s read_list %s reads %d extra %d "
              "tokens %d extra_tokens %d files: %s" % (
                  record["unit"], record["source"], record["commit"],
                  " (plan close)" if record["anchor"] == "close" else
                  " (working tree)" if record["anchor"] == "worktree" else "",
                  record["read_list"] if record["refs"] >= 0 else "-",
                  record["calls"], record["extra"],
                  record["tokens"], record["extra_tokens"],
                  "; ".join("%s %d" % (item["path"], item["tokens"])
                            for item in record["extra_files"]) or "-"))
    for rank, (size, count, path) in enumerate(top, 1):
        print("top %d %d %d %s" % (rank, tokens(size), count, path))
    print("share reads %d/%d (%.1f%%) tokens %d/%d (%.1f%%)"
          " (of %d units with a plan file)" % (
              totals["extra"], totals["calls"], share_calls,
              totals["extra_tokens"], totals["tokens"],
              share_tokens, len(per_unit)))
    print("skipped units %d (no transcript window and no pi .out log), "
          "unmeasured units %d (the plan file of the unit is not in history)" % (
              skipped, unmeasured))
    for line in base_lines:
        print(line)


if __name__ == "__main__":
    main()
