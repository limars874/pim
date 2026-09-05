package apply

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/limars874/pim/library"
)

func TestApplySmokeWithPi(t *testing.T) {
	if os.Getenv("PIM_PI_SMOKE") != "1" {
		t.Skip("set PIM_PI_SMOKE=1 to run against the installed pi binary")
	}
	paths := library.Paths{AgentDir: t.TempDir()}
	source := library.Library{Providers: []library.Provider{{
		ID: "pim-smoke",
		Raw: []byte(`{
  "baseUrl": "http://127.0.0.1:1/v1",
  "api": "openai-completions",
  "apiKey": "smoke",
  "models": [{"id":"one"},{"id":"two"}]
}`),
		Models: []library.Model{
			{ID: "one", Raw: []byte(`{"id":"one"}`)},
			{ID: "two", Raw: []byte(`{"id":"two"}`)},
		},
	}}}
	initial := library.Selection{"pim-smoke": {"one"}}
	initialJSON, err := library.GenerateModelsJSON(source, initial)
	if err != nil {
		t.Fatalf("generate initial models.json: %v", err)
	}
	if err := os.WriteFile(paths.ModelsPath(), initialJSON, 0o600); err != nil {
		t.Fatalf("write initial models.json: %v", err)
	}

	result, err := (Service{Paths: paths, VerifyTimeout: 20 * time.Second}).Apply(
		context.Background(), source, library.Selection{"pim-smoke": {"two"}},
	)
	if err != nil {
		t.Fatalf("Apply() smoke error = %v", err)
	}
	if result.BackupPath == "" {
		t.Fatal("Apply() smoke did not retain a backup")
	}
	contents, err := os.ReadFile(paths.ModelsPath())
	if err != nil {
		t.Fatalf("read applied models.json: %v", err)
	}
	if string(contents) == string(initialJSON) {
		t.Fatal("Apply() smoke did not replace models.json")
	}
}
