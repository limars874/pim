package library

import (
	"fmt"
	"os"
)

// ReadSelection 从真实 models.json 的 providers object 推导全部已启用的
// provider/model 集合，全程只读 pi 配置。
func ReadSelection(modelsPath string) (Selection, error) {
	contents, err := os.ReadFile(modelsPath)
	if err != nil {
		return nil, fmt.Errorf("read models.json: %w", err)
	}
	return DecodeSelection(contents)
}

// DecodeSelection 从真实 models.json 的 providers object 推导全部已启用的
// provider/model 集合。provider 即使 models 为空或缺失，也会保留在结果中。
func DecodeSelection(contents []byte) (Selection, error) {
	library, err := decodeModelsDocument(contents)
	if err != nil {
		return nil, fmt.Errorf("decode models.json: %w", err)
	}

	selection := make(Selection, len(library.Providers))
	for _, provider := range library.Providers {
		modelIDs := make([]string, len(provider.Models))
		for index, model := range provider.Models {
			modelIDs[index] = model.ID
		}
		selection[provider.ID] = modelIDs
	}
	return selection, nil
}
