// Command pim 启动本地 provider/model selection TUI。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/limars874/pim/apply"
	"github.com/limars874/pim/library"
	"github.com/limars874/pim/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pim:", err)
		os.Exit(1)
	}
}

func run() error {
	paths, err := library.DefaultPaths()
	if err != nil {
		return err
	}
	if agentDir := os.Getenv("PI_CODING_AGENT_DIR"); agentDir != "" {
		paths.AgentDir = agentDir
	}

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
