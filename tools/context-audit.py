#!/usr/bin/env python3
"""Context growth audit: measure every input docflow assembles for an agent
and project it to a repository with 200 closed plans.

Run from the repository root:

    python3 tools/context-audit.py            # table
    python3 tools/context-audit.py --json     # the same rows as JSON
    python3 tools/context-audit.py --check    # exit 1 on a budget breach

Proxy tokens are bytes / 4, the same proxy as `docflow lint`: it is close to
the real token count for English (~4 bytes per token) and for Russian
(~2 bytes per character, ~2 characters per token).

Rows:
  start       `docflow status` output plus every `start_read` file
  start_file  one `start_read` file
  skill_desc  the frontmatter `description:` of a skill (always in an agent's
              context when the skill is installed)
  skill       the body of a skill
  skills_all  every skill description together
  design      `docs/design/<topic>.md` and `docs/usage.md`: size, the archive
              entries it links, the bytes of their `## Gotchas`
  docflow     `docs/docflow.md` total and its `## Backlog` section
  prompt      `docflow prompt` of fixture units built in a temporary copy of
              the repository: a regular unit that `Read:` a whole
              `docs/design/runner.md` and one line range of it, the same unit
              with only ranged reads, and an acceptance unit

`--check` fails (exit 1) when at the target number of plans the start reads
exceed `start_budget`, when all skill descriptions together exceed 1500
tokens, or when the regular-unit prompt with only ranged reads exceeds half of
`read_budget`.

Projection. Per-plan growth is measured over the plans closed after the
history squash: `git log` puts every archive entry up to `--window-plan`
(P035 by default) in one commit, so growth before that commit is not
measurable per plan. Size rows take the endpoints of that window. Archive
`## Gotchas` growth uses the mean per-plan growth of every linked entry plus
the mean `## Gotchas` size times the number of entries newly linked per plan —
a fixture plan of a prompt row does not exist inside the window, so its rows
are modelled from the sources its `Read:` items reference.
`at200` = now + per-plan * (target plans - plans closed now).
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

TOKEN_DIVISOR = 4
SKILL_DESC_BUDGET = 1500
DEFAULT_TARGET = 200
DEFAULT_WINDOW = "P035"

FENCE_RE = re.compile(r"^\s*(```|~~~)")
HEADING_RE = re.compile(r"^(#{1,6})[ \t]+(.*?)[ \t#]*$")
META_RE = re.compile(r"^([A-Za-z0-9_]+):[ \t]*(.*)$")
REF_RE = re.compile(r"^(.+?)(?::L([0-9]+)-([0-9]+)|#(.+))?\Z", re.S)
MARKDOWN_LINK_RE = re.compile(r"^\[[^\]]*\]\([^)]*\)$")
# Mirrors format.ARCHIVE_LINK_RE: an archive/ link on a word boundary.
ARCHIVE_RE = re.compile(r"(?:docs/|\.\./|^|[^\w/.-])archive/(P[0-9]+-[\w.-]*?\.md)")

FIXTURE_ID = "P999"
FIXTURE_PLAN = """---
id: P999
title: Context audit fixture
type: perf
status: active
created: 2026-01-01
---
## Goal

Measure the prompt of a unit that reads a whole design topic and of the same
unit narrowed to ranges.

## Context

- docs/design/cycle.md:L77-126

## Constraints

- Tools in `tools/` are `bash` 3.2 or `python3` stdlib only.

## Traps

- `docflow prompt` appends `## Archive gotchas` of every entry linked from
  the plan's Context, Decisions and from the text each `Read:` item names.

## Decisions

- Bound the per-unit cost by narrowing references, not by capping Gotchas.

## Phase 1: Measure

### [~] P999.1.1 Whole read of a topic
Read:
- docs/design/runner.md
- docs/design/runner.md:L10-12
Do:
- Nothing, the unit is a measurement fixture.
Verify: `docflow lint`
Depends: -
Commit: P999.1.1: fixture whole read

### [~] P999.1.2 Ranged read of the same topic
Read:
- docs/design/runner.md:L10-12
- docs/design/cycle.md:L94-126
Do:
- Nothing, the unit is a measurement fixture.
Verify: `docflow lint`
Depends: -
Commit: P999.1.2: fixture ranged read

## Phase 2: Acceptance

