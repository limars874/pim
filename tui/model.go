// Package tui 提供 pim 的 Bubble Tea 选择界面状态机。
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/limars874/pim/library"
)

// FocusArea 表示当前接收导航和切换输入的栏位。
type FocusArea uint8

const (
	// ProvidersFocus 表示左侧 provider 栏位。
	ProvidersFocus FocusArea = iota
	// ModelsFocus 表示右侧 model 栏位。
	ModelsFocus
)

// ModelRef 唯一标识一个 provider 下的 model。
type ModelRef struct {
	ProviderID string
	ModelID    string
}

// Diff 描述 initial 与 staged selection 的差异。
type Diff struct {
	AddedProviders   []string
	RemovedProviders []string
	AddedModels      []ModelRef
	RemovedModels    []ModelRef
}

// Empty 报告 diff 是否没有变更。
func (d Diff) Empty() bool {
	return len(d.AddedProviders) == 0 && len(d.RemovedProviders) == 0 &&
		len(d.AddedModels) == 0 && len(d.RemovedModels) == 0
}

// ApplyRequest 是交给调用方的 staged selection 与完整 diff。ApplyFunc 的
// 实现负责后续提交；本 package 不会写入任何 pi 文件。
type ApplyRequest struct {
	Initial library.Selection
	Staged  library.Selection
	Diff    Diff
}

// ApplyFunc 是明确的提交 contract。它必须返回实际提交的 tea.Cmd，且该
// command 必须产生 ApplySucceededMsg 或 ApplyFailedMsg；nil 会显示错误。
type ApplyFunc func(ApplyRequest) tea.Cmd

// FixRequest 描述 conflict repair 要提交的 reconciled selection。FixFunc 的
// 实现负责实际提交；本 package 不会写入任何 pi 文件。
type FixRequest struct {
	Active     library.Selection
	Reconciled library.Selection
	Conflicts  []library.Conflict
	Diff       Diff
}

// FixFunc 是 conflict repair 的独立提交 contract。它必须返回实际提交的
// tea.Cmd，并产生 FixSucceededMsg 或 FixFailedMsg；nil 会显示错误。
type FixFunc func(FixRequest) tea.Cmd

// FixSucceededMsg 表示调用方已成功提交 reconciled selection。
type FixSucceededMsg struct {
	Selection library.Selection
	Message   string
}

// FixFailedMsg 表示 conflict repair 提交失败。TUI 保留 repair preview。
type FixFailedMsg struct {
	Err error
}

// ApplySucceededMsg 表示调用方已成功提交指定 selection。
type ApplySucceededMsg struct {
	Selection library.Selection
	Message   string
}

// ApplyFailedMsg 表示调用方提交失败。TUI 保留 preview 和 staged selection。
type ApplyFailedMsg struct {
	Err error
}

// Model 是 Bubble Tea 双栏状态机。
type Model struct {
	providers []library.Provider
	active    library.Selection
	initial   library.Selection
	staged    library.Selection
	apply     ApplyFunc

	focus          FocusArea
	providerCursor int
	providerOffset int
	modelCursor    int
	modelOffset    int
	width          int
	height         int
	preview        bool
	previewOffset  int
	applying       bool
	conflicts      []library.Conflict
	fix            FixFunc
	fixCursor      int
	fixPreview     bool
	fixApplying    bool
	confirmQuit    bool
	message        string
}

// NewModel 使用 library 和启动时 active selection 创建状态机。它只接收传入
// 数据，不读取 models.json 或调用 pi --list-models。orphan 引用会进入 conflict
// UI，而不是让构造失败。可选 FixFunc 用于提交 repair preview。
func NewModel(source library.Library, active library.Selection, apply ApplyFunc, fix ...FixFunc) (Model, error) {
	providers := append([]library.Provider(nil), source.Providers...)
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	reconciliation := library.Reconcile(source, active)
	if err := library.ValidateSelection(source, reconciliation.Valid); err != nil {
		return Model{}, err
	}

	var fixCallback FixFunc
	if len(fix) > 0 {
		fixCallback = fix[0]
	}
	return Model{
		providers: providers,
		active:    cloneSelection(active),
		initial:   cloneSelection(reconciliation.Valid),
		staged:    cloneSelection(reconciliation.Valid),
		apply:     apply,
		conflicts: append([]library.Conflict(nil), reconciliation.Conflicts...),
		fix:       fixCallback,
		width:     80,
		height:    24,
	}, nil
}

