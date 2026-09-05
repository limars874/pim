// Package library 管理 pim 的 provider model library。
package library

import "encoding/json"

// Paths 标识本包管理或读取的 pi agent 文件。
// AgentDir 通常为 ~/.pi/agent。
type Paths struct {
	AgentDir string
}

// Provider 是 model library 中的 provider 条目。Raw 保存完整 provider
// JSON object，持久化时不会丢失 pim 不理解的字段。
type Provider struct {
	ID     string
	Raw    json.RawMessage
	Models []Model
}

// Model 是从 provider 中提取的 model 条目。Raw 保存完整 model JSON object，
// 独立于 pim 用于校验的字段。
type Model struct {
	ID  string
	Raw json.RawMessage
}

// Library 是完整的 provider library。磁盘读取或导入后均按 ID 排序。
type Library struct {
	Providers []Provider
}

// Selection 是已启用 provider 到其已启用 model ID 的集合。map 中存在 key
// 即表示 provider 已启用；空 slice 仅对本身没有 models 的 provider 有效。
type Selection map[string][]string

// ConflictKind 标识 active selection 与 library 不一致的类型。
type ConflictKind string

const (
	// MissingProviderConflict 表示 active selection 引用了不在 library 中的 provider。
	MissingProviderConflict ConflictKind = "missing provider"
	// MissingModelConflict 表示 active selection 引用了 provider 中不存在的 model。
	MissingModelConflict ConflictKind = "missing model"
	// EmptyModelSelectionConflict 表示有 models 的 provider 在 active selection 中
	// 没有任何 model，无法作为有效 selection 保留。
	EmptyModelSelectionConflict ConflictKind = "provider has no selected models"
)

// Conflict 描述一项需要从 active selection 移除或修复的不一致条目。缺失
// provider 不会为其 models 生成冗余的 Conflict。
type Conflict struct {
	Kind       ConflictKind
	ProviderID string
	ModelID    string
}

// Reconciliation 是 active selection 与 library 对齐后的结果。Valid 可安全传给
// GenerateModelsJSON；Conflicts 按 provider、类型和 model ID 确定性排序。
type Reconciliation struct {
	Valid     Selection
	Conflicts []Conflict
}