### [~] P999.2.1 Accept the fixture
Read:
- docs/design/runner.md
- docs/design/runner.md:L10-12
Do:
- Nothing, the unit is a measurement fixture.
Verify: `docflow lint`
Role: acceptance
Depends: -
Commit: P999.2.1: accept fixture
"""

# name, unit id, role, sources: (ref, whole-file share of its links)
FIXTURE_UNITS = [
    ("prompt:regular:whole+range", "P999.1.1", "regular",
     ["docs/design/runner.md", "docs/design/runner.md:L10-12"]),
    ("prompt:regular:ranged", "P999.1.2", "regular",
     ["docs/design/runner.md:L10-12", "docs/design/cycle.md:L94-126"]),
    ("prompt:acceptance:whole+range", "P999.2.1", "acceptance",
     ["docs/design/runner.md", "docs/design/runner.md:L10-12"]),
]


def tokens(nbytes):
    return int(nbytes) // TOKEN_DIVISOR


def read(path):
    try:
        with open(path, "rb") as handle:
            return handle.read().decode("utf-8")
    except (IOError, OSError):
        return None


def git(root, *args, **kwargs):
    stdin = kwargs.get("stdin")
    return subprocess.run(["git", "-C", root] + list(args), input=stdin,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def lines_of(text):
    return text.splitlines(keepends=True)


def fence_mask(lines):
    """Lines inside a fenced code block, mirroring format.FenceMask."""
    mask = [False] * len(lines)
    fence = None
    for i, line in enumerate(lines):
        match = FENCE_RE.match(line.rstrip("\n"))
        if fence is None:
            if match:
                fence = match.group(1)
                mask[i] = True
            continue
        mask[i] = True
        if match and match.group(1) == fence:
            fence = None
    return mask


def headings(lines):
    mask = fence_mask(lines)
    found = []
    for i, line in enumerate(lines):
        if mask[i]:
            continue
        match = HEADING_RE.match(line.rstrip("\n"))
        if match:
            found.append((i, len(match.group(1)), match.group(2)))
    return found


def slug(text):
    kept = "".join(c for c in text.strip().lower()
                   if c.isalnum() or c in " -_")
    return kept.replace(" ", "-")


def section_span(text, name):
    """[start, stop) line indices of a heading section, start = the heading."""
    lines = lines_of(text)
    found = headings(lines)
    for k, (i, level, title) in enumerate(found):
        if title.strip() != name:
            continue
        stop = len(lines)
        for (j, level_j, _) in found[k + 1:]:
            if level_j <= level:
                stop = j
                break
        return (i, stop)
    return None


def section_body(text, name):
    span = section_span(text, name)
    if span is None:
        return ""
    return "".join(lines_of(text)[span[0] + 1:span[1]])


def anchor_span(lines, anchor):
    found = headings(lines)
    counts = {}
    for k, (i, level, title) in enumerate(found):
        key = slug(title)
        seen = counts.get(key, 0)
        counts[key] = seen + 1
        if seen:
            key = "%s-%d" % (key, seen)
        if key != anchor:
            continue
        stop = len(lines)
        for (j, level_j, _) in found[k + 1:]:
            if level_j <= level:
                stop = j
                break
        return (i, stop)
    return None


def parse_ref(text):
    """path, (start, end) and anchor of a `path`, `path:L1-9`, `path#anchor`."""
    bare = text.strip().strip("`")
    if MARKDOWN_LINK_RE.match(bare):
        return (None, None, None)
    match = REF_RE.match(bare)
    if not match:
        return (None, None, None)
    path = match.group(1)
    rng = None
    if match.group(2) is not None:
        rng = (int(match.group(2)), int(match.group(3)))
    return (path, rng, match.group(4))


def ref_slice(text, ref):
    """The referenced part of a file's text: full text, a range, an anchor."""
    _, rng, anchor = ref
    lines = lines_of(text)
    if anchor:
        span = anchor_span(lines, anchor)
        if span is None:
            return ""
        return "".join(lines[span[0]:span[1]])
    if rng:
        start = max(0, rng[0] - 1)
        return "".join(lines[start:min(len(lines), rng[1])])
    return text