// Init 不需要启动异步任务。
func (m Model) Init() tea.Cmd {
	return nil
}

// Update 处理窗口尺寸和键盘交互。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case FixSucceededMsg:
		m.fixApplying = false
		selection := msg.Selection
		if selection == nil {
			selection = m.initial
		}
		m.active = cloneSelection(selection)
		m.initial = cloneSelection(selection)
		m.staged = cloneSelection(selection)
		m.conflicts = nil
		m.fixPreview = false
		m.previewOffset = 0
		m.message = msg.Message
		return m, nil
	case FixFailedMsg:
		m.fixApplying = false
		if msg.Err == nil {
			m.message = "Fix failed"
		} else {
			m.message = "Fix failed: " + msg.Err.Error()
		}
		return m, nil
	case ApplySucceededMsg:
		if m.fixPreview {
			m.message = "Repair callback returned an unexpected Apply result"
			return m, nil
		}
		m.applying = false
		selection := msg.Selection
		if selection == nil {
			selection = m.staged
		}
		m.initial = cloneSelection(selection)
		m.staged = cloneSelection(selection)
		m.preview = false
		m.previewOffset = 0
		m.message = msg.Message
		return m, nil
	case ApplyFailedMsg:
		if m.fixPreview {
			m.message = "Repair callback returned an unexpected Apply result"
			return m, nil
		}
		m.applying = false
		if msg.Err == nil {
			m.message = "Apply failed"
		} else {
			m.message = "Apply failed: " + msg.Err.Error()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampViewports()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.confirmQuit {
		switch key {
		case "enter", "y", "Y":
			return m, tea.Quit
		case "esc", "n", "N":
			m.confirmQuit = false
			return m, nil
		}
		return m, nil
	}
	if m.fixPreview {
		if m.fixApplying {
			return m, nil
		}
		switch key {
		case "esc":
			m.fixPreview = false
			m.previewOffset = 0
			m.message = ""
			return m, nil
		case "ctrl+c", "q", "Q":
			return m, tea.Quit
		case "up", "k":
			m.movePreview(-1)
			return m, nil
		case "down", "j":
			m.movePreview(1)
			return m, nil
		case "enter":
			if m.fix == nil {
				m.message = "Fix callback is not configured"
				return m, nil
			}
			command := m.fix(m.FixRequest())
			if command == nil {
				m.message = "Fix callback returned no result"
				return m, nil
			}
			m.fixApplying = true
			return m, command
		}
		return m, nil
	}
	if len(m.conflicts) > 0 {
		return m.updateConflictKey(key)
	}
	if m.preview {
		if m.applying {
			return m, nil
		}
		switch key {
		case "esc":
			m.preview = false
			m.previewOffset = 0
			return m, nil
		case "up", "k":
			m.movePreview(-1)
			return m, nil
		case "down", "j":
			m.movePreview(1)
			return m, nil
		case "enter":
			if m.apply == nil {
				m.message = "Apply callback is not configured"
				return m, nil
			}
			request := ApplyRequest{
				Initial: cloneSelection(m.initial),
				Staged:  cloneSelection(m.staged),
				Diff:    DiffSelections(m.initial, m.staged),
			}
			command := m.apply(request)
			if command == nil {
				m.message = "Apply callback returned no result"
				return m, nil
			}
			m.applying = true
			return m, command
		}
		return m, nil
	}

	switch key {
	case "ctrl+c", "q", "Q":
		return m.requestQuit()
	case "esc":
		if m.focus == ModelsFocus {
			m.focus = ProvidersFocus
			return m, nil
		}
		return m.requestQuit()
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case " ":
		m.toggleFocused()
	case "enter", "right":
		if m.focus == ProvidersFocus && len(m.providers) > 0 {
			m.focus = ModelsFocus
			m.clampModelCursor()
			m.ensureModelVisible()
		}
	case "left":
		if m.focus == ModelsFocus {
			m.focus = ProvidersFocus
		}
	case "r", "R":
		m.staged = cloneSelection(m.initial)
		m.message = "Staged selection reset"
	case "a", "A":
		m.preview = true
		m.previewOffset = 0
		m.message = ""
	}
	return m, nil
}

