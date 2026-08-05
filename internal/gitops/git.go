package gitops

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gtimonitor/internal/model"
)

type CredentialProvider interface {
	GetCredential(id string) (model.Credential, bool, error)
}

type Manager struct {
	repoRoot     string
	gitUserName  string
	gitUserEmail string
}

type CommitTestResult struct {
	Ready   bool
	Message string
}

type LocalRepository struct {
	Name      string
	RemoteURL string
	Branch    string
	LocalPath string
	Revision  string
}

func NewManager(repoRoot, gitUserName, gitUserEmail string) *Manager {
	return &Manager{
		repoRoot:     repoRoot,
		gitUserName:  strings.TrimSpace(gitUserName),
		gitUserEmail: strings.TrimSpace(gitUserEmail),
	}
}

func (m *Manager) EnsureRepoRoot() error {
	return os.MkdirAll(m.repoRoot, 0o755)
}

func (m *Manager) ResolvePath(repoName string) string {
	return filepath.Join(m.repoRoot, repoName)
}

func (m *Manager) ScanLocalRepos(ctx context.Context) ([]LocalRepository, error) {
	if err := m.EnsureRepoRoot(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.repoRoot)
	if err != nil {
		return nil, err
	}

	repos := make([]LocalRepository, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		localPath := filepath.Join(m.repoRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(localPath, ".git")); err != nil {
			continue
		}

		remoteURL, _, err := m.runGit(ctx, localPath, nil, "remote", "get-url", "origin")
		if err != nil {
			continue
		}

		branch, _, err := m.runGit(ctx, localPath, nil, "branch", "--show-current")
		if err != nil || strings.TrimSpace(branch) == "" {
			branch = "main"
		}

		revision, _, err := m.runGit(ctx, localPath, nil, "rev-parse", "HEAD")
		if err != nil {
			revision = ""
		}

		repos = append(repos, LocalRepository{
			Name:      entry.Name(),
			RemoteURL: strings.TrimSpace(remoteURL),
			Branch:    strings.TrimSpace(branch),
			LocalPath: localPath,
			Revision:  strings.TrimSpace(revision),
		})
	}

	return repos, nil
}

func (m *Manager) Clone(ctx context.Context, repo model.RepoConfig, cred *model.Credential) error {
	if err := m.EnsureRepoRoot(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo.LocalPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(repo.LocalPath), 0o755); err != nil {
		return err
	}

	args := []string{"clone", "--branch", repo.Branch, "--single-branch", repo.RemoteURL, repo.LocalPath}
	_, _, err := m.runGit(ctx, "", cred, args...)
	return err
}

