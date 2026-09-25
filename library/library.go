package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	libraryDirectory = "model-library/providers"
	historyDirectory = "model-library/history"
	modelsFilename   = "models.json"
)

// DefaultPaths 返回 pim 的 runtime path，不访问 filesystem。
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return Paths{AgentDir: filepath.Join(home, ".pi", "agent")}, nil
}

// LibraryDir 返回 provider library directory。
func (p Paths) LibraryDir() string {
	return filepath.Join(p.AgentDir, filepath.FromSlash(libraryDirectory))
}

// HistoryDir 返回 models.json backup history directory。
func (p Paths) HistoryDir() string {
	return filepath.Join(p.AgentDir, filepath.FromSlash(historyDirectory))
}

// ModelsPath 返回 pi source models.json path。
func (p Paths) ModelsPath() string {
	return filepath.Join(p.AgentDir, modelsFilename)
}

// Bootstrap 仅在 provider library directory 不存在时首次导入 models.json。
// 一旦目录已存在（即使没有 provider JSON），library 都是唯一库存来源，后续
// 不会读取或同步 models.json。
func Bootstrap(paths Paths) (Library, bool, error) {
	exists, err := libraryDirectoryExists(paths.LibraryDir())
	if err != nil {
		return Library{}, false, err
	}
	if exists {
		library, err := Load(paths)
		return library, false, err
	}

	contents, err := os.ReadFile(paths.ModelsPath())
	if err != nil {
		return Library{}, false, fmt.Errorf("read source models.json: %w", err)
	}
	library, err := decodeModelsDocument(contents)
	if err != nil {
		return Library{}, false, fmt.Errorf("decode source models.json: %w", err)
	}
	if err := persistImportedLibrary(paths.LibraryDir(), library); err != nil {
		return Library{}, false, err
	}
	return library, true, nil
}

// Load 读取 library 中的 provider JSON file。目录不存在时返回 empty library。
func Load(paths Paths) (Library, error) {
	entries, err := os.ReadDir(paths.LibraryDir())
	if errors.Is(err, fs.ErrNotExist) {
		return Library{}, nil
	}
	if err != nil {
		return Library{}, fmt.Errorf("read provider library: %w", err)
	}

	providers := make([]Provider, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateProviderID(id); err != nil {
			return Library{}, fmt.Errorf("invalid provider filename %q: %w", entry.Name(), err)
		}
		if _, ok := seen[id]; ok {
			return Library{}, fmt.Errorf("duplicate provider %q", id)
		}
		seen[id] = struct{}{}

		providerPath := filepath.Join(paths.LibraryDir(), entry.Name())
		contents, err := os.ReadFile(providerPath)
		if err != nil {
			return Library{}, fmt.Errorf("read provider %q at %s: %w", id, providerPath, err)
		}
		provider, err := decodeProvider(id, contents)
		if err != nil {
			return Library{}, fmt.Errorf("decode provider %q at %s: %w", id, providerPath, err)
		}
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return Library{Providers: providers}, nil
}

func libraryDirectoryExists(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat provider library: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("provider library is not a directory: %s", dir)
	}
	return true, nil
}

func persistImportedLibrary(dir string, library Library) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create provider library: %w", err)
	}
	for _, provider := range library.Providers {
		path := filepath.Join(dir, provider.ID+".json")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create provider %q: %w", provider.ID, err)
		}
		_, writeErr := file.Write(provider.Raw)
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("write provider %q: %w", provider.ID, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close provider %q: %w", provider.ID, closeErr)
		}
	}
	return nil
}

func decodeModelsDocument(contents []byte) (Library, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(contents, &document); err != nil {
		return Library{}, err
	}
	providersJSON, ok := document["providers"]
	if !ok {
		return Library{}, errors.New("missing providers")
	}
	var sourceProviders map[string]json.RawMessage
	if err := json.Unmarshal(providersJSON, &sourceProviders); err != nil || sourceProviders == nil {
		if err == nil {
			err = errors.New("providers is null")
		}
		return Library{}, fmt.Errorf("providers must be an object: %w", err)
	}

	providerIDs := make([]string, 0, len(sourceProviders))
	for id := range sourceProviders {
		if err := validateProviderID(id); err != nil {
			return Library{}, fmt.Errorf("invalid provider %q: %w", id, err)
		}
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)

	providers := make([]Provider, 0, len(providerIDs))
	for _, id := range providerIDs {
		provider, err := decodeProvider(id, sourceProviders[id])
		if err != nil {
			return Library{}, fmt.Errorf("decode provider %q: %w", id, err)
		}
		providers = append(providers, provider)
	}
	return Library{Providers: providers}, nil
}

func decodeProvider(id string, contents []byte) (Provider, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		return Provider{}, err
	}
	if fields == nil {
		return Provider{}, errors.New("provider must be an object")
	}

	models := []Model{}
	if modelsJSON, ok := fields["models"]; ok {
		var sourceModels []json.RawMessage
		if err := json.Unmarshal(modelsJSON, &sourceModels); err != nil || sourceModels == nil {
			if err == nil {
				err = errors.New("models is null")
			}
			return Provider{}, fmt.Errorf("models must be an array: %w", err)
		}
		seen := make(map[string]struct{}, len(sourceModels))
		for index, rawModel := range sourceModels {
			model, err := decodeModel(rawModel)
			if err != nil {
				return Provider{}, fmt.Errorf("invalid model at index %d: %w", index, err)
			}
			if _, ok := seen[model.ID]; ok {
				return Provider{}, fmt.Errorf("duplicate model %q", model.ID)
			}
			seen[model.ID] = struct{}{}
			models = append(models, model)
		}
	}
	return Provider{ID: id, Raw: append(json.RawMessage(nil), contents...), Models: models}, nil
}

func decodeModel(contents []byte) (Model, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		return Model{}, err
	}
	if fields == nil {
		return Model{}, errors.New("model must be an object")
	}
	idJSON, ok := fields["id"]
	if !ok {
		return Model{}, errors.New("missing id")
	}
	var id string
	if err := json.Unmarshal(idJSON, &id); err != nil || strings.TrimSpace(id) == "" {
		return Model{}, errors.New("id must be a non-empty string")
	}
	return Model{ID: id, Raw: append(json.RawMessage(nil), contents...)}, nil
}

func validateProviderID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("id must be non-empty")
	}
	if filepath.Base(id) != id || id == "." || id == ".." {
		return errors.New("id must be one path component")
	}
	return nil
}
