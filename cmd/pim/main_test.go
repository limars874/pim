package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/limars874/pim/apply"
	"github.com/limars874/pim/library"
	"github.com/limars874/pim/tui"
)

func TestExecuteHelpPrintsResolvedPathsWithoutLaunchingTUI(t *testing.T) {
	customDir := filepath.Join(t.TempDir(), "custom-agent")
	t.Setenv("PI_CODING_AGENT_DIR", customDir)

	for _, flag := range []string{"-h", "--help", "help"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := execute([]string{flag}, &stdout, &stderr, func(library.Paths) error {
				t.Fatal("launch must not be called for help")
				return nil
			})
			if code != 0 {
				t.Fatalf("execute(%q) code = %d, stderr = %q; want 0", flag, code, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("execute(%q) stderr = %q; want empty", flag, stderr.String())
			}
			out := stdout.String()
			for _, want := range []string{
				"pim - Terminal UI for managing Pi custom provider/model libraries",
				"PI_CODING_AGENT_DIR",
				"Agent dir:      " + customDir,
				"Active config:  " + filepath.Join(customDir, "models.json"),
				"Provider lib:   " + filepath.Join(customDir, "model-library", "providers"),
				"Backup history: " + filepath.Join(customDir, "model-library", "history"),
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("help output missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

func TestExecuteRejectsUnknownArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute([]string{"--unknown"}, &stdout, &stderr, func(library.Paths) error {
		t.Fatal("launch must not be called for unknown argument")
		return nil
	})
	if code != 2 {
		t.Fatalf("execute(--unknown) code = %d; want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q; want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unexpected argument "--unknown"`) {
		t.Fatalf("stderr = %q; want unexpected argument message", stderr.String())
	}
}

func TestExecuteLaunchesTUIWhenNoArgs(t *testing.T) {
	customDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PI_CODING_AGENT_DIR", customDir)

	var stdout, stderr bytes.Buffer
	var gotPaths library.Paths
	code := execute(nil, &stdout, &stderr, func(paths library.Paths) error {
		gotPaths = paths
		return nil
	})
	if code != 0 || gotPaths.AgentDir != customDir {
		t.Fatalf("execute(nil) = code %d, paths %#v; want 0, %q", code, gotPaths, customDir)
	}
}

func TestRepairCallbackAppliesReconciledSelectionSafely(t *testing.T) {
	paths, source, active, old := repairFixture(t)
	verified := false
	service := apply.Service{
		Paths: paths,
		Verify: func(_ context.Context, gotPaths library.Paths, selection library.Selection) error {
			verified = true
			if gotPaths != paths || !reflect.DeepEqual(selection, library.Selection{"acme": {"alpha"}}) {
				t.Fatalf("verifier input = %#v, %#v", gotPaths, selection)
			}
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 5, 1, 2, 3, 4, time.UTC) },
	}
	model := newRepairModel(t, source, active, newFixCallback(service, source))

	model, command := pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil || !model.FixPreviewOpen() {
		t.Fatalf("conflict Enter = command %v, preview %v; want preview only", command != nil, model.FixPreviewOpen())
	}
	current, err := os.ReadFile(paths.ModelsPath())
	if err != nil || string(current) != string(old) {
		t.Fatalf("preview changed models.json: %q, %v", current, err)
	}

	model, command = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("repair confirmation returned no callback command")
	}
	next, _ := model.Update(command())
	model = next.(tui.Model)
	if !verified || model.ConflictOpen() || model.FixPreviewOpen() || model.Dirty() {
		t.Fatalf("repair success state = verified %v, conflict %v, preview %v, dirty %v", verified, model.ConflictOpen(), model.FixPreviewOpen(), model.Dirty())
	}
	if got := model.StagedSelection(); !reflect.DeepEqual(got, library.Selection{"acme": {"alpha"}}) {
		t.Fatalf("selection after repair = %#v", got)
	}

	history, err := os.ReadDir(filepath.Join(paths.AgentDir, "model-library", "history"))
	if err != nil || len(history) != 1 {
		t.Fatalf("history entries = %#v, %v; want one backup", history, err)
	}
	backup, err := os.ReadFile(filepath.Join(paths.AgentDir, "model-library", "history", history[0].Name()))
	if err != nil || string(backup) != string(old) {
		t.Fatalf("history backup = %q, %v; want original bytes", backup, err)
	}
	selection, err := library.ReadSelection(paths.ModelsPath())
	if err != nil || !reflect.DeepEqual(selection, library.Selection{"acme": {"alpha"}}) {
		t.Fatalf("applied selection = %#v, %v", selection, err)
	}
}

func TestRepairCallbackFailuresKeepPreviewAndRollback(t *testing.T) {
	tests := []struct {
		name    string
		service func(library.Paths) apply.Service
	}{
		{
			name: "verification failure",
			service: func(paths library.Paths) apply.Service {
				return apply.Service{
					Paths: paths,
					Verify: func(context.Context, library.Paths, library.Selection) error {
						return errors.New("injected verifier failure")
					},
				}
			},
		},
		{
			name: "committed replace failure",
			service: func(paths library.Paths) apply.Service {
				agentSyncs := 0
				return apply.Service{
					Paths: paths,
					Verify: func(context.Context, library.Paths, library.Selection) error {
						t.Fatal("verifier must not run after replace failure")
						return nil
					},
					SyncDirectory: func(dir string) error {
						if dir == paths.AgentDir {
							agentSyncs++
							if agentSyncs == 2 {
								return errors.New("injected replace sync failure")
							}
						}
						return nil
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, source, active, old := repairFixture(t)
			model := newRepairModel(t, source, active, newFixCallback(tt.service(paths), source))
			model, _ = pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			model, command := pressKey(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			next, _ := model.Update(command())
			model = next.(tui.Model)
			if !model.FixPreviewOpen() || len(model.Conflicts()) == 0 || !strings.Contains(model.View(), "Fix failed:") {
				t.Fatalf("repair failure state = %q", model.View())
			}
			current, err := os.ReadFile(paths.ModelsPath())
			if err != nil || string(current) != string(old) {
				t.Fatalf("failed repair did not roll back original bytes: %q, %v", current, err)
			}
			history, err := os.ReadDir(filepath.Join(paths.AgentDir, "model-library", "history"))
			if err != nil || len(history) != 1 {
				t.Fatalf("failed repair backup evidence = %#v, %v", history, err)
			}
			backup, err := os.ReadFile(filepath.Join(paths.AgentDir, "model-library", "history", history[0].Name()))
			if err != nil || string(backup) != string(old) {
				t.Fatalf("failed repair backup = %q, %v", backup, err)
			}
		})
	}
}

func repairFixture(t *testing.T) (library.Paths, library.Library, library.Selection, []byte) {
	t.Helper()
	paths := library.Paths{AgentDir: t.TempDir()}
	if err := os.MkdirAll(paths.LibraryDir(), 0o700); err != nil {
		t.Fatalf("create provider library: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.LibraryDir(), "acme.json"), []byte(`{"api":"openai-completions","models":[{"id":"alpha"},{"id":"beta"}]}`), 0o600); err != nil {
		t.Fatalf("write provider library: %v", err)
	}
	source, err := library.Load(paths)
	if err != nil {
		t.Fatalf("load source: %v", err)
	}
	old := []byte("{\n  \"providers\": {\n    \"acme\": {\"api\": \"openai-completions\", \"models\": [{\"id\": \"alpha\"}, {\"id\": \"gone\"}]},\n    \"orphan\": {\"api\": \"openai-completions\", \"models\": [{\"id\": \"one\"}]}\n  }\n}\n")
	if err := os.WriteFile(paths.ModelsPath(), old, 0o600); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
	active, err := library.ReadSelection(paths.ModelsPath())
	if err != nil {
		t.Fatalf("read active selection: %v", err)
	}
	return paths, source, active, old
}

func newRepairModel(t *testing.T, source library.Library, active library.Selection, fix tui.FixFunc) tui.Model {
	t.Helper()
	model, err := tui.NewModel(source, active, nil, fix)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	return model
}

func pressKey(t *testing.T, model tui.Model, key tea.KeyMsg) (tui.Model, tea.Cmd) {
	t.Helper()
	next, command := model.Update(key)
	return next.(tui.Model), command
}
