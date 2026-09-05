package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/limars874/pim/library"
)

func TestApplyAtomicallyWritesGeneratedModelsAndBackup(t *testing.T) {
	paths := library.Paths{AgentDir: t.TempDir()}
	old := []byte("{\n  \"providers\": {\"old\": {\"models\": []}}\n}\n")
	writeTarget(t, paths, old, 0o640)
	source := testLibrary()
	selection := library.Selection{"acme": {"beta"}, "empty": {}}
	service := Service{
		Paths: paths,
		Verify: func(_ context.Context, gotPaths library.Paths, gotSelection library.Selection) error {
			if gotPaths != paths || !equalSelection(gotSelection, selection) {
				t.Fatalf("verifier request = %#v, %#v", gotPaths, gotSelection)
			}
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 5, 1, 2, 3, 4, time.UTC) },
	}

	result, err := service.Apply(context.Background(), source, selection)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.BackupPath == "" {
		t.Fatal("Apply() did not return backup path")
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != string(old) {
		t.Fatalf("backup = %q, want old file %q", backup, old)
	}
	generated, err := os.ReadFile(paths.ModelsPath())
	if err != nil {
		t.Fatalf("read generated target: %v", err)
	}
	if strings.Contains(string(generated), `"old"`) || !strings.Contains(string(generated), `"beta"`) {
		t.Fatalf("generated models.json = %s", generated)
	}
	info, err := os.Stat(paths.ModelsPath())
	if err != nil {
		t.Fatalf("stat generated target: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("generated mode = %#o, want 0640", got)
	}
	entries, err := os.ReadDir(paths.AgentDir)
	if err != nil {
		t.Fatalf("read agent directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".models.json-") {
			t.Fatalf("temporary file was left behind: %s", entry.Name())
		}
	}
}

func TestApplyReplaceSyncFailureAfterCommitRollsBack(t *testing.T) {
	paths := library.Paths{AgentDir: t.TempDir()}
	old := []byte("{\n  \"providers\": {\"old\": {\"models\": []}}\n}\n")
	writeTarget(t, paths, old, 0o640)
	agentSyncs := 0
	service := Service{
		Paths: paths,
		Verify: func(context.Context, library.Paths, library.Selection) error {
			t.Fatal("verifier must not run after committed replace sync failure")
			return nil
		},
		SyncDirectory: func(dir string) error {
			if dir == paths.AgentDir {
				agentSyncs++
				if agentSyncs == 2 {
					return errors.New("injected target directory sync failure")
				}
			}
			return syncDirectory(dir)
		},
	}

	result, err := service.Apply(context.Background(), testLibrary(), library.Selection{"acme": {"beta"}})
	if err == nil || !strings.Contains(err.Error(), "replace failed after commit") || !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("Apply() error = %v, want committed replace and rollback outcome", err)
	}
	if agentSyncs != 3 {
		t.Fatalf("target directory Sync calls = %d, want 3 (history, failed replace, rollback)", agentSyncs)
	}
	current, readErr := os.ReadFile(paths.ModelsPath())
	if readErr != nil {
		t.Fatalf("read rolled back target: %v", readErr)
	}
	if string(current) != string(old) {
		t.Fatalf("target after committed replace failure = %q, want %q", current, old)
	}
	info, statErr := os.Stat(paths.ModelsPath())
	if statErr != nil {
		t.Fatalf("stat rolled back target: %v", statErr)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("rolled back mode = %#o, want 0640", info.Mode().Perm())
	}
	if result.BackupPath == "" {
		t.Fatal("committed replace failure did not retain backup")
	}
}

func TestApplyReplaceSyncFailureReportsRollbackFailure(t *testing.T) {
	paths := library.Paths{AgentDir: t.TempDir()}
	writeTarget(t, paths, []byte(`{"providers":{"old":{"models":[]}}}`), 0o600)
	agentSyncs := 0
	service := Service{
		Paths: paths,
		SyncDirectory: func(dir string) error {
			if dir == paths.AgentDir {
				agentSyncs++
				if agentSyncs == 2 || agentSyncs == 3 {
					return errors.New("injected target directory sync failure")
				}
			}
			return syncDirectory(dir)
		},
	}

	_, err := service.Apply(context.Background(), testLibrary(), library.Selection{"acme": {"alpha"}})
	if err == nil || !strings.Contains(err.Error(), "replace failed after commit") || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("Apply() error = %v, want rollback failure outcome", err)
	}
}

