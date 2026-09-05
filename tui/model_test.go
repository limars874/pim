package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/limars874/pim/library"
)

func TestTwoColumnNavigationAndNarrowLayout(t *testing.T) {
	model := newTestModel(t, library.Selection{"acme": {"alpha"}}, nil)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 36, Height: 8})
	model = next.(Model)

	lines := strings.Split(model.View(), "\n")
	if len(lines) != 8 {
		t.Fatalf("View() lines = %d, want 8", len(lines))
	}
	if !strings.Contains(lines[0], "Providers") || !strings.Contains(lines[0], "Models") {
		t.Fatalf("View() header = %q, want both columns", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "Space") {
		t.Fatalf("View() footer = %q, want high-signal shortcuts", lines[len(lines)-1])
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	if model.providerCursor != 1 {
		t.Fatalf("provider cursor = %d, want 1", model.providerCursor)
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.Focus() != ModelsFocus {
		t.Fatalf("focus = %v, want ModelsFocus", model.Focus())
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.Focus() != ProvidersFocus {
		t.Fatalf("focus after Esc = %v, want ProvidersFocus", model.Focus())
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyRight})
	if model.Focus() != ModelsFocus {
		t.Fatalf("focus after right = %v, want ModelsFocus", model.Focus())
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyLeft})
	if model.Focus() != ProvidersFocus {
		t.Fatalf("focus after left = %v, want ProvidersFocus", model.Focus())
	}
}

func TestViewportsKeepProviderAndModelCursorsVisible(t *testing.T) {
	providers := make([]library.Provider, 12)
	for providerIndex := range providers {
		models := make([]library.Model, 10)
		for modelIndex := range models {
			models[modelIndex] = library.Model{ID: fmt.Sprintf("model-%02d", modelIndex)}
		}
		if providerIndex == 8 {
			models = models[:2]
		}
		providers[providerIndex] = library.Provider{
			ID:     fmt.Sprintf("provider-%02d", providerIndex),
			Models: models,
		}
	}
	model, err := NewModel(library.Library{Providers: providers}, nil, nil)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	next, _ := model.Update(tea.WindowSizeMsg{Width: 50, Height: 5})
	model = next.(Model)

	for range 8 {
		model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.providerCursor != 8 || model.providerOffset != 6 {
		t.Fatalf("provider viewport = cursor %d, offset %d; want 8, 6", model.providerCursor, model.providerOffset)
	}
	if view := model.View(); !strings.Contains(view, "> [ ] provider-08") {
		t.Fatalf("provider cursor is not visible: %q", view)
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyUp})
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	for range 7 {
		model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.modelCursor != 7 || model.modelOffset != 5 {
		t.Fatalf("model viewport = cursor %d, offset %d; want 7, 5", model.modelCursor, model.modelOffset)
	}
	if view := model.View(); !strings.Contains(view, "> [ ] model-07") {
		t.Fatalf("model cursor is not visible: %q", view)
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyLeft})
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	if model.providerCursor != 8 || model.modelCursor != 1 || model.modelOffset != 0 {
		t.Fatalf("switch provider did not clamp model viewport: cursor %d/%d, offset %d", model.providerCursor, model.modelCursor, model.modelOffset)
	}
}

func TestNarrowViewNeverExceedsDeclaredWidth(t *testing.T) {
	for width := 1; width < 5; width++ {
		model := newTestModel(t, library.Selection{"acme": {"alpha"}}, nil)
		next, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 4})
		model = next.(Model)
		assertViewFits(t, model.View(), width)

		model, _ = press(model, runeKey('a'))
		assertViewFits(t, model.View(), width)
		model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
		model, _ = press(model, spaceKey())
		model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
		assertViewFits(t, model.View(), width)
	}
}

