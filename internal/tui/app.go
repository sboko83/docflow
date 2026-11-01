package tui

import (
	"fmt"
	"io"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/term"
)

// Option configures the terminal workspace without changing the plain Run
// entry point tests use.
type Option func(*options)

type options struct {
	skillDirectory string
}

// WithSkillDirectory names the installed docflow skills for the start check.
func WithSkillDirectory(path string) Option {
	return func(o *options) { o.skillDirectory = path }
}

// Run opens the terminal workspace for the project at root. It refuses
// non-terminal streams: the workspace needs keyboard input and an alternate
// screen, and scripts should keep using the ordinary CLI commands.
func Run(root string, in io.Reader, out io.Writer, opts ...Option) error {
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow tui: requires an interactive terminal; use docflow status, docflow prompt or docflow run in scripts (root: %s)", filepath.Clean(root)),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	var options options
	for _, option := range opts {
		option(&options)
	}
	model := New(root)
	model.skillDirectory = options.skillDirectory
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	stopHangup := watchHangup(program)
	defer stopHangup()
	final, runErr := program.Run()
	// SIGTERM becomes a QuitMsg inside Bubble Tea and a hangup is turned into
	// one above, so both return from Run without the exit dialog. A run the
	// workspace still owns is stopped here, after the terminal is restored.
	if stopped, ok := final.(Model); ok {
		if stopErr := stopped.stopAfterSignal(); stopErr != nil {
			if runErr != nil {
				return fmt.Errorf("%w: %w", runErr, stopErr)
			}
			return stopErr
		}
	}
	return runErr
}