func (m *Manager) TestPull(ctx context.Context, repo model.RepoConfig, cred *model.Credential) error {
	args := []string{"ls-remote", "--heads", repo.RemoteURL, repo.Branch}
	stdout, _, err := m.runGit(ctx, "", cred, args...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(stdout) == "" {
		return fmt.Errorf("branch %s not found on remote", repo.Branch)
	}
	return nil
}

func (m *Manager) TestCommit(ctx context.Context, repo model.RepoConfig, cred *model.Credential) (CommitTestResult, error) {
	if _, err := os.Stat(filepath.Join(repo.LocalPath, ".git")); err != nil {
		return CommitTestResult{Ready: false, Message: "本地仓库不存在，需先完成 clone"}, nil
	}
	if _, _, err := m.runGit(ctx, repo.LocalPath, cred, "rev-parse", "--is-inside-work-tree"); err != nil {
		return CommitTestResult{}, err
	}
	if err := m.ensureRepoIdentity(ctx, repo.LocalPath, cred); err != nil {
		return CommitTestResult{}, err
	}

	name, _, err := m.runGit(ctx, repo.LocalPath, cred, "config", "--get", "user.name")
	if err != nil || strings.TrimSpace(name) == "" {
		return CommitTestResult{Ready: false, Message: "缺少 git user.name 配置"}, nil
	}
	email, _, err := m.runGit(ctx, repo.LocalPath, cred, "config", "--get", "user.email")
	if err != nil || strings.TrimSpace(email) == "" {
		return CommitTestResult{Ready: false, Message: "缺少 git user.email 配置"}, nil
	}

	dirty, err := m.isDirty(ctx, repo.LocalPath, cred)
	if err != nil {
		return CommitTestResult{}, err
	}
	if !dirty {
		return CommitTestResult{Ready: true, Message: "可提交，当前无待提交文件"}, nil
	}

	if err := m.ensureAutoCommitSafe(ctx, repo.LocalPath, cred); err != nil {
		return CommitTestResult{Ready: false, Message: err.Error()}, nil
	}
	message, err := m.previewCommitMessage(ctx, repo.LocalPath, cred)
	if err != nil {
		return CommitTestResult{}, err
	}
	return CommitTestResult{Ready: true, Message: "可提交，提交信息将为: " + message}, nil
}

func (m *Manager) Sync(ctx context.Context, repo model.RepoConfig, cred *model.Credential) (model.SyncResult, error) {
	result := model.SyncResult{
		StartedAt: time.Now(),
		Status:    "running",
	}
	appendStepLog(&result, "info", "开始同步仓库")

	if _, err := os.Stat(filepath.Join(repo.LocalPath, ".git")); err != nil {
		appendStepLog(&result, "info", "本地仓库不存在，开始 clone")
		if err := m.Clone(ctx, repo, cred); err != nil {
			appendStepLog(&result, "error", "clone 失败: "+err.Error())
			return m.finish(result, "error", err.Error(), false, false, false, false, ""), err
		}
		appendStepLog(&result, "info", "clone 完成")
	}
	if err := m.ensureRepoIdentity(ctx, repo.LocalPath, cred); err != nil {
		appendStepLog(&result, "error", "配置 git 提交身份失败: "+err.Error())
		return m.finish(result, "error", err.Error(), false, false, false, false, ""), err
	}
	appendStepLog(&result, "info", "git 提交身份已确认")

	headBefore, _, err := m.runGit(ctx, repo.LocalPath, cred, "rev-parse", "HEAD")
	if err != nil {
		headBefore = ""
	}

	if dirty, err := m.isDirty(ctx, repo.LocalPath, cred); err == nil && dirty {
		if !repo.AutoCommitEnabled {
			err := errors.New("working tree has local changes while auto commit is disabled")
			appendStepLog(&result, "error", "检测到本地改动，但自动提交已禁用")
			return m.finish(result, "error", err.Error(), false, false, false, false, ""), err
		}
		appendStepLog(&result, "info", "检测到本地改动，开始自动提交")
		if err := m.autoCommit(ctx, repo, cred); err != nil {
			appendStepLog(&result, "error", "自动提交失败: "+err.Error())
			return m.finish(result, "error", err.Error(), false, false, false, false, ""), err
		}
		result.LocalCommitted = true
		appendStepLog(&result, "info", "自动提交完成")
	} else {
		appendStepLog(&result, "info", "本地工作区无待提交改动")
	}

	if repo.AutoPullEnabled {
		appendStepLog(&result, "info", "开始拉取远端更新")
		fetchHead, remoteUpdated, err := m.fetch(ctx, repo, cred, headBefore)
		if err != nil {
			finishedAt := time.Now()
			result.PullAttempted = true
			result.PullSucceeded = false
			result.PullFinishedAt = &finishedAt
			result.PullMessage = err.Error()
			appendStepLog(&result, "error", "拉取远端失败: "+err.Error())
			return m.finish(result, "error", err.Error(), false, result.LocalCommitted, false, false, ""), err
		}
		result.RemoteUpdated = remoteUpdated
		result.PullAttempted = true
		result.PullSucceeded = true
		result.PullMessage = ""
		finishedAt := time.Now()
		result.PullFinishedAt = &finishedAt

		if remoteUpdated {
			appendStepLog(&result, "info", "检测到远端更新，开始合并")
			resolved, err := m.mergeRemote(ctx, repo, cred, fetchHead)
			if err != nil {
				finishedAt := time.Now()
				result.PullSucceeded = false
				result.PullFinishedAt = &finishedAt
				result.PullMessage = err.Error()
				appendStepLog(&result, "error", "合并远端更新失败: "+err.Error())
				return m.finish(result, "error", err.Error(), true, result.LocalCommitted, false, resolved, ""), err
			}
			result.ResolvedConflicts = resolved
			finishedAt := time.Now()
			result.PullFinishedAt = &finishedAt
			result.PullMessage = ""
			if resolved {
				appendStepLog(&result, "info", "合并完成，已自动解决冲突")
			} else {
				appendStepLog(&result, "info", "合并完成")
			}
		} else {
			appendStepLog(&result, "info", "远端无新提交")
		}
	} else {
		appendStepLog(&result, "info", "自动拉取已禁用")
	}

	result.PushAttempted = true
	appendStepLog(&result, "info", "开始检查是否需要推送")
	pushAttempted, err := m.pushIfNeeded(ctx, repo, cred)
	if err != nil {
		finishedAt := time.Now()
		result.PushSucceeded = false
		result.PushFinishedAt = &finishedAt
		result.PushMessage = err.Error()
		appendStepLog(&result, "error", "推送失败: "+err.Error())
		return m.finish(result, "error", err.Error(), result.RemoteUpdated, result.LocalCommitted, false, result.ResolvedConflicts, ""), err
	}
	result.PushSucceeded = true
	finishedAt := time.Now()
	result.PushFinishedAt = &finishedAt
	result.PushMessage = ""
	result.Pushed = pushAttempted
	if pushAttempted {
		appendStepLog(&result, "info", "推送完成")
	} else {
		appendStepLog(&result, "info", "无需推送，本地与远端已同步")
	}

	headAfter, _, err := m.runGit(ctx, repo.LocalPath, cred, "rev-parse", "HEAD")
	if err == nil && strings.TrimSpace(headAfter) != strings.TrimSpace(headBefore) {
		result.RepositoryRevision = strings.TrimSpace(headAfter)
	}
	return m.finish(result, "ok", "sync completed", result.RemoteUpdated, result.LocalCommitted, result.Pushed, result.ResolvedConflicts, strings.TrimSpace(headAfter)), nil
}

func (m *Manager) fetch(ctx context.Context, repo model.RepoConfig, cred *model.Credential, previousHead string) (string, bool, error) {
	if _, _, err := m.runGit(ctx, repo.LocalPath, cred, "fetch", "origin", repo.Branch); err != nil {
		return "", false, err
	}
	fetchHead, _, err := m.runGit(ctx, repo.LocalPath, cred, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(previousHead) == "" {
		return strings.TrimSpace(fetchHead), true, nil
	}

	if strings.TrimSpace(fetchHead) == strings.TrimSpace(previousHead) {
		return strings.TrimSpace(fetchHead), false, nil
	}

	diffOutput, _, err := m.runGit(ctx, repo.LocalPath, cred, "rev-list", "--left-right", "--count", fmt.Sprintf("%s...%s", strings.TrimSpace(previousHead), strings.TrimSpace(fetchHead)))
	if err != nil {
		return "", false, err
	}
	parts := strings.Fields(diffOutput)
	if len(parts) != 2 {
		return "", false, fmt.Errorf("unexpected rev-list output: %q", strings.TrimSpace(diffOutput))
	}

	remoteOnly, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", false, fmt.Errorf("parse remote-only commit count: %w", err)
	}
	if remoteOnly > 0 {
		return strings.TrimSpace(fetchHead), true, nil
	}

	return strings.TrimSpace(fetchHead), false, nil
}

func (m *Manager) mergeRemote(ctx context.Context, repo model.RepoConfig, cred *model.Credential, fetchHead string) (bool, error) {
	_, stderr, err := m.runGit(ctx, repo.LocalPath, cred, "merge", "--no-ff", "--no-edit", fetchHead)
	if err == nil {
		return false, nil
	}
	if !strings.Contains(stderr, "CONFLICT") {
		return false, err
	}
	if err := m.resolveConflictsByTimestamp(ctx, repo, cred, fetchHead); err != nil {
		return false, m.abortMerge(ctx, repo.LocalPath, cred, err)
	}
	if _, _, err := m.runGit(ctx, repo.LocalPath, cred, "commit", "--no-edit", "-m", "chore(sync): auto-resolve merge conflicts"); err != nil {
		return false, m.abortMerge(ctx, repo.LocalPath, cred, err)
	}
	return true, nil
}

func (m *Manager) resolveConflictsByTimestamp(ctx context.Context, repo model.RepoConfig, cred *model.Credential, fetchHead string) error {
	files, _, err := m.runGit(ctx, repo.LocalPath, cred, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return err
	}
	paths := splitLines(files)
	for _, path := range paths {
		oursTime := m.commitTime(ctx, repo.LocalPath, cred, "HEAD", path)
		theirsTime := m.commitTime(ctx, repo.LocalPath, cred, fetchHead, path)
		localInfo, statErr := os.Stat(filepath.Join(repo.LocalPath, path))
		if statErr == nil && localInfo.ModTime().After(oursTime) {
			oursTime = localInfo.ModTime()
		}

		side := "--ours"
		if theirsTime.After(oursTime) {
			side = "--theirs"
		}
		if _, _, err := m.runGit(ctx, repo.LocalPath, cred, "checkout", side, "--", path); err != nil {
			return err
		}
		if _, _, err := m.runGit(ctx, repo.LocalPath, cred, "add", "--", path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) pushIfNeeded(ctx context.Context, repo model.RepoConfig, cred *model.Credential) (bool, error) {
	aheadOutput, _, err := m.runGit(ctx, repo.LocalPath, cred, "rev-list", "--left-right", "--count", fmt.Sprintf("origin/%s...HEAD", repo.Branch))
	if err != nil {
		return false, nil
	}
	parts := strings.Fields(aheadOutput)
	if len(parts) != 2 {
		return false, nil
	}
	ahead, err := strconv.Atoi(parts[1])
	if err != nil || ahead == 0 {
		return false, nil
	}
	_, _, err = m.runGit(ctx, repo.LocalPath, cred, "push", "origin", repo.Branch)
	return true, err
}

func (m *Manager) autoCommit(ctx context.Context, repo model.RepoConfig, cred *model.Credential) error {
	if !repo.AutoCommitEnabled {
		return nil
	}
	if err := m.ensureAutoCommitSafe(ctx, repo.LocalPath, cred); err != nil {
		return err
	}
	if _, _, err := m.runGit(ctx, repo.LocalPath, cred, "add", "-A"); err != nil {
		return err
	}
	message, err := m.buildCommitMessage(ctx, repo.LocalPath, cred)
	if err != nil {
		return err
	}
	if _, stderr, err := m.runGit(ctx, repo.LocalPath, cred, "commit", "-m", message); err != nil {
		if strings.Contains(stderr, "nothing to commit") {
			return nil
		}
		return err
	}
	return nil
}

func (m *Manager) buildCommitMessage(ctx context.Context, repoPath string, cred *model.Credential) (string, error) {
	if _, _, err := m.runGit(ctx, repoPath, cred, "add", "-A"); err != nil {
		return "", err
	}
	return m.previewCommitMessage(ctx, repoPath, cred)
}

func (m *Manager) previewCommitMessage(ctx context.Context, repoPath string, cred *model.Credential) (string, error) {
	out, _, err := m.runGit(ctx, repoPath, cred, "diff", "--cached", "--name-only")
	if err != nil {
		return "", err
	}
	files := splitLines(out)
	if len(files) == 0 {
		return "GitMonitor: update", nil
	}

	message := "GitMonitor: " + strings.Join(files, ", ")
	if len(message) <= 180 {
		return message, nil
	}

	trimmed := []string{}
	length := len("GitMonitor: ")
	for _, file := range files {
		addition := len(file)
		if len(trimmed) > 0 {
			addition += 2
		}
		if length+addition > 170 {
			break
		}
		trimmed = append(trimmed, file)
		length += addition
	}
	if len(trimmed) == 0 {
		return "GitMonitor: " + filepath.Base(files[0]), nil
	}
	return "GitMonitor: " + strings.Join(trimmed, ", ") + " ...", nil
}

func (m *Manager) isDirty(ctx context.Context, repoPath string, cred *model.Credential) (bool, error) {
	out, _, err := m.runGit(ctx, repoPath, cred, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) ensureAutoCommitSafe(ctx context.Context, repoPath string, cred *model.Credential) error {
	if operation, ok, err := m.gitOperationInProgress(ctx, repoPath, cred); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("repository has unfinished %s; resolve or abort it before automatic commit", operation)
	}

	unmerged, err := m.unmergedFiles(ctx, repoPath, cred)
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		return fmt.Errorf("repository has unmerged files: %s", strings.Join(unmerged, ", "))
	}

	marked, err := m.filesWithConflictMarkers(ctx, repoPath, cred)
	if err != nil {
		return err
	}
	if len(marked) > 0 {
		return fmt.Errorf("working tree contains conflict markers: %s", strings.Join(marked, ", "))
	}
	return nil
}

func (m *Manager) gitOperationInProgress(ctx context.Context, repoPath string, cred *model.Credential) (string, bool, error) {
	checks := []struct {
		name string
		path string
	}{
		{name: "merge", path: "MERGE_HEAD"},
		{name: "cherry-pick", path: "CHERRY_PICK_HEAD"},
		{name: "revert", path: "REVERT_HEAD"},
		{name: "rebase", path: "rebase-merge"},
		{name: "rebase", path: "rebase-apply"},
	}
	for _, check := range checks {
		path, _, err := m.runGit(ctx, repoPath, cred, "rev-parse", "--git-path", check.path)
		if err != nil {
			return "", false, err
		}
		if _, err := os.Stat(resolveGitPath(repoPath, strings.TrimSpace(path))); err == nil {
			return check.name, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	return "", false, nil
}

func (m *Manager) unmergedFiles(ctx context.Context, repoPath string, cred *model.Credential) ([]string, error) {
	out, _, err := m.runGit(ctx, repoPath, cred, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (m *Manager) filesWithConflictMarkers(ctx context.Context, repoPath string, cred *model.Credential) ([]string, error) {
	paths, err := m.dirtyFilePaths(ctx, repoPath, cred)
	if err != nil {
		return nil, err
	}

	marked := []string{}
	for _, path := range paths {
		hasMarkers, err := hasConflictMarkers(filepath.Join(repoPath, path))
		if err != nil {
			return nil, err
		}
		if hasMarkers {
			marked = append(marked, path)
		}
	}
	return marked, nil
}

func (m *Manager) dirtyFilePaths(ctx context.Context, repoPath string, cred *model.Credential) ([]string, error) {
	commands := [][]string{
		{"diff", "--name-only"},
		{"diff", "--cached", "--name-only"},
		{"ls-files", "--others", "--exclude-standard"},
	}
	seen := map[string]bool{}
	paths := []string{}
	for _, args := range commands {
		out, _, err := m.runGit(ctx, repoPath, cred, args...)
		if err != nil {
			return nil, err
		}
		for _, path := range splitLines(out) {
			if seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func hasConflictMarkers(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sawStart := false
	sawSeparator := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "<<<<<<< "):
			sawStart = true
			sawSeparator = false
		case sawStart && strings.HasPrefix(line, "======="):
			sawSeparator = true
		case sawStart && sawSeparator && strings.HasPrefix(line, ">>>>>>> "):
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func resolveGitPath(repoPath, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(repoPath, path)
}

func (m *Manager) abortMerge(ctx context.Context, repoPath string, cred *model.Credential, cause error) error {
	if _, _, err := m.runGit(ctx, repoPath, cred, "merge", "--abort"); err != nil {
		return fmt.Errorf("%w; additionally failed to abort merge: %v", cause, err)
	}
	return cause
}

func (m *Manager) ensureRepoIdentity(ctx context.Context, repoPath string, cred *model.Credential) error {
	name, _, err := m.runGit(ctx, repoPath, cred, "config", "--get", "user.name")
	if err == nil && strings.TrimSpace(name) != "" {
		email, _, emailErr := m.runGit(ctx, repoPath, cred, "config", "--get", "user.email")
		if emailErr == nil && strings.TrimSpace(email) != "" {
			return nil
		}
	}

	if m.gitUserName == "" || m.gitUserEmail == "" {
		return nil
	}

	currentName := strings.TrimSpace(name)
	if currentName == "" {
		if _, _, err := m.runGit(ctx, repoPath, cred, "config", "user.name", m.gitUserName); err != nil {
			return err
		}
	}

	email, _, emailErr := m.runGit(ctx, repoPath, cred, "config", "--get", "user.email")
	currentEmail := ""
	if emailErr == nil {
		currentEmail = strings.TrimSpace(email)
	}
	if currentEmail == "" {
		if _, _, err := m.runGit(ctx, repoPath, cred, "config", "user.email", m.gitUserEmail); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) commitTime(ctx context.Context, repoPath string, cred *model.Credential, revision, path string) time.Time {
	out, _, err := m.runGit(ctx, repoPath, cred, "log", "-1", "--format=%ct", revision, "--", path)
	if err != nil {
		return time.Time{}
	}
	unixSec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(unixSec, 0)
}

func (m *Manager) runGit(ctx context.Context, repoPath string, cred *model.Credential, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repoPath
	command.Env = append(os.Environ(), "LC_ALL=C")

	cleanup, env, err := prepareCredentialEnv(cred)
	if err != nil {
		return "", "", err
	}
	if cleanup != nil {
		defer cleanup()
	}
	command.Env = append(command.Env, env...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err = command.Run()
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), nil
}

func prepareCredentialEnv(cred *model.Credential) (func(), []string, error) {
	if cred == nil {
		return nil, nil, nil
	}
	switch cred.Type {
	case model.CredentialTypeSSH:
		keyFile, err := os.CreateTemp("", "gti-key-*")
		if err != nil {
			return nil, nil, err
		}
		if err := keyFile.Chmod(0o600); err != nil {
			keyFile.Close()
			return nil, nil, err
		}
		if _, err := keyFile.WriteString(cred.PrivateKey); err != nil {
			keyFile.Close()
			return nil, nil, err
		}
		keyFile.Close()

		cleanup := func() {
			_ = os.Remove(keyFile.Name())
		}
		env := []string{
			fmt.Sprintf(`GIT_SSH_COMMAND=ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new`, keyFile.Name()),
		}
		return cleanup, env, nil
	case model.CredentialTypeHTTP:
		scriptFile, err := os.CreateTemp("", "gti-askpass-*")
		if err != nil {
			return nil, nil, err
		}
		content := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n*Username*) printf '%%s' '%s' ;;\n*) printf '%%s' '%s' ;;\nesac\n", escapeShell(cred.Username), escapeShell(cred.Password))
		if _, err := scriptFile.WriteString(content); err != nil {
			scriptFile.Close()
			return nil, nil, err
		}
		scriptFile.Close()
		if err := os.Chmod(scriptFile.Name(), 0o700); err != nil {
			return nil, nil, err
		}

		cleanup := func() {
			_ = os.Remove(scriptFile.Name())
		}
		env := []string{
			"GIT_TERMINAL_PROMPT=0",
			fmt.Sprintf("GIT_ASKPASS=%s", scriptFile.Name()),
		}
		return cleanup, env, nil
	default:
		return nil, nil, fmt.Errorf("unsupported credential type %s", cred.Type)
	}
}

func escapeShell(value string) string {
	return strings.ReplaceAll(value, "'", "'\"'\"'")
}

func splitLines(value string) []string {
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (m *Manager) finish(result model.SyncResult, status, message string, remoteUpdated, localCommitted, pushed, resolved bool, revision string) model.SyncResult {
	result.FinishedAt = time.Now()
	result.Status = status
	result.Message = message
	result.RemoteUpdated = remoteUpdated
	result.LocalCommitted = localCommitted
	result.Pushed = pushed
	result.ResolvedConflicts = resolved
	result.RepositoryRevision = revision
	appendStepLog(&result, status, message)
	return result
}

func appendStepLog(result *model.SyncResult, level, message string) {
	result.Logs = append(result.Logs, model.LogEntry{
		Time:    time.Now(),
		Level:   level,
		Message: message,
	})
}