func (m Model) updateConflictKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c", "q", "Q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.fixCursor = clampIndex(m.fixCursor-1, 2)
		return m, nil
	case "down", "j":
		m.fixCursor = clampIndex(m.fixCursor+1, 2)
		return m, nil
	case "enter":
		if m.fixCursor == 1 {
			return m, tea.Quit
		}
		m.fixPreview = true
		m.previewOffset = 0
		m.message = ""
		return m, nil
	}
	return m, nil
}

// ConflictOpen 报告启动时是否检测到 orphan selection。
func (m Model) ConflictOpen() bool {
	return len(m.conflicts) > 0 && !m.fixPreview
}

// FixPreviewOpen 报告 conflict repair preview 是否打开。
func (m Model) FixPreviewOpen() bool {
	return m.fixPreview
}

// Conflicts 返回启动时 conflict 的独立副本。
func (m Model) Conflicts() []library.Conflict {
	return append([]library.Conflict(nil), m.conflicts...)
}

// FixRequest 返回交给 S2 repair callback 的完整、只读修复计划。
func (m Model) FixRequest() FixRequest {
	return FixRequest{
		Active:     cloneSelection(m.active),
		Reconciled: cloneSelection(m.initial),
		Conflicts:  m.Conflicts(),
		Diff:       repairDiff(m.active, m.initial),
	}
}

func repairDiff(active, reconciled library.Selection) Diff {
	diff := DiffSelections(active, reconciled)
	removedProviders := make(map[string]bool, len(diff.RemovedProviders))
	for _, providerID := range diff.RemovedProviders {
		removedProviders[providerID] = true
	}
	models := make([]ModelRef, 0, len(diff.RemovedModels))
	for _, model := range diff.RemovedModels {
		if !removedProviders[model.ProviderID] {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		diff.RemovedModels = nil
	} else {
		diff.RemovedModels = models
	}
	return diff
}

func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	if !m.Dirty() {
		return m, tea.Quit
	}
	m.confirmQuit = true
	return m, nil
}

func (m *Model) move(delta int) {
	if m.focus == ProvidersFocus {
		m.providerCursor = clampIndex(m.providerCursor+delta, len(m.providers))
		m.ensureProviderVisible()
		m.clampModelCursor()
		m.ensureModelVisible()
		return
	}
	m.modelCursor = clampIndex(m.modelCursor+delta, len(m.currentModels()))
	m.ensureModelVisible()
}

func (m *Model) toggleFocused() {
	if m.focus == ProvidersFocus {
		m.toggleProvider()
		return
	}
	m.toggleModel()
}

func (m *Model) toggleProvider() {
	provider, ok := m.currentProvider()
	if !ok {
		return
	}
	if m.providerFullySelected(provider) {
		delete(m.staged, provider.ID)
		return
	}
	m.staged[provider.ID] = providerModelIDs(provider)
}

func (m *Model) toggleModel() {
	provider, ok := m.currentProvider()
	if !ok {
		return
	}
	models := provider.Models
	if m.modelCursor < 0 || m.modelCursor >= len(models) {
		return
	}
	selected := selectionSet(m.staged[provider.ID])
	modelID := models[m.modelCursor].ID
	if selected[modelID] {
		delete(selected, modelID)
	} else {
		selected[modelID] = true
	}
	modelIDs := orderedModelIDs(models, selected)
	if len(modelIDs) == 0 {
		delete(m.staged, provider.ID)
		return
	}
	m.staged[provider.ID] = modelIDs
}

func (m *Model) clampModelCursor() {
	m.modelCursor = clampIndex(m.modelCursor, len(m.currentModels()))
}

func (m *Model) clampViewports() {
	m.providerCursor = clampIndex(m.providerCursor, len(m.providers))
	m.ensureProviderVisible()
	m.clampModelCursor()
	m.ensureModelVisible()
}

func (m *Model) ensureProviderVisible() {
	m.providerOffset = visibleOffset(m.providerCursor, m.providerOffset, len(m.providers), m.bodyHeight())
}

func (m *Model) ensureModelVisible() {
	m.modelOffset = visibleOffset(m.modelCursor, m.modelOffset, len(m.currentModels()), m.bodyHeight())
}

func (m *Model) movePreview(delta int) {
	contentHeight := len(m.previewContentLines())
	if m.fixPreview {
		contentHeight = len(m.fixPreviewContentLines())
	}
	bodyHeight := max(m.height-1, 1)
	maximum := max(contentHeight-bodyHeight, 0)
	m.previewOffset = clampIndex(m.previewOffset+delta, maximum+1)
}