func TestProviderThreeStateAndEmptyProviderToggle(t *testing.T) {
	model := newTestModel(t, library.Selection{
		"acme":    {"alpha"},
		"builtin": {},
	}, nil)

	if got := model.providerState(model.providers[0]); got != "[-]" {
		t.Fatalf("acme state = %s, want [-]", got)
	}
	model, _ = press(model, spaceKey())
	if got := model.providerState(model.providers[0]); got != "[x]" {
		t.Fatalf("acme state after Space = %s, want [x]", got)
	}
	model, _ = press(model, spaceKey())
	if got := model.providerState(model.providers[0]); got != "[ ]" {
		t.Fatalf("acme state after second Space = %s, want [ ]", got)
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.providerState(model.providers[1]); got != "[x]" {
		t.Fatalf("builtin state = %s, want [x]", got)
	}
	model, _ = press(model, spaceKey())
	if _, ok := model.StagedSelection()["builtin"]; ok {
		t.Fatal("empty provider remained selected after Space")
	}
	model, _ = press(model, spaceKey())
	if models, ok := model.StagedSelection()["builtin"]; !ok || len(models) != 0 {
		t.Fatalf("empty provider selection = %#v, want present empty collection", model.StagedSelection())
	}
}

func TestRemovingLastModelDisablesProvider(t *testing.T) {
	model := newTestModel(t, library.Selection{"acme": {"alpha"}}, nil)
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = press(model, spaceKey())
	if _, ok := model.StagedSelection()["acme"]; ok {
		t.Fatalf("staged selection retained empty model provider: %#v", model.StagedSelection())
	}
	if got := model.providerState(model.providers[0]); got != "[ ]" {
		t.Fatalf("provider state after removing last model = %s, want [ ]", got)
	}
}

func TestModelToggleAndReset(t *testing.T) {
	initial := library.Selection{"acme": {"alpha"}}
	model := newTestModel(t, initial, nil)

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	model, _ = press(model, spaceKey())
	if got, want := model.StagedSelection(), (library.Selection{"acme": {"alpha", "beta"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("staged after model toggle = %#v, want %#v", got, want)
	}
	if !model.Dirty() {
		t.Fatal("model toggle did not mark state dirty")
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
	model, _ = press(model, runeKey('r'))
	if got := model.StagedSelection(); !reflect.DeepEqual(got, initial) {
		t.Fatalf("staged after reset = %#v, want %#v", got, initial)
	}
	if model.Dirty() {
		t.Fatal("reset left state dirty")
	}
}

func TestDiffSelectionsListsProviderAndModelChanges(t *testing.T) {
	initial := library.Selection{
		"acme":    {"alpha"},
		"builtin": {},
	}
	staged := library.Selection{
		"acme": {"beta"},
		"zen":  {"gamma"},
	}
	want := Diff{
		AddedProviders:   []string{"zen"},
		RemovedProviders: []string{"builtin"},
		AddedModels: []ModelRef{
			{ProviderID: "acme", ModelID: "beta"},
			{ProviderID: "zen", ModelID: "gamma"},
		},
		RemovedModels: []ModelRef{{ProviderID: "acme", ModelID: "alpha"}},
	}
	if got := DiffSelections(initial, staged); !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffSelections() = %#v, want %#v", got, want)
	}
}

func TestPreviewSubmitsApplyRequestThroughSpy(t *testing.T) {
	var received ApplyRequest
	called := false
	spy := func(request ApplyRequest) tea.Cmd {
		called = true
		received = request
		return func() tea.Msg { return struct{}{} }
	}
	initial := library.Selection{"acme": {"alpha"}}
	model := newTestModel(t, initial, spy)

	model, _ = press(model, spaceKey())
	model, _ = press(model, runeKey('a'))
	if !model.PreviewOpen() {
		t.Fatal("A did not open Apply preview")
	}
	if view := model.View(); !strings.Contains(view, "+ model acme/beta") {
		t.Fatalf("preview = %q, want added model", view)
	}

	model, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if !called {
		t.Fatal("Apply callback was not called")
	}
	if command == nil {
		t.Fatal("Apply callback command is nil")
	}
	_ = command()
	want := ApplyRequest{
		Initial: initial,
		Staged:  library.Selection{"acme": {"alpha", "beta"}},
		Diff: Diff{
			AddedModels: []ModelRef{{ProviderID: "acme", ModelID: "beta"}},
		},
	}
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("ApplyRequest = %#v, want %#v", received, want)
	}
	if !model.PreviewOpen() {
		t.Fatal("submit fabricated a successful Apply state transition")
	}
}

func TestApplyInFlightLocksPreviewUntilResult(t *testing.T) {
	calls := 0
	model := newTestModel(t, library.Selection{"acme": {"alpha"}}, func(ApplyRequest) tea.Cmd {
		calls++
		return func() tea.Msg { return nil }
	})
	model, _ = press(model, spaceKey())
	model, _ = press(model, runeKey('a'))
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEsc})
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if calls != 1 || !model.PreviewOpen() || !model.applying {
		t.Fatalf("in-flight Apply accepted input: calls %d, preview %v, applying %v", calls, model.PreviewOpen(), model.applying)
	}
	next, _ := model.Update(ApplyFailedMsg{Err: errors.New("failed")})
	model = next.(Model)
	if model.applying || !model.PreviewOpen() {
		t.Fatalf("failure did not unlock preview: applying %v, preview %v", model.applying, model.PreviewOpen())
	}
}

