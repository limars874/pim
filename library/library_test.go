package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBootstrapImportsOnceAndPreservesUnknownFields(t *testing.T) {
	paths := testPaths(t)
	source := []byte(`{
  "providers": {
    "acme": {
      "api": "openai-completions",
      "providerExtension": {"retry": 3},
      "models": [
        {"id": "alpha", "modelExtension": {"enabled": true}, "samplingParams": {"top_k": 7}}
      ]
    }
  }
}`)
	writeModels(t, paths, source)

	library, imported, err := Bootstrap(paths)
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if !imported {
		t.Fatal("Bootstrap() imported = false, want true")
	}
	if len(library.Providers) != 1 || library.Providers[0].ID != "acme" {
		t.Fatalf("Bootstrap() providers = %#v", library.Providers)
	}
	if !jsonEqual(t, library.Providers[0].Raw, providerFromSource(t, source, "acme")) {
		t.Fatal("provider raw JSON lost an unknown field")
	}
	if !jsonEqual(t, library.Providers[0].Models[0].Raw, []byte(`{"id":"alpha","modelExtension":{"enabled":true},"samplingParams":{"top_k":7}}`)) {
		t.Fatal("model raw JSON lost an unknown field")
	}

	persisted, err := os.ReadFile(filepath.Join(paths.LibraryDir(), "acme.json"))
	if err != nil {
		t.Fatalf("read persisted provider: %v", err)
	}
	if !jsonEqual(t, persisted, providerFromSource(t, source, "acme")) {
		t.Fatal("persisted provider does not preserve its complete JSON object")
	}

	writeModels(t, paths, []byte(`{"providers":{"new-provider":{"models":[{"id":"new-model"}]}}}`))
	library, imported, err = Bootstrap(paths)
	if err != nil {
		t.Fatalf("second Bootstrap() error = %v", err)
	}
	if imported {
		t.Fatal("second Bootstrap() imported = true, want false")
	}
	if len(library.Providers) != 1 || library.Providers[0].ID != "acme" {
		t.Fatalf("second Bootstrap() providers = %#v, want original library only", library.Providers)
	}
}