func (m Model) currentProvider() (library.Provider, bool) {
	if m.providerCursor < 0 || m.providerCursor >= len(m.providers) {
		return library.Provider{}, false
	}
	return m.providers[m.providerCursor], true
}

func (m Model) currentModels() []library.Model {
	provider, ok := m.currentProvider()
	if !ok {
		return nil
	}
	return provider.Models
}

func (m Model) providerState(provider library.Provider) string {
	if !hasProvider(m.staged, provider.ID) {
		return "[ ]"
	}
	if m.providerFullySelected(provider) {
		return "[x]"
	}
	return "[-]"
}

func (m Model) providerFullySelected(provider library.Provider) bool {
	selected, enabled := m.staged[provider.ID]
	if !enabled {
		return false
	}
	if len(provider.Models) == 0 {
		return true
	}
	selectedSet := selectionSet(selected)
	for _, model := range provider.Models {
		if !selectedSet[model.ID] {
			return false
		}
	}
	return true
}

// StagedSelection 返回 staged selection 的独立副本。
func (m Model) StagedSelection() library.Selection {
	return cloneSelection(m.staged)
}

// Dirty 报告 staged selection 是否不同于启动时 active selection。
func (m Model) Dirty() bool {
	return !selectionEqual(m.initial, m.staged)
}

// Focus 返回当前焦点栏位。
func (m Model) Focus() FocusArea {
	return m.focus
}

// PreviewOpen 报告 Apply preview 是否打开。
func (m Model) PreviewOpen() bool {
	return m.preview
}

// QuitConfirmationOpen 报告脏退出确认是否打开。
func (m Model) QuitConfirmationOpen() bool {
	return m.confirmQuit
}

// DiffSelections 计算两个 selection 之间按 provider/model 划分的差异。
func DiffSelections(initial, staged library.Selection) Diff {
	var diff Diff
	providerIDs := make(map[string]struct{}, len(initial)+len(staged))
	for providerID := range initial {
		providerIDs[providerID] = struct{}{}
	}
	for providerID := range staged {
		providerIDs[providerID] = struct{}{}
	}
	for providerID := range providerIDs {
		initialModels, initiallyEnabled := initial[providerID]
		stagedModels, stagedEnabled := staged[providerID]
		if !initiallyEnabled && stagedEnabled {
			diff.AddedProviders = append(diff.AddedProviders, providerID)
		}
		if initiallyEnabled && !stagedEnabled {
			diff.RemovedProviders = append(diff.RemovedProviders, providerID)
		}
		initialSet := selectionSet(initialModels)
		stagedSet := selectionSet(stagedModels)
		for modelID := range stagedSet {
			if !initialSet[modelID] {
				diff.AddedModels = append(diff.AddedModels, ModelRef{ProviderID: providerID, ModelID: modelID})
			}
		}
		for modelID := range initialSet {
			if !stagedSet[modelID] {
				diff.RemovedModels = append(diff.RemovedModels, ModelRef{ProviderID: providerID, ModelID: modelID})
			}
		}
	}
	sort.Strings(diff.AddedProviders)
	sort.Strings(diff.RemovedProviders)
	sortModelRefs(diff.AddedModels)
	sortModelRefs(diff.RemovedModels)
	return diff
}

func (m Model) View() string {
	if m.confirmQuit {
		return m.fitViewLines([]string{"Discard staged changes? Enter/y confirms, Esc/n returns."})
	}
	if m.fixPreview {
		return m.fitViewLines(strings.Split(m.fixPreviewView(), "\n"))
	}
	if len(m.conflicts) > 0 {
		return m.fitViewLines(strings.Split(m.conflictView(), "\n"))
	}
	if m.preview {
		return m.fitViewLines(strings.Split(m.previewView(), "\n"))
	}
	return m.browserView()
}