func TestApplyResultMessagesUpdateTUIState(t *testing.T) {
	model := newTestModel(t, library.Selection{"acme": {"alpha"}}, nil)
	model, _ = press(model, spaceKey())
	model, _ = press(model, runeKey('a'))
	applied := library.Selection{"acme": {"alpha", "beta"}}
	next, _ := model.Update(ApplySucceededMsg{Selection: applied, Message: "Applied with backup"})
	model = next.(Model)
	if model.PreviewOpen() || model.Dirty() || !reflect.DeepEqual(model.StagedSelection(), applied) {
		t.Fatalf("success state = preview %v, dirty %v, staged %#v", model.PreviewOpen(), model.Dirty(), model.StagedSelection())
	}
	if !strings.Contains(model.View(), "Applied with backup") {
		t.Fatalf("success message is not displayed: %q", model.View())
	}

	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = press(model, spaceKey())
	model, _ = press(model, runeKey('a'))
	next, _ = model.Update(ApplyFailedMsg{Err: errors.New("verifier rejected models")})
	model = next.(Model)
	if !model.PreviewOpen() || !model.Dirty() || !strings.Contains(model.View(), "Apply failed: verifier rejected models") {
		t.Fatalf("failure state did not retain preview/staged changes: %q", model.View())
	}
}

func TestLongPreviewScrollsWithinTerminalHeight(t *testing.T) {
	models := make([]library.Model, 10)
	selected := make([]string, len(models))
	for index := range models {
		id := fmt.Sprintf("model-%02d", index)
		models[index] = library.Model{ID: id}
		selected[index] = id
	}
	model, err := NewModel(library.Library{Providers: []library.Provider{{ID: "acme", Models: models}}}, library.Selection{"acme": selected}, nil)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model.staged = library.Selection{"acme": {"model-00"}}
	next, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 4})
	model = next.(Model)
	model, _ = press(model, runeKey('a'))
	if got := len(strings.Split(model.View(), "\n")); got != 4 {
		t.Fatalf("preview height = %d, want 4", got)
	}
	for range 5 {
		model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.previewOffset != 5 {
		t.Fatalf("preview offset = %d, want 5", model.previewOffset)
	}
	if view := model.View(); !strings.Contains(view, "- model acme/model-05") {
		t.Fatalf("scrolled preview does not show expected diff entry: %q", view)
	}
}

func TestDirtyQuitConfirmation(t *testing.T) {
	model := newTestModel(t, library.Selection{"acme": {"alpha"}}, nil)
	model, _ = press(model, spaceKey())
	model, command := press(model, tea.KeyMsg{Type: tea.KeyEsc})
	if command != nil || !model.QuitConfirmationOpen() {
		t.Fatal("dirty Esc did not open quit confirmation")
	}
	if !strings.Contains(model.View(), "Discard staged changes") {
		t.Fatalf("confirmation view = %q", model.View())
	}
	model, _ = press(model, runeKey('n'))
	if model.QuitConfirmationOpen() {
		t.Fatal("n did not cancel quit confirmation")
	}

	model, _ = press(model, runeKey('q'))
	if !model.QuitConfirmationOpen() {
		t.Fatal("dirty q did not open quit confirmation")
	}
	_, command = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("confirmed quit did not return tea.Quit command")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("confirmed quit command result = %T, want tea.QuitMsg", command())
	}
}

func TestNewModelWithoutConflictsKeepsBrowserBehavior(t *testing.T) {
	model := newTestModel(t, library.Selection{"acme": {"alpha"}}, nil)
	if model.ConflictOpen() || model.FixPreviewOpen() {
		t.Fatalf("valid active selection opened conflict UI: %#v", model.Conflicts())
	}
	if !strings.Contains(model.View(), "Providers") {
		t.Fatalf("valid active selection did not render browser: %q", model.View())
	}
}

