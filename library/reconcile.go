package library

import "sort"

// Reconcile 计算 active selection 与 library 的有效交集，并记录需要移除的
// orphan 引用或无效空 model selection。它不访问 filesystem，也不会修改输入。
func Reconcile(source Library, active Selection) Reconciliation {
	providers := make(map[string]Provider, len(source.Providers))
	for _, provider := range source.Providers {
		providers[provider.ID] = provider
	}

	providerIDs := make([]string, 0, len(active))
	for providerID := range active {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	valid := make(Selection, len(active))
	var conflicts []Conflict
	for _, providerID := range providerIDs {
		activeModels := active[providerID]
		provider, exists := providers[providerID]
		if !exists {
			conflicts = append(conflicts, Conflict{
				Kind:       MissingProviderConflict,
				ProviderID: providerID,
			})
			continue
		}

		if len(provider.Models) > 0 && len(activeModels) == 0 {
			conflicts = append(conflicts, Conflict{
				Kind:       EmptyModelSelectionConflict,
				ProviderID: providerID,
			})
			continue
		}

		knownModels := make(map[string]struct{}, len(provider.Models))
		for _, model := range provider.Models {
			knownModels[model.ID] = struct{}{}
		}
		validModels := make([]string, 0, len(activeModels))
		for _, modelID := range activeModels {
			if _, exists := knownModels[modelID]; exists {
				validModels = append(validModels, modelID)
				continue
			}
			conflicts = append(conflicts, Conflict{
				Kind:       MissingModelConflict,
				ProviderID: providerID,
				ModelID:    modelID,
			})
		}

		// library 中没有 models 的 provider 可以被空 selection 启用。
		if len(provider.Models) == 0 && len(activeModels) == 0 {
			valid[providerID] = []string{}
		} else if len(validModels) > 0 {
			valid[providerID] = validModels
		}
	}

	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].ProviderID != conflicts[j].ProviderID {
			return conflicts[i].ProviderID < conflicts[j].ProviderID
		}
		if conflicts[i].Kind != conflicts[j].Kind {
			return conflicts[i].Kind < conflicts[j].Kind
		}
		return conflicts[i].ModelID < conflicts[j].ModelID
	})
	return Reconciliation{Valid: valid, Conflicts: conflicts}
}
