package runner

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
)

// RecoveryConflictError says the saved checkpoint no longer describes this
// checkout: the plan file or HEAD moved since the pause. Resuming silently
// would continue a different unit or replay a completed commit, so the
// interactive host shows this conflict instead.
type RecoveryConflictError struct{ Reason string }

func (e RecoveryConflictError) Error() string { return "runner: recovery conflict: " + e.Reason }

// ValidateResumable rejects a checkpoint whose interrupted operation left no
// session to continue: a replan edit is rolled back and a closing writes
// nothing, so the next run starts from the restored plan instead of resuming.
func ValidateResumable(cp Checkpoint) error {
	switch cp.Stage {
	case "replan", "close":
		return RecoveryConflictError{Reason: fmt.Sprintf("the run paused during %s; the plan is unchanged — start a new run", cp.Stage)}
	}
	return nil
}

// ValidateCheckpoint checks that a saved pause still belongs to the checkout it
// was taken in.
func ValidateCheckpoint(root string, cp Checkpoint) error {
	head, err := currentHead(root)
	if err != nil {
		return err
	}
	if head != cp.Head {
		return RecoveryConflictError{Reason: fmt.Sprintf("HEAD moved from %s to %s since the pause", shortHash(cp.Head), shortHash(head))}
	}
	fingerprint, err := planFingerprint(root, cp.Plan)
	if err != nil {
		return RecoveryConflictError{Reason: fmt.Sprintf("plan %s cannot be read: %v", cp.Plan, err)}
	}
	if cp.PlanHash != "" && fingerprint != cp.PlanHash {
		return RecoveryConflictError{Reason: fmt.Sprintf("plan %s changed since the pause", cp.Plan)}
	}
	return nil
}

func currentHead(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("runner: recovery HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// resumeFor returns the checkpoint when the run is about to continue that
// unit. A completed unit has nothing to resume: the loop moves to the next one.
func resumeFor(checkpoint *Checkpoint, uid string) *Checkpoint {
	if checkpoint == nil || checkpoint.Unit == "" || checkpoint.Unit != uid || checkpoint.Stage == "complete" {
		return nil
	}
	return checkpoint
}

func checkpointRoute(cp Checkpoint) agents.Route {
	return agents.Route{Agent: cp.Agent, Model: cp.Model, Effort: cp.Effort}
}

// recoveryContinuation is the short prompt sent into the paused session. The
// session already owns the full unit prompt and tool history, so this only
// names the interruption.
func recoveryContinuation(cp Checkpoint) string {
	return fmt.Sprintf("Runner: the interactive run was paused during %s of %s. The session is resumed; continue the same unit from the current tree. Repeated steps must not be repeated.\n", stageName(cp.Stage), cp.Unit)
}

// recoveryPrompt is the full prompt for a new session. It carries the reason
// the old session cannot be continued and the working tree the agent inherits,
// so nothing that only existed in the lost session reads as never done.
func recoveryPrompt(root string, cp Checkpoint, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Recovery\n\nThe interactive run paused during %s of %s and is resuming on %s %s%s. A new session starts with the current tree because %s.\n\n", stageName(cp.Stage), cp.Unit, cp.Agent, orDash(cp.Model), effortClause(cp.Effort), reason)
	b.WriteString("Inspect the working tree before acting: work already written by the paused run stands and must not be redone. The unit's commit must not be repeated if it already exists.\n")
	if tree := treeSummary(root); tree != "" {
		b.WriteString("\n### Working tree\n\n")
		b.WriteString(tree)
		b.WriteString("\n")
	}
	return b.String()
}

func stageName(stage string) string {
	switch stage {
	case "agent":
		return "an agent call"
	case "quota":
		return "a provider wait"
	case "verify":
		return "verify"
	case "commit":
		return "the done/commit transaction"
	}
	if stage == "" {
		return "work"
	}
	return stage
}

func effortClause(effort string) string {
	if effort == "" {
		return ""
	}
	return " (" + effort + ")"
}

func treeSummary(root string) string {
	var out strings.Builder
	if diff, err := exec.Command("git", "-C", root, "diff", "--stat", "HEAD").Output(); err == nil {
		if text := strings.TrimRight(string(diff), "\r\n"); text != "" {
			out.WriteString(text)
			out.WriteString("\n")
		}
	}
	if status, err := exec.Command("git", "-C", root, "status", "--porcelain").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimRight(string(status), "\r\n"), "\n") {
			if strings.HasPrefix(line, "?? ") {
				out.WriteString("untracked: ")
				out.WriteString(strings.TrimSpace(line[3:]))
				out.WriteString("\n")
			}
		}
	}
	if out.Len() == 0 {
		return "clean: no uncommitted work"
	}
	return out.String()
}

var resumeRejectedRE = regexp.MustCompile(`(?i)(session|thread|conversation)[^\n]{0,80}(not found|does not exist|unknown|invalid|expired|no such|not exist|rejected|failed to resume|could not resume|unable to resume)`)

// resumeRejected recognizes the provider refusing a saved session id: the
// call then starts one new session instead of failing the attempt.
func resumeRejected(run agentio.AgentRun) bool {
	return resumeRejectedRE.MatchString(run.Stdout) || resumeRejectedRE.MatchString(run.Stderr)
}
