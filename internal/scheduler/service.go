package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gtimonitor/internal/gitops"
	"gtimonitor/internal/model"
)

type RepositoryStore interface {
	ListRepositories() []model.RepoConfig
	GetRepository(id string) (model.RepoConfig, bool)
	UpdateRepositorySyncState(id string, result model.SyncResult, lastError string) error
	GetCredential(id string) (model.Credential, bool, error)
	ResolveCredential(repo model.RepoConfig) (model.Credential, bool, error)
}

type Service struct {
	mu      sync.RWMutex
	store   RepositoryStore
	manager *gitops.Manager
	runners map[string]*runner
}

type runner struct {
	repo      model.RepoConfig
	cancel    context.CancelFunc
	lastRun   *model.SyncResult
	logs      []model.LogEntry
	running   bool
	nextRunAt *time.Time
}

func appendRunnerLogs(existing []model.LogEntry, additions []model.LogEntry) []model.LogEntry {
	if len(additions) == 0 {
		return existing
	}
	existing = append(existing, additions...)
	if len(existing) > 200 {
		existing = existing[len(existing)-200:]
	}
	return existing
}

func NewService(store RepositoryStore, manager *gitops.Manager) *Service {
	return &Service{
		store:   store,
		manager: manager,
		runners: map[string]*runner{},
	}
}

func (s *Service) Load(ctx context.Context) error {
	for _, repo := range s.store.ListRepositories() {
		if err := s.Upsert(ctx, repo); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Upsert(ctx context.Context, repo model.RepoConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.runners[repo.ID]; ok && existing.cancel != nil {
		existing.cancel()
	}
	r := &runner{repo: repo}
	s.runners[repo.ID] = r
	if repo.Enabled {
		s.startLocked(ctx, r)
	}
	return nil
}

func (s *Service) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.runners[id]; ok {
		if existing.cancel != nil {
			existing.cancel()
		}
		delete(s.runners, id)
	}
}

func (s *Service) Trigger(ctx context.Context, repoID string) (*model.SyncResult, error) {
	s.mu.Lock()
	r, ok := s.runners[repoID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("repository not found")
	}
	if r.running {
		s.mu.Unlock()
		return nil, fmt.Errorf("repository sync already running")
	}
	r.running = true
	s.mu.Unlock()

	result, err := s.runSync(ctx, r.repo)

	s.mu.Lock()
	r.running = false
	if updated, ok := s.store.GetRepository(repoID); ok {
		r.repo = updated
	}
	r.lastRun = &result
	r.logs = appendRunnerLogs(r.logs, result.Logs)
	s.mu.Unlock()
	return &result, err
}

func (s *Service) GetStatuses() []model.RepoStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	statuses := make([]model.RepoStatus, 0, len(s.runners))
	for _, r := range s.runners {
		status := model.RepoStatus{
			Repo:      r.repo,
			LastRun:   r.lastRun,
			Running:   r.running,
			NextRunAt: r.nextRunAt,
			Logs:      append([]model.LogEntry(nil), r.logs...),
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runners {
		if r.cancel != nil {
			r.cancel()
		}
	}
}

func (s *Service) startLocked(parent context.Context, r *runner) {
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	go func() {
		ticker := time.NewTicker(time.Duration(r.repo.SyncIntervalSec) * time.Second)
		defer ticker.Stop()
		for {
			next := time.Now().Add(time.Duration(r.repo.SyncIntervalSec) * time.Second)
			s.mu.Lock()
			r.nextRunAt = &next
			s.mu.Unlock()

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				if r.running {
					s.mu.Unlock()
					continue
				}
				r.running = true
				s.mu.Unlock()

				result, _ := s.runSync(context.Background(), r.repo)

				s.mu.Lock()
				r.running = false
				if updated, ok := s.store.GetRepository(r.repo.ID); ok {
					r.repo = updated
				}
				r.lastRun = &result
				r.logs = appendRunnerLogs(r.logs, result.Logs)
				s.mu.Unlock()
			}
		}
	}()
}

func (s *Service) runSync(ctx context.Context, repo model.RepoConfig) (model.SyncResult, error) {
	var cred *model.Credential
	loaded, ok, err := s.store.ResolveCredential(repo)
	if err != nil {
		result := model.SyncResult{StartedAt: time.Now(), FinishedAt: time.Now(), Status: "error", Message: err.Error()}
		return result, err
	}
	if ok {
		cred = &loaded
	}
	result, err := s.manager.Sync(ctx, repo, cred)
	lastError := ""
	if err != nil {
		lastError = err.Error()
	}
	_ = s.store.UpdateRepositorySyncState(repo.ID, result, lastError)
	return result, err
}
