package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
)

const checkpointVersion = 1

// Checkpoint is the durable, secret-free state needed to inspect or recover a
// controlled run. Log paths are relative to the checkout.
type Checkpoint struct {
	Version  int    `json:"version"`
	Plan     string `json:"plan"`
	Unit     string `json:"unit,omitempty"`
	Head     string `json:"head"`
	PlanHash string `json:"plan_fingerprint"`
	Stage    string `json:"stage"`
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Session  string `json:"session_id,omitempty"`
	Calls    int    `json:"calls"`
	Tokens   int    `json:"tokens"`
	// Remaining is the time left of the interrupted agent call's timeout. A
	// resume spends this remainder before the attempt gets its full limit
	// again; the pause itself never counts against it.
	Remaining float64        `json:"timeout_remaining_seconds,omitempty"`
	Logs      []string       `json:"logs,omitempty"`
	Counts    map[string]int `json:"counts,omitempty"`
}

func checkpointPath(root string) (string, error) {
	state, err := cycle.StateDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(state), "tui", "checkpoint.json"), nil
}

func PlanFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func WriteCheckpoint(root string, cp Checkpoint) error {
	path, err := checkpointPath(root)
	if err != nil {
		return err
	}
	if cp.Plan == "" || cp.Head == "" || cp.Stage == "" {
		return errors.New("runner: checkpoint requires plan, HEAD, and stage")
	}
	cp.Version = checkpointVersion
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadCheckpoint(root string) (Checkpoint, error) {
	path, err := checkpointPath(root)
	if err != nil {
		return Checkpoint{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return cp, fmt.Errorf("runner: invalid checkpoint: %w", err)
	}
	if cp.Version != checkpointVersion {
		return cp, fmt.Errorf("runner: unsupported checkpoint version %d", cp.Version)
	}
	if cp.Plan == "" || cp.Head == "" || cp.Stage == "" || strings.Contains(cp.Session, "\n") {
		return cp, errors.New("runner: invalid checkpoint fields")
	}
	return cp, nil
}

// writePauseCheckpoint records the boundary of a Pause and reports whether it
// wrote one. Without a host or outside StatePausing it writes nothing, and the
// caller publishes the state event only when this returned true: a signal or a
// canceled parent context must never read as a pause.
func writePauseCheckpoint(ctx context.Context, root, planID, unit, stage string, route agents.Route, session string, calls, tokens int, remaining time.Duration, logs []string) (bool, error) {
	host := controlledHost(ctx)
	if host == nil || host.State() != StatePausing {
		return false, nil
	}
	headCmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	head, err := headCmd.Output()
	if err != nil {
		return false, fmt.Errorf("runner: checkpoint HEAD: %w", err)
	}
	fingerprint, err := planFingerprint(root, planID)
	if err != nil {
		return false, err
	}
	if err := host.checkpoint(root, Checkpoint{
		Plan: planID, Unit: unit, Head: strings.TrimSpace(string(head)), PlanHash: fingerprint,
		Stage: stage, Agent: route.Agent, Model: route.Model, Effort: route.Effort,
		Session: session, Calls: calls, Tokens: tokens, Remaining: remaining.Seconds(),
		Logs: checkpointLogs(root, logs), Counts: map[string]int{"calls": calls, "tokens": tokens},
	}); err != nil {
		return false, err
	}
	return true, nil
}

// planFingerprint hashes the plan file a checkpoint belongs to.
func planFingerprint(root, planID string) (string, error) {
	plan, err := cycle.FindPlan(root, planID)
	if err != nil {
		return "", fmt.Errorf("runner: checkpoint plan %s: %w", planID, err)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(plan.Path)))
	if err != nil {
		return "", err
	}
	return PlanFingerprint(data), nil
}

// checkpointLogs names the call logs relative to the checkout. macOS puts a
// symlink in front of a temporary home and git resolves it, so the root the
// runner was given and the run directory it reads back are two spellings of one
// path, and a plain Rel of the two reads as a ladder of "..". The ladder is
// relative to the root its writer used, so it is joined back under that root
// first; the pair of spellings that puts the log under the checkout wins.
func checkpointLogs(root string, logs []string) []string {
	if len(logs) == 0 {
		return logs
	}
	base := filepath.Clean(root)
	bases := []string{base}
	if resolved, err := filepath.EvalSymlinks(base); err == nil && resolved != base {
		bases = append(bases, resolved)
	}
	out := make([]string, 0, len(logs))
	for _, log := range logs {
		path := log
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		clean := filepath.Clean(path)
		resolved := clean
		if where, err := filepath.EvalSymlinks(filepath.Dir(clean)); err == nil {
			resolved = filepath.Join(where, filepath.Base(clean))
		}
		best := filepath.ToSlash(log)
	found:
		for _, where := range bases {
			for _, target := range []string{resolved, clean} {
				rel, err := filepath.Rel(where, target)
				if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
					continue
				}
				best = filepath.ToSlash(rel)
				break found
			}
		}
		out = append(out, best)
	}
	return out
}