func (m Model) conflictView() string {
	height := max(m.height, 1)
	fixCursor := " "
	quitCursor := " "
	if m.fixCursor == 0 {
		fixCursor = ">"
	} else {
		quitCursor = ">"
	}
	options := []string{
		fixCursor + " Fix and continue",
		quitCursor + " Quit without changes",
		"↑/↓ select  Enter continue",
	}
	if height <= len(options) {
		return strings.Join(options[:height], "\n")
	}

	details := make([]string, 0, len(m.conflicts))
	for _, conflict := range m.conflicts {
		switch conflict.Kind {
		case library.MissingProviderConflict:
			details = append(details, "[!] missing provider "+conflict.ProviderID)
		case library.MissingModelConflict:
			details = append(details, "[!] missing model "+conflict.ProviderID+"/"+conflict.ModelID)
		case library.EmptyModelSelectionConflict:
			details = append(details, "[!] provider "+conflict.ProviderID+" has no selected models")
		}
	}
	detailHeight := height - len(options)
	visible := max(detailHeight-1, 0)
	hidden := len(details) - visible
	header := fmt.Sprintf("Conflicts: %d", len(details))
	if hidden > 0 {
		header += fmt.Sprintf(" +%d", hidden)
	}
	lines := append([]string{header}, details[:min(visible, len(details))]...)
	lines = paddedLines(lines, detailHeight)
	lines = append(lines, options...)
	return strings.Join(lines, "\n")
}

func (m Model) fixPreviewView() string {
	footer := "↑/↓ scroll  Enter fix and apply  Esc back"
	if m.fixApplying {
		footer = "Fixing and applying..."
	}
	if m.height <= 1 {
		return footer
	}
	lines := m.fixPreviewContentLines()
	bodyHeight := m.height - 1
	start, end := viewportRange(m.previewOffset, len(lines), bodyHeight)
	lines = paddedLines(lines[start:end], bodyHeight)
	lines = append(lines, footer)
	return strings.Join(lines, "\n")
}

func (m Model) fixPreviewContentLines() []string {
	request := m.FixRequest()
	lines := []string{"Conflict repair preview"}
	for _, providerID := range request.Diff.RemovedProviders {
		lines = append(lines, "- provider "+providerID)
	}
	for _, model := range request.Diff.RemovedModels {
		lines = append(lines, "- model "+model.ProviderID+"/"+model.ModelID)
	}
	if m.fixApplying {
		lines = append(lines, "Fixing and applying...")
	}
	if m.message != "" {
		lines = append(lines, m.message)
	}
	return lines
}

func (m Model) browserView() string {
	width := max(m.width, 1)
	separator, leftWidth, rightWidth := columnLayout(width)
	bodyHeight := m.bodyHeight()

	left := m.providerLines(bodyHeight)
	right := m.modelLines(bodyHeight)
	lines := make([]string, bodyHeight)
	for index := range lines {
		lines[index] = fit(left[index], leftWidth) + separator + fit(right[index], rightWidth)
	}
	header := fit(focusLabel("Providers", m.focus == ProvidersFocus), leftWidth) + separator +
		fit(focusLabel("Models", m.focus == ModelsFocus), rightWidth)
	footer := "↑/↓ move  Space select  Enter/→ models  A preview  R reset  Esc/q quit"
	if m.message != "" {
		footer = m.message + "  " + footer
	}
	return header + "\n" + strings.Join(lines, "\n") + "\n" + fit(footer, width)
}

