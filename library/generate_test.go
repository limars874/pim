package library

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateModelsJSONFiltersAndPreservesFields(t *testing.T) {
	source := Library{Providers: []Provider{
		{
			ID:  "zeta",
			Raw: json.RawMessage(`{"api":"openai-completions","providerExtension":{"retry":3},"models":[{"id":"one"},{"id":"two"}]}`),
			Models: []Model{
				{ID: "one", Raw: json.RawMessage(`{"id":"one","unknownModel":{"level":1}}`)},
				{ID: "two", Raw: json.RawMessage(`{"id":"two","unknownModel":{"level":2},"samplingParams":{"top_p":0.9}}`)},
			},
		},
		{
			ID:  "empty",
			Raw: json.RawMessage(`{"api":"openai-completions","providerExtension":{"enabled":true}}`),
		},
		{ID: "disabled", Raw: json.RawMessage(`{"models":[{"id":"no"}]}`), Models: []Model{{ID: "no", Raw: json.RawMessage(`{"id":"no"}`)}}},
	}}
	selection := Selection{"zeta": {"two"}, "empty": {}}

	generated, err := GenerateModelsJSON(source, selection)
	if err != nil {
		t.Fatalf("GenerateModelsJSON() error = %v", err)
	}
	if !bytes.HasSuffix(generated, []byte("\n")) {
		t.Fatal("generated JSON is missing trailing newline")
	}
	if bytes.Index(generated, []byte(`"zeta"`)) > bytes.Index(generated, []byte(`"empty"`)) {
		t.Fatalf("provider order did not follow library order: %s", generated)
	}

	var document struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(generated, &document); err != nil {
		t.Fatalf("generated JSON is invalid: %v", err)
	}
	if _, exists := document.Providers["disabled"]; exists {
		t.Fatal("disabled provider was generated")
	}
	if !jsonEqual(t, document.Providers["empty"], source.Providers[1].Raw) {
		t.Fatal("provider without models was not preserved verbatim")
	}

	var zeta map[string]json.RawMessage
	if err := json.Unmarshal(document.Providers["zeta"], &zeta); err != nil {
		t.Fatalf("decode zeta: %v", err)
	}
	if !jsonEqual(t, zeta["providerExtension"], []byte(`{"retry":3}`)) {
		t.Fatal("unknown provider field was lost")
	}
	var models []json.RawMessage
	if err := json.Unmarshal(zeta["models"], &models); err != nil {
		t.Fatalf("decode filtered models: %v", err)
	}
	if len(models) != 1 || !jsonEqual(t, models[0], source.Providers[0].Models[1].Raw) {
		t.Fatalf("filtered model JSON = %s, want model two with unknown fields", zeta["models"])
	}
}

func TestGenerateModelsJSONRejectsInvalidSelection(t *testing.T) {
	source := Library{Providers: []Provider{{
		ID:     "acme",
		Raw:    json.RawMessage(`{"models":[{"id":"alpha"}]}`),
		Models: []Model{{ID: "alpha", Raw: json.RawMessage(`{"id":"alpha"}`)}},
	}}}
	_, err := GenerateModelsJSON(source, Selection{"acme": {}})
	if err == nil || !strings.Contains(err.Error(), "no selected model") {
		t.Fatalf("GenerateModelsJSON() error = %v, want empty model selection error", err)
	}
}
