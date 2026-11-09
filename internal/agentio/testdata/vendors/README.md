# Vendor output snapshots

Real output of the agent CLIs docflow drives, cut to a few events and
anonymized. `TestVendorContract` (`internal/agentio/contract_test.go`) parses
each one the way the runner and `docflow stats` do and pins the outcome:
refusal class and reset, quota windows, tokens, cost, result status and the
stats transcript row. Each case also renames the JSON keys it depends on and
requires the outcome to change, so a renamed field fails the test instead of
reading as zero.

File name: `<vendor>/<cli version>_<capture date>_<kind>.<ext>`.

| File | Source | Pins |
|---|---|---|
| `claude/2.1.309_2026-11-02_transcript-window.jsonl` | Claude Code transcript (`~/.claude/projects`), interactive session around `docflow prompt P052.4.3` … `docflow commit P052.4.3` | stats window: requests, sum, base, peak |
| `claude/2.1.317_2026-11-07_transcript-session.jsonl` | Claude Code transcript, first 160 lines of a session | stats row of a runner session |
| `codex/0.165.0_2026-11-05_exec.jsonl` | `codex exec --json` stdout (benchmark run) | thread id, tokens, success |
| `codex/0.165.0_2026-11-05_rollout.jsonl` | rollout of the same thread (`~/.codex/sessions`): `session_meta` and three `token_count` events | quota windows 5h/7d below the limit |
| `pi/1.4.0_2026-10-28_zai-result.jsonl` | `pi -p --mode json` stdout, z.ai, first and last turn | session, tokens, cost, result status |
| `pi/1.4.0_2026-10-28_strata-result.jsonl` | the same, strata provider (cost 0) | session, tokens, cost |
| `pi/1.4.0_2026-10-23_zai-limit-1310.jsonl` | pi stdout of a z.ai weekly limit (code 1310) | `limit` with the reset from the text |
| `opencode/2.2.3_2026-10-30_result.jsonl` | `opencode run --format json` stdout | session, tokens, cost, result status |
| `opencode/2.2.3_2026-10-30_no-route.jsonl` | the same, unknown model | `outage` (`provider.no-route`) |
| `opencode/2.2.3_2026-10-30_usage-limits.json` | `opencode api get /api/experimental/usage/limits` | go allowance windows |
| `dsh/0.2.2-rc.1_2026-11-08_result.jsonl` | `dsh headless --json` stdout | session, tokens, result status |
| `dsh/0.2.2-rc.1_2026-11-08_truncated.jsonl` | the same, truncated tool output | tokens |
| `dsh/0.2.2-rc.1_2026-11-08_auth.jsonl` | the same, invalid API key | failure read as a login failure |
| `dsh/0.2.2-rc.1_2026-11-08_launch-error.jsonl` | the same, unknown flag | failure reason |

No live sample exists yet for: Claude `-p --output-format stream-json`
(result, `rate_limit_event`, rejected limit), `claude -p /usage`, Codex
refusals (usage limit, `rate_limit_reached_type`, outages), pi outages and
throttles (z.ai 1302/1305), OpenCode and dsh limits. Synthetic events in
`refusal_test.go`, `quota_test.go` and `output_test.go` cover those until a
real one is captured.

## Adding a snapshot

When a vendor CLI updates, or a new kind of output shows up live:

1. Take the raw output: the runner keeps each call in
   `<git-dir>/docflow/run/<unit>.<attempt>.out` (stdout between `--- stdout`
   and `--- stderr`); Codex rollouts live in `~/.codex/sessions`, Claude
   transcripts in `~/.claude/projects/<project>`. Note the CLI version
   (`--version`, Codex `session_meta.cli_version`, Claude `version` per line).
2. Keep 1–3 samples per kind and only the events that matter, in order.
3. Anonymize: prompt, answer, thinking and tool texts become `redacted`
   (a result block keeps its keys, `status` and `blocker`); session, message
   and tool ids become stable placeholders, the same id everywhere; paths,
   user names, emails, hosts and account ids go. Keep keys, numbers,
   timestamps and enum values (`type`, `role`, `stopReason`, model names) as
   they were.
4. Add the file under its vendor with the version and date in the name;
   keep the older snapshot: the decoder must read both versions.
5. Add a case to `TestVendorContract` with the expected outcome, computed
   from the snapshot independently of the decoder, and the keys to drift.
6. Run `go test ./internal/agentio/` and `bash tools/audit-public.sh`.