func TestMissingProviderConflictQuitsWithoutApply(t *testing.T) {
	called := false
	source := library.Library{Providers: []library.Provider{{ID: "acme", Models: []library.Model{{ID: "alpha"}}}}}
	model, err := NewModel(source, library.Selection{"orphan": {"one", "two"}}, func(ApplyRequest) tea.Cmd {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	if !model.ConflictOpen() || !strings.Contains(model.View(), "[!] missing provider orphan") || strings.Contains(model.View(), "orphan/one") {
		t.Fatalf("conflict view = %q", model.View())
	}
	if got := model.FixRequest().Diff; !reflect.DeepEqual(got, Diff{RemovedProviders: []string{"orphan"}}) {
		t.Fatalf("repair diff = %#v, want only provider removal", got)
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	_, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("Quit without changes did not return tea.Quit")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("Quit command result = %T, want tea.QuitMsg", command())
	}
	if called {
		t.Fatal("Quit without changes called Apply")
	}
}

func TestEmptyModelSelectionConflictPreviewsProviderRemoval(t *testing.T) {
	source := library.Library{Providers: []library.Provider{{ID: "acme", Models: []library.Model{{ID: "alpha"}}}}}
	model, err := NewModel(source, library.Selection{"acme": {}}, nil)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	if view := model.View(); !strings.Contains(view, "provider acme has no selected models") {
		t.Fatalf("empty selection conflict = %q", view)
	}
	model, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil || !model.FixPreviewOpen() {
		t.Fatalf("conflict Enter = command %v, preview %v", command != nil, model.FixPreviewOpen())
	}
	if view := model.View(); !strings.Contains(view, "- provider acme") {
		t.Fatalf("repair preview did not show provider removal: %q", view)
	}
}

func TestAllMissingModelsPreviewProviderRemoval(t *testing.T) {
	source := library.Library{Providers: []library.Provider{{ID: "acme", Models: []library.Model{{ID: "alpha"}, {ID: "beta"}}}}}
	model, err := NewModel(source, library.Selection{"acme": {"gone-alpha", "gone-beta"}}, nil)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	if got := model.FixRequest().Diff; !reflect.DeepEqual(got, Diff{RemovedProviders: []string{"acme"}}) {
		t.Fatalf("repair diff = %#v, want provider removal", got)
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if view := model.View(); !strings.Contains(view, "- provider acme") {
		t.Fatalf("repair preview did not show all-missing provider removal: %q", view)
	}
}

func TestFixPreviewDefersSubmissionUntilEnter(t *testing.T) {
	calls := 0
	source := library.Library{Providers: []library.Provider{{
		ID:     "acme",
		Models: []library.Model{{ID: "alpha"}},
	}}}
	model, err := NewModel(source, library.Selection{"acme": {"alpha", "gone"}}, nil, func(FixRequest) tea.Cmd {
		calls++
		return func() tea.Msg { return FixSucceededMsg{Selection: library.Selection{"acme": {"alpha"}}} }
	})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil || !model.FixPreviewOpen() || calls != 0 {
		t.Fatalf("conflict Enter = command %v, preview %v, calls %d; want preview without submission", command != nil, model.FixPreviewOpen(), calls)
	}
	if view := model.View(); !strings.Contains(view, "Enter fix and apply") {
		t.Fatalf("repair footer = %q", view)
	}
	model, command = press(model, tea.KeyMsg{Type: tea.KeyEsc})
	if command != nil || model.FixPreviewOpen() || calls != 0 {
		t.Fatalf("Esc submitted or retained preview: command %v, preview %v, calls %d", command != nil, model.FixPreviewOpen(), calls)
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	_, command = press(model, runeKey('q'))
	if command == nil || calls != 0 {
		t.Fatalf("Quit submitted repair: command %v, calls %d", command != nil, calls)
	}

	withoutCallback, err := NewModel(source, library.Selection{"acme": {"alpha", "gone"}}, nil)
	if err != nil {
		t.Fatalf("NewModel() without callback error = %v", err)
	}
	withoutCallback, _ = press(withoutCallback, tea.KeyMsg{Type: tea.KeyEnter})
	withoutCallback, command = press(withoutCallback, tea.KeyMsg{Type: tea.KeyEnter})
	if command != nil || !strings.Contains(withoutCallback.View(), "Fix callback is not configured") {
		t.Fatalf("nil callback result = command %v, view %q", command != nil, withoutCallback.View())
	}
}

func TestFixSuccessEntersBrowserAndLocksInFlight(t *testing.T) {
	calls := 0
	var received FixRequest
	source := library.Library{Providers: []library.Provider{{
		ID:     "acme",
		Models: []library.Model{{ID: "alpha"}, {ID: "beta"}},
	}}}
	model, err := NewModel(source, library.Selection{"acme": {"alpha", "gone"}}, nil, func(request FixRequest) tea.Cmd {
		calls++
		received = request
		return func() tea.Msg { return FixSucceededMsg{Selection: request.Reconciled, Message: "Fixed models.json"} }
	})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	model, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil || calls != 1 {
		t.Fatalf("repair submit = command %v, calls %d", command != nil, calls)
	}
	model, second := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if second != nil || calls != 1 {
		t.Fatalf("in-flight repair was submitted twice: command %v, calls %d", second != nil, calls)
	}
	if got, want := received, (FixRequest{
		Active:     library.Selection{"acme": {"alpha", "gone"}},
		Reconciled: library.Selection{"acme": {"alpha"}},
		Conflicts:  []library.Conflict{{Kind: library.MissingModelConflict, ProviderID: "acme", ModelID: "gone"}},
		Diff:       Diff{RemovedModels: []ModelRef{{ProviderID: "acme", ModelID: "gone"}}},
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("FixRequest = %#v, want %#v", got, want)
	}
	next, _ := model.Update(command())
	model = next.(Model)
	if model.ConflictOpen() || model.FixPreviewOpen() || model.Dirty() || !strings.Contains(model.View(), "Providers") {
		t.Fatalf("fix success did not enter clean browser: %q", model.View())
	}
	if got := model.StagedSelection(); !reflect.DeepEqual(got, library.Selection{"acme": {"alpha"}}) {
		t.Fatalf("selection after fix = %#v", got)
	}
}

func TestFixFailureRetainsPreviewAndCanRetry(t *testing.T) {
	calls := 0
	source := library.Library{Providers: []library.Provider{{ID: "acme", Models: []library.Model{{ID: "alpha"}}}}}
	model, err := NewModel(source, library.Selection{"acme": {"gone"}}, nil, func(FixRequest) tea.Cmd {
		calls++
		return func() tea.Msg { return FixFailedMsg{Err: errors.New("verifier rejected repair")} }
	})
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	model, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	next, _ := model.Update(command())
	model = next.(Model)
	if !model.FixPreviewOpen() || !strings.Contains(model.View(), "Fix failed: verifier rejected repair") {
		t.Fatalf("fix failure did not retain preview: %q", model.View())
	}
	_, command = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil || calls != 2 {
		t.Fatalf("failed repair did not retry: command %v, calls %d", command != nil, calls)
	}
}

func TestConflictAndFixPreviewFitTinyTerminalHeight(t *testing.T) {
	source := library.Library{Providers: []library.Provider{{ID: "acme", Models: []library.Model{{ID: "alpha"}}}}}
	for height := 1; height <= 3; height++ {
		t.Run(fmt.Sprintf("height-%d", height), func(t *testing.T) {
			model, err := NewModel(source, library.Selection{"acme": {}}, nil)
			if err != nil {
				t.Fatalf("NewModel() error = %v", err)
			}
			next, _ := model.Update(tea.WindowSizeMsg{Width: 12, Height: height})
			model = next.(Model)
			assertViewHeight(t, model.View(), height)
			assertViewFits(t, model.View(), 12)

			model, _ = press(model, tea.KeyMsg{Type: tea.KeyEnter})
			assertViewHeight(t, model.View(), height)
			assertViewFits(t, model.View(), 12)
		})
	}
}

func TestConflictViewFitsNarrowTerminalWithTruncation(t *testing.T) {
	active := make(library.Selection)
	for index := range 20 {
		active[fmt.Sprintf("missing-%02d", index)] = []string{"model"}
	}
	model, err := NewModel(library.Library{}, active, nil)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	next, _ := model.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	model = next.(Model)
	view := model.View()
	if len(strings.Split(view, "\n")) != 5 || !strings.Contains(view, "+19") {
		t.Fatalf("narrow conflict view = %q", view)
	}
	assertViewFits(t, view, 20)
	model, _ = press(model, tea.KeyMsg{Type: tea.KeyDown})
	_, command := press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("narrow conflict view did not keep Quit reachable")
	}
}

func newTestModel(t *testing.T, active library.Selection, apply ApplyFunc) Model {
	t.Helper()
	source := library.Library{Providers: []library.Provider{
		{
			ID: "acme",
			Models: []library.Model{
				{ID: "alpha"},
				{ID: "beta"},
			},
		},
		{ID: "builtin"},
		{
			ID: "zen",
			Models: []library.Model{
				{ID: "gamma"},
				{ID: "delta"},
			},
		},
	}}
	model, err := NewModel(source, active, apply)
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	return model
}

func press(model Model, key tea.KeyMsg) (Model, tea.Cmd) {
	next, command := model.Update(key)
	return next.(Model), command
}

func runeKey(value rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}}
}

func spaceKey() tea.KeyMsg {
	return runeKey(' ')
}

func assertViewHeight(t *testing.T, view string, height int) {
	t.Helper()
	if got := len(strings.Split(view, "\n")); got > height {
		t.Fatalf("height %d produced %d lines: %q", height, got, view)
	}
}

func assertViewFits(t *testing.T, view string, width int) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > width {
			t.Fatalf("width %d produced overflowing line %q", width, line)
		}
	}
}