func TestApplyVerifierFailureRollsBackFromBackup(t *testing.T) {
	paths := library.Paths{AgentDir: t.TempDir()}
	old := []byte(`{"providers":{"old":{"models":[]}}}`)
	writeTarget(t, paths, old, 0o600)
	verificationErr := errors.New("model is absent from list")
	service := Service{
		Paths: paths,
		Verify: func(context.Context, library.Paths, library.Selection) error {
			return verificationErr
		},
		Now: func() time.Time { return time.Date(2026, 9, 5, 1, 2, 3, 4, time.UTC) },
	}

	result, err := service.Apply(context.Background(), testLibrary(), library.Selection{"acme": {"alpha"}})
	if err == nil {
		t.Fatal("Apply() error = nil, want verifier failure")
	}
	if !strings.Contains(err.Error(), "verification failed") || !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("Apply() error = %v, want verification and rollback outcome", err)
	}
	if result.BackupPath == "" {
		t.Fatal("failed Apply did not retain backup path")
	}
	current, readErr := os.ReadFile(paths.ModelsPath())
	if readErr != nil {
		t.Fatalf("read rolled back target: %v", readErr)
	}
	if string(current) != string(old) {
		t.Fatalf("target after rollback = %q, want %q", current, old)
	}
	if _, statErr := os.Stat(result.BackupPath); statErr != nil {
		t.Fatalf("backup was not retained: %v", statErr)
	}
}

func TestApplyVerifierFailureRemovesNewTargetWhenNoOriginalExists(t *testing.T) {
	paths := library.Paths{AgentDir: t.TempDir()}
	service := Service{
		Paths: paths,
		Verify: func(context.Context, library.Paths, library.Selection) error {
			return errors.New("verification failed")
		},
	}

	_, err := service.Apply(context.Background(), testLibrary(), library.Selection{"acme": {"alpha"}})
	if err == nil || !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("Apply() error = %v, want successful rollback result", err)
	}
	if _, statErr := os.Stat(paths.ModelsPath()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new unverified target remains after rollback: %v", statErr)
	}
}

func TestListedModelsRequiresProviderAndModelPair(t *testing.T) {
	available := listedModels("provider  model\nacme      alpha\nother     beta\n")
	if !available["acme\x00alpha"] || available["acme\x00beta"] {
		t.Fatalf("listed models = %#v", available)
	}
}

func testLibrary() library.Library {
	return library.Library{Providers: []library.Provider{
		{
			ID:     "acme",
			Raw:    []byte(`{"api":"openai-completions","models":[{"id":"alpha"},{"id":"beta","unknown":true}]}`),
			Models: []library.Model{{ID: "alpha", Raw: []byte(`{"id":"alpha"}`)}, {ID: "beta", Raw: []byte(`{"id":"beta","unknown":true}`)}},
		},
		{ID: "empty", Raw: []byte(`{"api":"openai-completions"}`)},
	}}
}

func writeTarget(t *testing.T, paths library.Paths, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(paths.ModelsPath(), contents, mode); err != nil {
		t.Fatalf("write target: %v", err)
	}
}

func equalSelection(left, right library.Selection) bool {
	if len(left) != len(right) {
		return false
	}
	for providerID, leftModels := range left {
		rightModels, ok := right[providerID]
		if !ok || strings.Join(leftModels, "\x00") != strings.Join(rightModels, "\x00") {
			return false
		}
	}
	return true
}

func TestWriteBackupCreatesUniqueTimestampNames(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 5, 1, 2, 3, 4, time.UTC)
	first, err := writeBackup(dir, []byte("one"), now)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := writeBackup(dir, []byte("two"), now)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if first == second || filepath.Base(first) == filepath.Base(second) {
		t.Fatalf("backup names are not unique: %q, %q", first, second)
	}
}
