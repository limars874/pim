// Package apply 原子生成并验证 pi 的 models.json。
package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/limars874/pim/library"
)

const defaultVerifyTimeout = 20 * time.Second

// Verifier 验证已写入的 models.json 是否能被 pi 读取。
type Verifier func(context.Context, library.Paths, library.Selection) error

// Service 配置生成、备份、原子替换与 verifier。
type Service struct {
	Paths         library.Paths
	Verify        Verifier
	VerifyTimeout time.Duration
	Now           func() time.Time

	// SyncDirectory 仅用于测试或替代 filesystem durability primitive。nil 时
	// 使用目录自身的 Sync；每个 Service 独立持有该依赖，避免全局测试竞态。
	SyncDirectory func(string) error
}

// Result 描述一次已完成或已回滚 Apply 的文件位置。
type Result struct {
	TargetPath string
	BackupPath string
}

// Apply 生成 staged selection，备份旧文件后原子替换。verifier 失败时会使用
// 已 Sync 的备份原子回滚，并在 error 中同时报告验证和回滚结果。
func (s Service) Apply(ctx context.Context, source library.Library, selection library.Selection) (Result, error) {
	generated, err := library.GenerateModelsJSON(source, selection)
	if err != nil {
		return Result{}, fmt.Errorf("generate models.json: %w", err)
	}

	target := s.Paths.ModelsPath()
	old, mode, existed, err := readTarget(target)
	if err != nil {
		return Result{TargetPath: target}, err
	}
	result := Result{TargetPath: target}
	if existed {
		backupPath, err := writeBackupWithSync(s.historyDir(), old, s.now(), s.syncDirectory)
		if err != nil {
			return result, err
		}
		result.BackupPath = backupPath
	}

	committed, replaceErr := s.replace(target, generated, mode)
	if replaceErr != nil {
		if !committed {
			return result, fmt.Errorf("atomically replace models.json: %w", replaceErr)
		}
		rollbackErr := s.rollback(target, result.BackupPath, mode, existed)
		if rollbackErr != nil {
			return result, fmt.Errorf("replace failed after commit: %w; rollback failed: %v", replaceErr, rollbackErr)
		}
		return result, fmt.Errorf("replace failed after commit: %w; rollback succeeded", replaceErr)
	}
	if err := s.verify(ctx, selection); err != nil {
		rollbackErr := s.rollback(target, result.BackupPath, mode, existed)
		if rollbackErr != nil {
			return result, fmt.Errorf("verification failed: %w; rollback failed: %v", err, rollbackErr)
		}
		return result, fmt.Errorf("verification failed: %w; rollback succeeded", err)
	}
	return result, nil
}

func (s Service) verify(ctx context.Context, selection library.Selection) error {
	verifier := s.Verify
	if verifier == nil {
		verifier = VerifyWithPi
	}
	timeout := s.VerifyTimeout
	if timeout <= 0 {
		timeout = defaultVerifyTimeout
	}
	verifyContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return verifier(verifyContext, s.Paths, selection)
}

func (s Service) replace(target string, contents []byte, mode os.FileMode) (bool, error) {
	return atomicReplaceWithSync(target, contents, mode, s.syncDirectory)
}

func (s Service) rollback(target, backupPath string, mode os.FileMode, existed bool) error {
	if !existed {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove unverified models.json: %w", err)
		}
		if err := s.syncDirectory(filepath.Dir(target)); err != nil {
			return fmt.Errorf("sync agent directory after removing unverified models.json: %w", err)
		}
		return nil
	}
	contents, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	_, err = s.replace(target, contents, mode)
	return err
}

func (s Service) syncDirectory(dir string) error {
	if s.SyncDirectory != nil {
		return s.SyncDirectory(dir)
	}
	return syncDirectory(dir)
}

func (s Service) historyDir() string {
	return filepath.Join(s.Paths.AgentDir, "model-library", "history")
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func readTarget(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0o600, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("stat models.json: %w", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read models.json: %w", err)
	}
	return contents, info.Mode().Perm(), true, nil
}

func writeBackup(dir string, contents []byte, now time.Time) (string, error) {
	return writeBackupWithSync(dir, contents, now, syncDirectory)
}

func writeBackupWithSync(dir string, contents []byte, now time.Time, sync func(string) error) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create history directory: %w", err)
	}
	stamp := now.Format("20060102T150405.000000000Z")
	for attempt := 0; ; attempt++ {
		name := "models-" + stamp
		if attempt > 0 {
			name += fmt.Sprintf("-%d", attempt)
		}
		path := filepath.Join(dir, name+".json")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create backup: %w", err)
		}
		if err := writeAndSync(file, contents); err != nil {
			return "", fmt.Errorf("write backup: %w", err)
		}
		if err := syncHistoryDirectories(dir, sync); err != nil {
			return "", fmt.Errorf("sync history directories: %w", err)
		}
		return path, nil
	}
}

func atomicReplace(target string, contents []byte, mode os.FileMode) (bool, error) {
	return atomicReplaceWithSync(target, contents, mode, syncDirectory)
}

func atomicReplaceWithSync(target string, contents []byte, mode os.FileMode, sync func(string) error) (committed bool, err error) {
	dir := filepath.Dir(target)
	file, err := os.CreateTemp(dir, ".models.json-")
	if err != nil {
		return false, fmt.Errorf("create temporary models.json: %w", err)
	}
	temporary := file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("chmod temporary models.json: %w", err)
	}
	if err := writeAndSync(file, contents); err != nil {
		return false, fmt.Errorf("write temporary models.json: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		return false, fmt.Errorf("rename temporary models.json: %w", err)
	}
	committed = true
	if err := sync(dir); err != nil {
		return true, fmt.Errorf("sync agent directory: %w", err)
	}
	return true, nil
}

func writeAndSync(file *os.File, contents []byte) error {
	written, err := file.Write(contents)
	if err == nil && written != len(contents) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func syncHistoryDirectories(historyDir string, sync func(string) error) error {
	for dir, count := historyDir, 0; count < 3; dir, count = filepath.Dir(dir), count+1 {
		if err := sync(dir); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

// VerifyWithPi 使用隔离环境运行 pi --list-models，并检查每个 selected
// provider/model pair 都出现在表格中。没有 models 的 provider 只要求命令成功。
func VerifyWithPi(ctx context.Context, paths library.Paths, selection library.Selection) error {
	command := exec.CommandContext(ctx, "pi", "--list-models")
	command.Env = commandEnvironment(paths.AgentDir)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("pi --list-models timed out or was canceled: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("pi --list-models failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	available := listedModels(string(output))
	for providerID, modelIDs := range selection {
		for _, modelID := range modelIDs {
			if !available[providerID+"\x00"+modelID] {
				return fmt.Errorf("pi --list-models is missing selected model %s/%s", providerID, modelID)
			}
		}
	}
	return nil
}

func commandEnvironment(agentDir string) []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PI_CODING_AGENT_DIR=") || strings.HasPrefix(entry, "PI_OFFLINE=") || strings.HasPrefix(entry, "PI_SKIP_VERSION_CHECK=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"PI_CODING_AGENT_DIR="+agentDir,
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
	)
}

func listedModels(output string) map[string]bool {
	models := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "provider" && fields[1] == "model" {
			continue
		}
		models[fields[0]+"\x00"+fields[1]] = true
	}
	return models
}
