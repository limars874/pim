package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// GenerateModelsJSON 从完整 library 和 staged selection 生成 pi 可读取的
// models.json。只输出 selected provider/model，保留其完整 JSON 字段。
func GenerateModelsJSON(source Library, selection Selection) ([]byte, error) {
	if err := ValidateSelection(source, selection); err != nil {
		return nil, err
	}

	providers := make([]generatedProvider, 0, len(selection))
	for _, provider := range source.Providers {
		modelIDs, selected := selection[provider.ID]
		if !selected {
			continue
		}
		raw, err := generateProviderJSON(provider, modelIDs)
		if err != nil {
			return nil, fmt.Errorf("generate provider %q: %w", provider.ID, err)
		}
		providers = append(providers, generatedProvider{ID: provider.ID, Raw: raw})
	}

	var output bytes.Buffer
	output.WriteString("{\n  \"providers\": {")
	for index, provider := range providers {
		if index > 0 {
			output.WriteByte(',')
		}
		output.WriteString("\n    ")
		output.WriteString(strconvQuote(provider.ID))
		output.WriteString(": ")
		output.WriteString(indentJSON(provider.Raw, "    "))
	}
	if len(providers) > 0 {
		output.WriteByte('\n')
	}
	output.WriteString("  }\n}\n")
	return output.Bytes(), nil
}

// ValidateSelection 检查 selection 是否引用 library 中存在的 provider/model。
// 只有本身没有 models 的 provider 可以使用空 model collection。
func ValidateSelection(source Library, selection Selection) error {
	providers := make(map[string]Provider, len(source.Providers))
	for _, provider := range source.Providers {
		if _, exists := providers[provider.ID]; exists {
			return fmt.Errorf("duplicate provider %q", provider.ID)
		}
		providers[provider.ID] = provider
	}
	for providerID, modelIDs := range selection {
		provider, exists := providers[providerID]
		if !exists {
			return fmt.Errorf("selected provider %q is not in the library", providerID)
		}
		if len(provider.Models) > 0 && len(modelIDs) == 0 {
			return fmt.Errorf("selected provider %q has models but no selected model", providerID)
		}
		knownModels := make(map[string]struct{}, len(provider.Models))
		for _, model := range provider.Models {
			knownModels[model.ID] = struct{}{}
		}
		seen := make(map[string]struct{}, len(modelIDs))
		for _, modelID := range modelIDs {
			if _, exists := knownModels[modelID]; !exists {
				return fmt.Errorf("selected model %q is not in provider %q", modelID, providerID)
			}
			if _, exists := seen[modelID]; exists {
				return fmt.Errorf("duplicate selected model %q in provider %q", modelID, providerID)
			}
			seen[modelID] = struct{}{}
		}
	}
	return nil
}

type generatedProvider struct {
	ID  string
	Raw json.RawMessage
}

func generateProviderJSON(provider Provider, modelIDs []string) (json.RawMessage, error) {
	if len(provider.Models) == 0 {
		if !json.Valid(provider.Raw) {
			return nil, fmt.Errorf("provider JSON is invalid")
		}
		return append(json.RawMessage(nil), provider.Raw...), nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(provider.Raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("provider JSON must be an object")
		}
		return nil, err
	}
	selected := make(map[string]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		selected[modelID] = struct{}{}
	}
	models := make([]json.RawMessage, 0, len(modelIDs))
	for _, model := range provider.Models {
		if _, ok := selected[model.ID]; ok {
			if !json.Valid(model.Raw) {
				return nil, fmt.Errorf("model %q JSON is invalid", model.ID)
			}
			models = append(models, model.Raw)
		}
	}
	modelsJSON, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	fields["models"] = modelsJSON
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func indentJSON(raw []byte, prefix string) string {
	var output bytes.Buffer
	_ = json.Indent(&output, raw, "", "  ")
	return prefix + strings.ReplaceAll(output.String(), "\n", "\n"+prefix)
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
