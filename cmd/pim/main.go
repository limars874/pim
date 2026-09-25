// Command pim 启动本地 provider/model selection TUI。
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/limars874/pim/apply"
	"github.com/limars874/pim/library"
	"github.com/limars874/pim/tui"
)

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, runTUI))
}

func execute(args []string, stdout, stderr io.Writer, launch func(library.Paths) error) int {
	if len(args) > 0 {
		if len(args) == 1 && isHelpArg(args[0]) {
			paths, err := resolvePaths()
			if err != nil {
				fmt.Fprintln(stderr, "pim:", err)
				return 1
			}
			fmt.Fprint(stdout, formatHelp(paths))
			return 0
		}
		fmt.Fprintf(stderr, "pim: unexpected argument %q\nTry 'pim --help' for more information.\n", strings.Join(args, " "))
		return 2
	}

	paths, err := resolvePaths()
	if err != nil {
		fmt.Fprintln(stderr, "pim:", err)
		return 1
	}
	if err := launch(paths); err != nil {
		fmt.Fprintln(stderr, "pim:", err)
		return 1
	}
	return 0
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func resolvePaths() (library.Paths, error) {
	if agentDir := os.Getenv("PI_CODING_AGENT_DIR"); agentDir != "" {
		return library.Paths{AgentDir: agentDir}, nil
	}
	return library.DefaultPaths()
}

func formatHelp(paths library.Paths) string {
	return fmt.Sprintf(`pim - Terminal UI for managing Pi custom provider/model libraries

Usage:
  pim              Launch the interactive TUI
  pim -h, --help   Show this help message

Overview:
  pim keeps the full provider/model inventory in model-library/providers/
  separate from Pi's active models.json. When applying staged changes, it
  backs up the current models.json, replaces it atomically, verifies the
  result with `+"`pi --list-models`"+`, and rolls back on failure.

Environment:
  PI_CODING_AGENT_DIR   Override Pi agent directory (default: ~/.pi/agent)

Resolved Paths:
  Agent dir:      %s
  Active config:  %s
  Provider lib:   %s
  Backup history: %s

TUI Keys:
  Up/Down, j/k    Move cursor
  Space           Toggle focused provider or model
  Enter, Right    Open selected provider's models / Confirm apply
  Esc, Left       Back to providers / Cancel preview
  A               Preview and apply staged selection
  R               Reset staged selection to active models.json
  q               Quit (prompts when selection has unapplied changes)
`, paths.AgentDir, paths.ModelsPath(), paths.LibraryDir(), paths.HistoryDir())
}

func runTUI(paths library.Paths) error {
	source, _, err := library.Bootstrap(paths)
	if err != nil {
		return err
	}
	active, err := library.ReadSelection(paths.ModelsPath())
	if err != nil {
		return err
	}
	service := apply.Service{Paths: paths}
	callback := newApplyCallback(service, source)
	fixCallback := newFixCallback(service, source)
	model, err := tui.NewModel(source, active, callback, fixCallback)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

func newApplyCallback(service apply.Service, source library.Library) tui.ApplyFunc {
	return func(request tui.ApplyRequest) tea.Cmd {
		return func() tea.Msg {
			result, err := service.Apply(context.Background(), source, request.Staged)
			if err != nil {
				return tui.ApplyFailedMsg{Err: err}
			}
			return tui.ApplySucceededMsg{
				Selection: request.Staged,
				Message:   applyMessage("Applied models.json", result.BackupPath),
			}
		}
	}
}

func newFixCallback(service apply.Service, source library.Library) tui.FixFunc {
	return func(request tui.FixRequest) tea.Cmd {
		return func() tea.Msg {
			result, err := service.Apply(context.Background(), source, request.Reconciled)
			if err != nil {
				return tui.FixFailedMsg{Err: err}
			}
			return tui.FixSucceededMsg{
				Selection: request.Reconciled,
				Message:   applyMessage("Fixed models.json", result.BackupPath),
			}
		}
	}
}

func applyMessage(prefix, backupPath string) string {
	if backupPath == "" {
		return prefix
	}
	return prefix + "; backup: " + filepath.Base(backupPath)
}