func (m Model) providerLines(height int) []string {
	if len(m.providers) == 0 {
		return paddedLines([]string{"(no providers)"}, height)
	}
	start, end := viewportRange(m.providerOffset, len(m.providers), height)
	lines := make([]string, 0, height)
	for index := start; index < end; index++ {
		provider := m.providers[index]
		cursor := " "
		if m.focus == ProvidersFocus && index == m.providerCursor {
			cursor = ">"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", cursor, m.providerState(provider), provider.ID))
	}
	return paddedLines(lines, height)
}

func (m Model) modelLines(height int) []string {
	provider, ok := m.currentProvider()
	if !ok {
		return paddedLines([]string{"(no provider selected)"}, height)
	}
	if len(provider.Models) == 0 {
		return paddedLines([]string{"(no models; provider can be selected)"}, height)
	}

	selected := selectionSet(m.staged[provider.ID])
	start, end := viewportRange(m.modelOffset, len(provider.Models), height)
	lines := make([]string, 0, height)
	for index := start; index < end; index++ {
		model := provider.Models[index]
		cursor := " "
		if m.focus == ModelsFocus && index == m.modelCursor {
			cursor = ">"
		}
		mark := "[ ]"
		if selected[model.ID] {
			mark = "[x]"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", cursor, mark, model.ID))
	}
	return paddedLines(lines, height)
}

func (m Model) fitViewLines(lines []string) string {
	width := max(m.width, 1)
	for index, line := range lines {
		lines[index] = fit(line, width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) previewView() string {
	lines := m.previewContentLines()
	bodyHeight := max(m.height-1, 1)
	start, end := viewportRange(m.previewOffset, len(lines), bodyHeight)
	lines = paddedLines(lines[start:end], bodyHeight)
	lines = append(lines, "↑/↓ scroll  Enter submit via Apply callback  Esc cancel")
	return strings.Join(lines, "\n")
}

func (m Model) previewContentLines() []string {
	diff := DiffSelections(m.initial, m.staged)
	lines := []string{"Apply preview"}
	for _, providerID := range diff.AddedProviders {
		lines = append(lines, "+ provider "+providerID)
	}
	for _, providerID := range diff.RemovedProviders {
		lines = append(lines, "- provider "+providerID)
	}
	for _, model := range diff.AddedModels {
		lines = append(lines, "+ model "+model.ProviderID+"/"+model.ModelID)
	}
	for _, model := range diff.RemovedModels {
		lines = append(lines, "- model "+model.ProviderID+"/"+model.ModelID)
	}
	if diff.Empty() {
		lines = append(lines, "No staged changes")
	}
	if m.applying {
		lines = append(lines, "Applying...")
	}
	if m.message != "" {
		lines = append(lines, m.message)
	}
	return lines
}

func cloneSelection(source library.Selection) library.Selection {
	copy := make(library.Selection, len(source))
	for providerID, modelIDs := range source {
		copy[providerID] = append([]string(nil), modelIDs...)
	}
	return copy
}

func selectionEqual(left, right library.Selection) bool {
	if len(left) != len(right) {
		return false
	}
	for providerID, leftModels := range left {
		rightModels, exists := right[providerID]
		if !exists || len(leftModels) != len(rightModels) {
			return false
		}
		leftSet := selectionSet(leftModels)
		rightSet := selectionSet(rightModels)
		if len(leftSet) != len(rightSet) {
			return false
		}
		for modelID := range leftSet {
			if !rightSet[modelID] {
				return false
			}
		}
	}
	return true
}

func providerModelIDs(provider library.Provider) []string {
	ids := make([]string, len(provider.Models))
	for index, model := range provider.Models {
		ids[index] = model.ID
	}
	return ids
}

func orderedModelIDs(models []library.Model, selected map[string]bool) []string {
	ids := make([]string, 0, len(selected))
	for _, model := range models {
		if selected[model.ID] {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func selectionSet(modelIDs []string) map[string]bool {
	set := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		set[modelID] = true
	}
	return set
}

func hasProvider(selection library.Selection, providerID string) bool {
	_, exists := selection[providerID]
	return exists
}

func clampIndex(index, length int) int {
	if length == 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func (m Model) bodyHeight() int {
	return max(m.height-2, 1)
}

func columnLayout(width int) (string, int, int) {
	switch {
	case width >= 5:
		separator := " | "
		contentWidth := width - len(separator)
		leftWidth := contentWidth / 2
		return separator, leftWidth, contentWidth - leftWidth
	case width >= 3:
		separator := "|"
		contentWidth := width - len(separator)
		leftWidth := contentWidth / 2
		return separator, leftWidth, contentWidth - leftWidth
	case width == 2:
		return "", 1, 1
	default:
		return "", 1, 0
	}
}

func visibleOffset(cursor, offset, length, height int) int {
	if length == 0 {
		return 0
	}
	cursor = clampIndex(cursor, length)
	height = max(height, 1)
	maximum := max(length-height, 0)
	offset = clampIndex(offset, maximum+1)
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+height {
		return cursor - height + 1
	}
	return offset
}

func viewportRange(offset, length, height int) (int, int) {
	if length == 0 {
		return 0, 0
	}
	height = max(height, 1)
	maximum := max(length-height, 0)
	offset = clampIndex(offset, maximum+1)
	return offset, min(offset+height, length)
}

func sortModelRefs(refs []ModelRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ProviderID == refs[j].ProviderID {
			return refs[i].ModelID < refs[j].ModelID
		}
		return refs[i].ProviderID < refs[j].ProviderID
	})
}

func paddedLines(lines []string, height int) []string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

func focusLabel(label string, focused bool) string {
	if focused {
		return "[" + label + "]"
	}
	return label
}

func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > width {
		if width == 1 {
			return string(runes[:1])
		}
		return string(runes[:width-1]) + "…"
	}
	return value + strings.Repeat(" ", width-len(runes))
}