func TestBootstrapKeepsExistingEmptyLibraryEmpty(t *testing.T) {
	paths := testPaths(t)
	writeModels(t, paths, []byte(`{"providers":{"deleted":{"models":[{"id":"gone"}]}}}`))
	if err := os.MkdirAll(paths.LibraryDir(), 0o700); err != nil {
		t.Fatalf("create empty provider library: %v", err)
	}

	source, imported, err := Bootstrap(paths)
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if imported || len(source.Providers) != 0 {
		t.Fatalf("Bootstrap() = %#v, imported %v; want existing empty library", source, imported)
	}
	active, err := ReadSelection(paths.ModelsPath())
	if err != nil {
		t.Fatalf("ReadSelection() error = %v", err)
	}
	got := Reconcile(source, active)
	want := Reconciliation{Valid: Selection{}, Conflicts: []Conflict{{
		Kind:       MissingProviderConflict,
		ProviderID: "deleted",
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %#v, want %#v", got, want)
	}
}

func TestReadSelectionDerivesAllEnabledProvidersAndModels(t *testing.T) {
	paths := testPaths(t)
	writeModels(t, paths, []byte(`{
  "providers": {
    "acme": {"models": [{"id": "alpha"}, {"id": "beta"}]},
    "builtin": {"api": "openai-completions"},
    "empty": {"models": []},
    "zen": {"models": [{"id": "gamma"}, {"id": "delta"}]}
  }
}`))

	selection, err := ReadSelection(paths.ModelsPath())
	if err != nil {
		t.Fatalf("ReadSelection() error = %v", err)
	}
	want := Selection{
		"acme":    {"alpha", "beta"},
		"builtin": {},
		"empty":   {},
		"zen":     {"gamma", "delta"},
	}
	if !reflect.DeepEqual(selection, want) {
		t.Fatalf("ReadSelection() = %#v, want %#v", selection, want)
	}
}

func TestBootstrapRejectsDuplicateAndInvalidModels(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "duplicate model ID",
			source: `{"providers":{"acme":{"models":[{"id":"alpha"},{"id":"alpha"}]}}}`,
		},
		{
			name:   "missing model ID",
			source: `{"providers":{"acme":{"models":[{"name":"alpha"}]}}}`,
		},
		{
			name:   "null models",
			source: `{"providers":{"acme":{"models":null}}}`,
		},
		{
			name:   "unsafe provider ID",
			source: `{"providers":{"../acme":{"models":[{"id":"alpha"}]}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := testPaths(t)
			writeModels(t, paths, []byte(tt.source))
			if _, _, err := Bootstrap(paths); err == nil {
				t.Fatal("Bootstrap() error = nil, want validation error")
			}
		})
	}
}

func TestDecodeSelectionRejectsDuplicateAndInvalidModels(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "duplicate model ID",
			source: `{"providers":{"acme":{"models":[{"id":"alpha"},{"id":"alpha"}]}}}`,
		},
		{
			name:   "missing model ID",
			source: `{"providers":{"acme":{"models":[{"name":"alpha"}]}}}`,
		},
		{
			name:   "blank model ID",
			source: `{"providers":{"acme":{"models":[{"id":"  "}]}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeSelection([]byte(tt.source)); err == nil {
				t.Fatal("DecodeSelection() error = nil, want validation error")
			}
		})
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	return Paths{AgentDir: t.TempDir()}
}

func writeModels(t *testing.T, paths Paths, contents []byte) {
	t.Helper()
	if err := os.WriteFile(paths.ModelsPath(), contents, 0o600); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
}

func providerFromSource(t *testing.T, source []byte, id string) []byte {
	t.Helper()
	var document struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(source, &document); err != nil {
		t.Fatalf("decode source: %v", err)
	}
	return document.Providers[id]
}

func TestReconcileMissingProviderKeepsNoRedundantModelConflicts(t *testing.T) {
	source := Library{Providers: []Provider{{ID: "acme", Models: []Model{{ID: "alpha"}}}}}
	active := Selection{"orphan": {"one", "two"}}

	got := Reconcile(source, active)
	want := Reconciliation{Valid: Selection{}, Conflicts: []Conflict{{
		Kind:       MissingProviderConflict,
		ProviderID: "orphan",
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %#v, want %#v", got, want)
	}
}

func TestReconcileMissingModelPreservesSurvivingSelection(t *testing.T) {
	source := Library{Providers: []Provider{{
		ID:     "acme",
		Models: []Model{{ID: "alpha"}, {ID: "beta"}},
	}, {ID: "builtin"}}}
	active := Selection{"acme": {"alpha", "gone", "beta"}, "builtin": {}}

	got := Reconcile(source, active)
	want := Reconciliation{
		Valid: Selection{"acme": {"alpha", "beta"}, "builtin": {}},
		Conflicts: []Conflict{{
			Kind:       MissingModelConflict,
			ProviderID: "acme",
			ModelID:    "gone",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %#v, want %#v", got, want)
	}
}

func TestReconcileEmptyModelSelectionIsAnExplicitConflict(t *testing.T) {
	source := Library{Providers: []Provider{{ID: "acme", Models: []Model{{ID: "alpha"}}}, {ID: "builtin"}}}
	active := Selection{"acme": {}, "builtin": {}}

	got := Reconcile(source, active)
	want := Reconciliation{
		Valid: Selection{"builtin": {}},
		Conflicts: []Conflict{{
			Kind:       EmptyModelSelectionConflict,
			ProviderID: "acme",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %#v, want %#v", got, want)
	}
}

func TestReconcileAllMissingModelsRecordsProviderRemovalEvidence(t *testing.T) {
	source := Library{Providers: []Provider{{ID: "acme", Models: []Model{{ID: "alpha"}, {ID: "beta"}}}}}
	active := Selection{"acme": {"gone-beta", "gone-alpha"}}

	got := Reconcile(source, active)
	want := Reconciliation{
		Valid: Selection{},
		Conflicts: []Conflict{
			{Kind: MissingModelConflict, ProviderID: "acme", ModelID: "gone-alpha"},
			{Kind: MissingModelConflict, ProviderID: "acme", ModelID: "gone-beta"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %#v, want %#v", got, want)
	}
}

func TestReconcileMixedConflictsHaveDeterministicOrder(t *testing.T) {
	source := Library{Providers: []Provider{{ID: "acme", Models: []Model{{ID: "kept"}}}, {ID: "empty", Models: []Model{{ID: "model"}}}, {ID: "zen", Models: []Model{{ID: "also-kept"}}}}}
	active := Selection{
		"zulu":  {"one", "two"},
		"acme":  {"zeta", "kept", "alpha"},
		"empty": {},
		"zen":   {"also-kept", "missing"},
	}

	got := Reconcile(source, active)
	want := Reconciliation{
		Valid: Selection{"acme": {"kept"}, "zen": {"also-kept"}},
		Conflicts: []Conflict{
			{Kind: MissingModelConflict, ProviderID: "acme", ModelID: "alpha"},
			{Kind: MissingModelConflict, ProviderID: "acme", ModelID: "zeta"},
			{Kind: EmptyModelSelectionConflict, ProviderID: "empty"},
			{Kind: MissingModelConflict, ProviderID: "zen", ModelID: "missing"},
			{Kind: MissingProviderConflict, ProviderID: "zulu"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %#v, want %#v", got, want)
	}
}

func TestLoadMalformedProviderIncludesFilePath(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.LibraryDir(), 0o700); err != nil {
		t.Fatalf("create library: %v", err)
	}
	providerPath := filepath.Join(paths.LibraryDir(), "acme.json")
	if err := os.WriteFile(providerPath, []byte(`{"models":`), 0o600); err != nil {
		t.Fatalf("write provider: %v", err)
	}

	_, err := Load(paths)
	if err == nil || !strings.Contains(err.Error(), providerPath) {
		t.Fatalf("Load() error = %v, want malformed provider path %q", err, providerPath)
	}
}

func jsonEqual(t *testing.T, got, want []byte) bool {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}
