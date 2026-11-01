package tui

import (
	"fmt"
	"io"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/term"
)

// Run opens the terminal workspace for the project at root. It refuses
// non-terminal streams: the workspace needs keyboard input and an alternate
// screen, and scripts should keep using the ordinary CLI commands.
func Run(root string, in io.Reader, out io.Writer) error {
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow tui: requires an interactive terminal; use docflow status, docflow prompt or docflow run in scripts (root: %s)", filepath.Clean(root)),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	program := tea.NewProgram(New(root), tea.WithInput(in), tea.WithOutput(out))
	_, err := program.Run()
	return err
}