def ref_bytes(root, ref_text):
    """(bytes of the referenced part, path) of one `Read:`/`start_read` item."""
    path, rng, anchor = parse_ref(ref_text)
    if not path:
        return (0, ref_text)
    content = read(os.path.join(root, path))
    if content is None:
        return (0, path)
    if rng is None and not anchor:
        return (len(content.encode("utf-8")), path)
    return (len(ref_slice(content, (path, rng, anchor)).encode("utf-8")), path)


def ref_path_of(ref_text):
    path, _, _ = parse_ref(ref_text)
    return path


def linked_ids(text):
    ids = set()
    for match in ARCHIVE_RE.finditer(text):
        ids.add(match.group(1).split("-")[0])
    return ids


def entry_path(archive_paths, plan_id):
    for path in archive_paths:
        if os.path.basename(path).startswith(plan_id + "-"):
            return path
    return None


def frontmatter(text):
    """(meta, first body line index). Quoted values lose their quotes."""
    lines = lines_of(text)
    if not lines or lines[0].strip() != "---":
        return {}, 0
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            meta = {}
            for line in lines[1:i]:
                match = META_RE.match(line.rstrip("\n"))
                if match:
                    meta[match.group(1)] = match.group(2).strip().strip('"')
            return meta, i + 1
    return {}, 0


def blob_snapshot(root, commit, prefixes):
    """path -> text of every blob tracked under `prefixes` at `commit`."""
    listing = git(root, "ls-tree", "-r", commit, "--", *prefixes).stdout.decode(
        "utf-8", "replace")
    entries = []
    for line in listing.splitlines():
        meta, _, path = line.partition("\t")
        parts = meta.split()
        if len(parts) == 3 and parts[1] == "blob":
            entries.append((parts[2], path))
    if not entries:
        return {}
    raw = git(root, "cat-file", "--batch",
              stdin=("\n".join(sha for sha, _ in entries) + "\n").encode()).stdout
    blobs = {}
    pos = 0
    for _, path in entries:
        newline = raw.find(b"\n", pos)
        if newline < 0:
            break
        header = raw[pos:newline].split()
        pos = newline + 1
        if len(header) != 3 or header[1] != b"blob":
            continue
        size = int(header[2])
        blobs[path] = raw[pos:pos + size].decode("utf-8", "replace")
        pos += size + 1
    return blobs


def close_commits(root):
    """[(commit, [archive files added])], oldest first."""
    out = git(root, "log", "--reverse", "--diff-filter=A", "--name-only",
              "--pretty=format:C %H", "--", "docs/archive/").stdout.decode(
                  "utf-8", "replace")
    closes = []
    for line in out.splitlines():
        if line.startswith("C "):
            closes.append((line.split()[1], []))
        elif line.startswith("docs/archive/") and closes:
            closes[-1][1].append(line)
    return closes


def growth_window(root, window_plan):
    """(baseline commit, plans closed in the window).

    The public-release squash puts every archive entry up to `window_plan` in
    one commit, so per-plan growth is measured from `window_plan` on.
    """
    closes = close_commits(root)
    index = None
    for i, (_, files) in enumerate(closes):
        if any(os.path.basename(f).startswith(window_plan + "-") for f in files):
            index = i
            break
    if index is None:
        sys.exit("docs/archive has no %s entry: pass --window-plan <id> of the "
                 "first plan closed after the history squash" % window_plan)
    plans = sum(len(files) for _, files in closes[index:])
    return closes[index][0] + "^", plans


def gotchas_bytes(text):
    """Bytes of an archive entry's `## Gotchas` body, 0 when it has none."""
    if text is None:
        return 0
    return len(section_body(text, "Gotchas").encode("utf-8"))


def gotchas_of(blobs, plan_id):
    """`## Gotchas` bytes of `plan_id` in a path -> text map of an archive."""
    path = entry_path([p for p in blobs if "/archive/" in "/" + p], plan_id)
    if path is None:
        return 0
    return gotchas_bytes(blobs[path])


def design_topics(root):
    topics = []
    for name in sorted(os.listdir(os.path.join(root, "docs", "design"))):
        if name.endswith(".md"):
            topics.append("docs/design/" + name)
    if os.path.exists(os.path.join(root, "docs", "usage.md")):
        topics.append("docs/usage.md")
    return topics


def start_files(meta):
    return [item.strip() for item in meta.get("start_read", "").split(",")
            if item.strip()]


def skill_files(root):
    skills = os.path.join(root, "skills")
    if not os.path.isdir(skills):
        return []
    out = []
    for name in sorted(os.listdir(skills)):
        path = os.path.join("skills", name, "SKILL.md")
        if os.path.isfile(os.path.join(root, path)):
            out.append(path)
    return out


def split_skill(text):
    """(bytes of the frontmatter description, bytes of the body)."""
    meta, body_start = frontmatter(text)
    desc = meta.get("description", "")
    body = "".join(lines_of(text)[body_start:])
    return len(desc.encode("utf-8")), len(body.encode("utf-8"))


def copy_tracked(root, dest):
    files = [f for f in git(root, "ls-files", "-z").stdout.decode().split("\0") if f]
    for name in files:
        source = os.path.join(root, name)
        if not os.path.isfile(source):
            continue
        target = os.path.join(dest, name)
        os.makedirs(os.path.dirname(target), exist_ok=True)
        shutil.copy2(source, target)


def fixture_repo(root):
    """A temporary copy of the tracked tree with the fixture plan, as a git
    repository: `docflow prompt` needs a repository and resolves `Read:`
    against the copy, so the fixture's archive links resolve."""
    tmp = tempfile.mkdtemp(prefix="context-audit-")
    repo = os.path.join(tmp, "repo")
    os.makedirs(repo)
    copy_tracked(root, repo)
    with open(os.path.join(repo, "docs", "plans", FIXTURE_ID + "-audit-fixture.md"),
              "w", encoding="utf-8") as handle:
        handle.write(FIXTURE_PLAN)
    if git(repo, "init", "-q").returncode != 0:
        shutil.rmtree(tmp, ignore_errors=True)
        sys.exit("git init failed in %s" % repo)
    return tmp, repo


def prompt_bytes(docflow, repo, unit_id):
    """Bytes of `docflow prompt <unit>`; statuses stay `[~]`, so the unit is
    not gated on a clean tree or on `Depends:`."""
    result = subprocess.run([docflow, "prompt", unit_id], cwd=repo,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode != 0:
        sys.exit("docflow prompt %s failed (%d): %s" % (
            unit_id, result.returncode,
            result.stderr.decode("utf-8", "replace").strip()))
    return len(result.stdout)


def find_docflow(root, override):
    if override:
        return override
    found = shutil.which("docflow")
    if found:
        return found
    for candidate in (os.path.join(root, "bin", "docflow"),
                      os.path.join(root, "bin", "docflow.exe")):
        if os.path.exists(candidate):
            return candidate
    sys.exit("docflow not in PATH and no bin/docflow: build it with "
             "go build -o bin/docflow ./cmd/docflow")


def status_output(docflow, root):
    result = subprocess.run([docflow, "status"], cwd=root,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode > 1:
        sys.exit("docflow status failed (%d): %s" % (
            result.returncode, result.stderr.decode("utf-8", "replace").strip()))
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--json", action="store_true", help="print the rows as JSON")
    parser.add_argument("--check", action="store_true", help="exit 1 on a budget breach")
    parser.add_argument("--plans", type=int, default=DEFAULT_TARGET,
                        help="target number of closed plans (default %d)" % DEFAULT_TARGET)
    parser.add_argument("--window-plan", default=DEFAULT_WINDOW,
                        help="first plan closed after the history squash (default %s)"
                             % DEFAULT_WINDOW)
    parser.add_argument("--docflow", help="docflow binary (default: PATH, then bin/docflow)")
    args = parser.parse_args()

    root = os.path.abspath(os.getcwd())
    if not os.path.isfile(os.path.join(root, "docs", "docflow.md")):
        sys.exit("run from the repository root: no docs/docflow.md here")
    docflow = find_docflow(root, args.docflow)

    docflow_text = read(os.path.join(root, "docs", "docflow.md"))
    meta, _ = frontmatter(docflow_text)
    start_budget = int(meta.get("start_budget", "0") or 0)
    read_budget = int(meta.get("read_budget", "0") or 0)

    archive_now = sorted(name for name in os.listdir(os.path.join(root, "docs", "archive"))
                         if name.endswith(".md"))
    closed = len(archive_now)
    remaining = max(0, args.plans - closed)
    base, window_plans = growth_window(root, args.window_plan)
    base_blobs = blob_snapshot(root, base, ["docs/", "README.md", "skills"])

    archive_now_blobs = {"docs/archive/" + name:
                         read(os.path.join(root, "docs", "archive", name))
                         for name in archive_now}
    # Per-plan growth of one archive entry's `## Gotchas`.
    entry_growth = {}
    gotchas_now = {}
    for name in archive_now:
        plan_id = name.split("-")[0]
        now = gotchas_bytes(archive_now_blobs["docs/archive/" + name])
        gotchas_now[plan_id] = now
        entry_growth[plan_id] = (now - gotchas_of(base_blobs, plan_id)) / float(window_plans)
    mean_gotchas = sum(gotchas_now.values()) / float(max(1, len(archive_now)))

    def per_plan(now, was):
        return (now - was) / float(window_plans)

    rows = []

    def add_row(name, kind, nbytes, growth=0.0, entries=0, gotchas=0,
                gotchas_growth=0.0, limit=None, detail=""):
        rows.append({
            "name": name,
            "kind": kind,
            "tokens": tokens(nbytes),
            "per_plan": round(growth / TOKEN_DIVISOR, 2),
            "tokens_at200": tokens(nbytes + growth * remaining),
            "entries": entries,
            "gotchas": tokens(gotchas),
            "gotchas_per_plan": round(gotchas_growth / TOKEN_DIVISOR, 2),
            "gotchas_at200": tokens(gotchas + gotchas_growth * remaining),
            "limit": limit,
            "detail": detail,
        })

    # 1. Start reads: the `docflow status` output plus every start_read file.
    #    `docflow status` lists active plans only, so it does not grow with the
    #    number of closed plans.
    status_bytes = len(status_output(docflow, root))
    start_growth = 0.0
    start_parts = []
    for ref in start_files(meta):
        nbytes, path = ref_bytes(root, ref)
        _, rng, anchor = parse_ref(ref)
        was = 0
        base_text = base_blobs.get(path)
        if base_text is not None:
            if rng is not None or anchor:
                was = len(ref_slice(base_text, (path, rng, anchor)).encode("utf-8"))
            else:
                was = len(base_text.encode("utf-8"))
        growth = per_plan(nbytes, was)
        start_growth += growth
        start_parts.append("%s:~%d" % (path, tokens(nbytes)))
        add_row(path, "start_file", nbytes, growth=growth)
    add_row("start-reads", "start", status_bytes + sum(
        ref_bytes(root, ref)[0] for ref in start_files(meta)),
        growth=start_growth, limit=start_budget,
        detail="docflow status ~%d tokens; %s" % (
            tokens(status_bytes), ", ".join(start_parts) or "no start_read"))

    # 2. Skills: every description (in an agent's context when installed) and body.
    desc_total, desc_growth = 0, 0.0
    for path in skill_files(root):
        text = read(os.path.join(root, path))
        desc, body = split_skill(text)
        base_text = base_blobs.get(path)
        base_desc, base_body = split_skill(base_text) if base_text else (0, 0)
        desc_total += desc
        desc_growth += per_plan(desc, base_desc)
        add_row(path + "#description", "skill_desc", desc,
                growth=per_plan(desc, base_desc))
        add_row(path, "skill", body, growth=per_plan(body, base_body))
    add_row("skills:descriptions", "skills_all", desc_total, growth=desc_growth,
            limit=SKILL_DESC_BUDGET, detail="every installed skill description")

    # 3. Design topics: size, the entries they link, the bytes of their Gotchas.
    for topic in design_topics(root):
        text = read(os.path.join(root, topic))
        nbytes = len(text.encode("utf-8"))
        ids = linked_ids(text)
        gotchas = sum(gotchas_of(archive_now_blobs, plan_id) for plan_id in ids)
        base_text = base_blobs.get(topic)
        base_ids = linked_ids(base_text) if base_text is not None else set()
        base_gotchas = sum(gotchas_of(base_blobs, plan_id) for plan_id in base_ids)
        add_row(topic, "design", nbytes,
                growth=per_plan(nbytes, len(base_text.encode("utf-8")) if base_text else 0),
                entries=len(ids), gotchas=gotchas,
                gotchas_growth=per_plan(gotchas, base_gotchas))

    # 4. docs/docflow.md: total and the Backlog section.
    backlog = len(section_body(docflow_text, "Backlog").encode("utf-8"))
    base_docflow = base_blobs.get("docs/docflow.md")
    add_row("docs/docflow.md", "docflow", len(docflow_text.encode("utf-8")),
            growth=per_plan(len(docflow_text.encode("utf-8")),
                            len(base_docflow.encode("utf-8")) if base_docflow else 0))
    add_row("docs/docflow.md#backlog", "docflow", backlog,
            growth=per_plan(backlog,
                            len(section_body(base_docflow, "Backlog").encode("utf-8"))
                            if base_docflow else 0))

    # 5. Fixtured `docflow prompt`: whole `Read:` of runner.md against ranges.
    tmp, repo = fixture_repo(root)
    for name, unit_id, _, refs in FIXTURE_UNITS:
        nbytes = prompt_bytes(docflow, repo, unit_id)
        pulled, sources = set(), set()
        for ref in refs:
            path = ref_path_of(ref)
            sources.add(path)
            content = read(os.path.join(repo, path))
            if content is not None:
                pulled |= linked_ids(ref_slice(content, parse_ref(ref)))
        entry_growths = sum(entry_growth.get(plan_id, 0.0) for plan_id in pulled)
        whole_links = set()
        base_links = set()
        for path in sources:
            content = read(os.path.join(repo, path))
            if content is not None:
                whole_links |= linked_ids(content)
            base_text = base_blobs.get(path)
            if base_text is not None:
                base_links |= linked_ids(base_text)
        share = len(pulled) / float(max(1, len(whole_links)))
        growth = (entry_growths
                  + mean_gotchas * per_plan(len(whole_links), len(base_links)) * share)
        add_row(name, "prompt", nbytes, growth=growth, entries=len(pulled),
                gotchas=0, detail="%s: %s" % (unit_id, ", ".join(sorted(pulled))))
    shutil.rmtree(tmp, ignore_errors=True)

    checks = []
    for row in rows:
        if row["kind"] == "start" and row["limit"]:
            checks.append((row["name"], row["tokens_at200"], row["limit"],
                           "start reads at %d plans vs start_budget" % args.plans))
        if row["kind"] == "skills_all":
            checks.append((row["name"], row["tokens"], row["limit"],
                           "skill descriptions now vs %d" % SKILL_DESC_BUDGET))
        if row["name"] == "prompt:regular:ranged" and read_budget:
            checks.append((row["name"], row["tokens_at200"], read_budget // 2,
                           "ranged-only prompt at %d plans vs read_budget / 2"
                           % args.plans))
    failures = [(name, value, limit, why) for name, value, limit, why in checks
                if value > limit]

    if args.json:
        for row in rows:
            row["check"] = ("fail" if row["name"] in [f[0] for f in failures]
                            else ("ok" if any(c[0] == row["name"] for c in checks)
                                  else "n/a"))
        print(json.dumps(rows, ensure_ascii=False, indent=1))
    else:
        print("# context audit: %s" % root)
        print("# docflow %s; proxy tokens = bytes / 4; %d plans closed; "
              "growth window %s..HEAD = %d plans; projection at %d plans"
              % (os.path.basename(docflow), closed, args.window_plan,
                 window_plans, args.plans))
        header = ("%-46s %-11s %8s %8s %9s %5s %8s %8s %9s"
                  % ("name", "kind", "now", "/plan", "at%d" % args.plans,
                     "ent", "gotch", "g/plan", "g-at%d" % args.plans))
        print(header)
        print("-" * len(header))
        for row in rows:
            print("%-46s %-11s %8d %8.1f %9d %5d %8d %8.1f %9d"
                  % (row["name"], row["kind"], row["tokens"], row["per_plan"],
                     row["tokens_at200"], row["entries"], row["gotchas"],
                     row["gotchas_per_plan"], row["gotchas_at200"]))
        for name, value, limit, why in checks:
            print("%-4s %-46s %8d / %d  %s"
                  % ("FAIL" if value > limit else "ok", name, value, limit, why))
        if failures and not args.json:
            print("failing rows:")
            for row in rows:
                if row["name"] in [f[0] for f in failures]:
                    print("  %s: %s" % (row["name"], row["detail"] or
                                         "at %d plans ~%d tokens"
                                         % (args.plans, row["tokens_at200"])))
    if args.check and failures:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
