package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gtimonitor/internal/auth"
	"gtimonitor/internal/model"
)

const (
	configFileName      = "config.json"
	credentialFileName  = "credentials.json"
	minSyncIntervalSec  = 30
	defaultCommitFormat = "chore(sync): auto commit from GTI Monitor"
)

type Store struct {
	mu          sync.RWMutex
	configDir   string
	configPath  string
	credPath    string
	masterKey   []byte
	appConfig   model.AppConfig
	credentials map[string]model.Credential
}

func NewStore(configDir string) (*Store, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	key, err := auth.LoadOrCreateKey(configDir)
	if err != nil {
		return nil, err
	}

	s := &Store{
		configDir:   configDir,
		configPath:  filepath.Join(configDir, configFileName),
		credPath:    filepath.Join(configDir, credentialFileName),
		masterKey:   key,
		credentials: map[string]model.Credential{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ConfigDir() string {
	return s.configDir
}

func (s *Store) ListRepositories() []model.RepoConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.RepoConfig, len(s.appConfig.Repositories))
	copy(out, s.appConfig.Repositories)
	return out
}

func (s *Store) GetRepository(id string) (model.RepoConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, repo := range s.appConfig.Repositories {
		if repo.ID == id {
			return repo, true
		}
	}
	return model.RepoConfig{}, false
}

func (s *Store) SaveRepository(repo model.RepoConfig) (model.RepoConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateRepo(repo); err != nil {
		return model.RepoConfig{}, err
	}
	now := time.Now()
	if repo.ID == "" {
		repo.ID = newID("repo")
		repo.CreatedAt = now
	}
	repo.UpdatedAt = now
	if repo.CommitMessage == "" {
		repo.CommitMessage = defaultCommitFormat
	}

	index := slices.IndexFunc(s.appConfig.Repositories, func(item model.RepoConfig) bool { return item.ID == repo.ID })
	if index >= 0 {
		repo.CreatedAt = s.appConfig.Repositories[index].CreatedAt
		repo.LastSyncAt = s.appConfig.Repositories[index].LastSyncAt
		repo.LastStatus = s.appConfig.Repositories[index].LastStatus
		repo.LastError = s.appConfig.Repositories[index].LastError
		repo.LastPushAt = s.appConfig.Repositories[index].LastPushAt
		repo.LastPushStatus = s.appConfig.Repositories[index].LastPushStatus
		repo.LastPullAt = s.appConfig.Repositories[index].LastPullAt
		repo.LastPullStatus = s.appConfig.Repositories[index].LastPullStatus
		repo.LastRevision = s.appConfig.Repositories[index].LastRevision
		s.appConfig.Repositories[index] = repo
	} else {
		s.appConfig.Repositories = append(s.appConfig.Repositories, repo)
	}

	if err := s.persistLocked(); err != nil {
		return model.RepoConfig{}, err
	}
	return repo, nil
}

func (s *Store) DeleteRepository(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	index := slices.IndexFunc(s.appConfig.Repositories, func(item model.RepoConfig) bool { return item.ID == id })
	if index < 0 {
		return errors.New("repository not found")
	}
	s.appConfig.Repositories = append(s.appConfig.Repositories[:index], s.appConfig.Repositories[index+1:]...)
	return s.persistLocked()
}

func (s *Store) UpdateRepositorySyncState(id string, result model.SyncResult, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := slices.IndexFunc(s.appConfig.Repositories, func(item model.RepoConfig) bool { return item.ID == id })
	if index < 0 {
		return errors.New("repository not found")
	}
	s.appConfig.Repositories[index].LastStatus = result.Status
	s.appConfig.Repositories[index].LastError = lastError
	finishedAt := result.FinishedAt
	s.appConfig.Repositories[index].LastSyncAt = &finishedAt
	if result.PullAttempted {
		s.appConfig.Repositories[index].LastPullAt = result.PullFinishedAt
		if result.PullSucceeded {
			s.appConfig.Repositories[index].LastPullStatus = "success"
		} else {
			s.appConfig.Repositories[index].LastPullStatus = "error"
		}
	}
	if result.PushAttempted {
		s.appConfig.Repositories[index].LastPushAt = result.PushFinishedAt
		if result.PushSucceeded {
			s.appConfig.Repositories[index].LastPushStatus = "success"
		} else {
			s.appConfig.Repositories[index].LastPushStatus = "error"
		}
	}
	if strings.TrimSpace(result.RepositoryRevision) != "" {
		s.appConfig.Repositories[index].LastRevision = strings.TrimSpace(result.RepositoryRevision)
	}
	s.appConfig.Repositories[index].UpdatedAt = time.Now()
	return s.persistLocked()
}

func (s *Store) ListCredentials() []model.Credential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.credentials))
	for id := range s.credentials {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]model.Credential, 0, len(ids))
	for _, id := range ids {
		out = append(out, maskCredential(s.credentials[id]))
	}
	return out
}

func (s *Store) SaveCredential(cred model.Credential) (model.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cred.Type == model.CredentialTypeSSH && cred.ID == "" && strings.TrimSpace(cred.PrivateKey) == "" {
		privateKey, publicKey, err := auth.GenerateSSHKeyPair(cred.Name)
		if err != nil {
			return model.Credential{}, err
		}
		cred.PrivateKey = privateKey
		cred.PublicKey = publicKey
	}

	if err := validateCredential(cred); err != nil {
		return model.Credential{}, err
	}
	now := time.Now()
	if cred.ID == "" {
		cred.ID = newID("cred")
		cred.CreatedAt = now
	}
	cred.UpdatedAt = now

	if cred.Password != "" {
		encrypted, err := auth.Encrypt(s.masterKey, cred.Password)
		if err != nil {
			return model.Credential{}, err
		}
		cred.Password = encrypted
	}
	if cred.PrivateKey != "" {
		encrypted, err := auth.Encrypt(s.masterKey, cred.PrivateKey)
		if err != nil {
			return model.Credential{}, err
		}
		cred.PrivateKey = encrypted
	}

	if existing, ok := s.credentials[cred.ID]; ok {
		cred.CreatedAt = existing.CreatedAt
		if cred.Password == "" {
			cred.Password = existing.Password
		}
		if cred.PrivateKey == "" {
			cred.PrivateKey = existing.PrivateKey
		}
		if cred.PublicKey == "" {
			cred.PublicKey = existing.PublicKey
		}
	}
	s.credentials[cred.ID] = cred

	if err := s.persistLocked(); err != nil {
		return model.Credential{}, err
	}
	return maskCredential(cred), nil
}

func (s *Store) DeleteCredential(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.credentials[id]; !ok {
		return errors.New("credential not found")
	}
	delete(s.credentials, id)
	return s.persistLocked()
}

func (s *Store) GetCredential(id string) (model.Credential, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cred, ok := s.credentials[id]
	if !ok {
		return model.Credential{}, false, nil
	}
	plain, err := decryptCredential(s.masterKey, cred)
	if err != nil {
		return model.Credential{}, false, err
	}
	return plain, true, nil
}

func (s *Store) ResolveCredential(repo model.RepoConfig) (model.Credential, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if repo.CredentialID != "" {
		cred, ok := s.credentials[repo.CredentialID]
		if !ok {
			return model.Credential{}, false, errors.New("credential not found")
		}
		plain, err := decryptCredential(s.masterKey, cred)
		if err != nil {
			return model.Credential{}, false, err
		}
		return plain, true, nil
	}

	bestID := ""
	bestScore := -1
	for id, cred := range s.credentials {
		score := credentialMatchScore(cred, repo.RemoteURL)
		if score > bestScore {
			bestID = id
			bestScore = score
		}
	}
	if bestID == "" {
		return model.Credential{}, false, nil
	}
	plain, err := decryptCredential(s.masterKey, s.credentials[bestID])
	if err != nil {
		return model.Credential{}, false, err
	}
	return plain, true, nil
}

func (s *Store) load() error {
	if data, err := os.ReadFile(s.configPath); err == nil {
		if err := json.Unmarshal(data, &s.appConfig); err != nil {
			return fmt.Errorf("parse config.json: %w", err)
		}
	}

	if data, err := os.ReadFile(s.credPath); err == nil {
		var creds model.CredentialStore
		if err := json.Unmarshal(data, &creds); err != nil {
			return fmt.Errorf("parse credentials.json: %w", err)
		}
		for _, cred := range creds.Credentials {
			s.credentials[cred.ID] = cred
		}
	}
	return nil
}

func (s *Store) persistLocked() error {
	configData, err := json.MarshalIndent(s.appConfig, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.configPath, configData, 0o600); err != nil {
		return err
	}

	creds := model.CredentialStore{Credentials: make([]model.Credential, 0, len(s.credentials))}
	for _, cred := range s.credentials {
		creds.Credentials = append(creds.Credentials, cred)
	}
	slices.SortFunc(creds.Credentials, func(a, b model.Credential) int {
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return 1
		}
		return 0
	})

	credData, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.credPath, credData, 0o600)
}

func decryptCredential(key []byte, cred model.Credential) (model.Credential, error) {
	var err error
	if cred.Password != "" {
		cred.Password, err = auth.Decrypt(key, cred.Password)
		if err != nil {
			return model.Credential{}, fmt.Errorf("decrypt password: %w", err)
		}
	}
	if cred.PrivateKey != "" {
		cred.PrivateKey, err = auth.Decrypt(key, cred.PrivateKey)
		if err != nil {
			return model.Credential{}, fmt.Errorf("decrypt ssh key: %w", err)
		}
	}
	return cred, nil
}

func validateRepo(repo model.RepoConfig) error {
	if repo.Name == "" {
		return errors.New("repository name is required")
	}
	if repo.RemoteURL == "" {
		return errors.New("remote URL is required")
	}
	if repo.Branch == "" {
		repo.Branch = "main"
	}
	if repo.SyncIntervalSec < minSyncIntervalSec {
		return fmt.Errorf("sync interval must be at least %d seconds", minSyncIntervalSec)
	}
	return nil
}

func validateCredential(cred model.Credential) error {
	if cred.Name == "" {
		return errors.New("credential name is required")
	}
	switch cred.Type {
	case model.CredentialTypeSSH:
		if strings.TrimSpace(cred.PrivateKey) == "" {
			return errors.New("ssh private key is required")
		}
		if strings.TrimSpace(cred.PublicKey) == "" {
			return errors.New("ssh public key is required")
		}
	case model.CredentialTypeHTTP:
		if strings.TrimSpace(cred.MatchURL) == "" {
			return errors.New("match URL is required for http credentials")
		}
		if cred.Username == "" {
			return errors.New("username is required")
		}
		if cred.Password == "" && cred.ID == "" {
			return errors.New("password or token is required")
		}
	default:
		return errors.New("unsupported credential type")
	}
	return nil
}

func maskCredential(cred model.Credential) model.Credential {
	cred.Password = ""
	cred.PrivateKey = ""
	switch cred.Type {
	case model.CredentialTypeSSH:
		fingerprint := auth.FingerprintSSHPublicKey(cred.PublicKey)
		if fingerprint == "" {
			cred.MaskedSecretHint = "SSH key pair ready"
		} else {
			cred.MaskedSecretHint = fingerprint
		}
	case model.CredentialTypeHTTP:
		if cred.Username != "" {
			cred.MaskedSecretHint = cred.MatchURL + " -> " + cred.Username + " / ******"
		}
	}
	return cred
}

func credentialMatchScore(cred model.Credential, remoteURL string) int {
	if cred.Type != model.CredentialTypeHTTP || strings.TrimSpace(cred.MatchURL) == "" || strings.TrimSpace(remoteURL) == "" {
		return -1
	}

	matchURL := normalizeMatchURL(cred.MatchURL)
	remote := normalizeMatchURL(remoteURL)
	if strings.HasPrefix(remote, matchURL) {
		return len(matchURL)
	}

	remoteHostPath := normalizeHostPath(remoteURL)
	if remoteHostPath != "" && strings.HasPrefix(remoteHostPath, matchURL) {
		return len(matchURL)
	}

	remoteHost := normalizeHost(remoteURL)
	if remoteHost != "" && remoteHost == matchURL {
		return len(matchURL)
	}
	return -1
}

func normalizeMatchURL(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "/")
	return value
}

func normalizeHostPath(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		path := parsed.Path
		if path == "" {
			path = parsed.EscapedPath()
		}
		return strings.TrimSuffix(strings.ToLower(parsed.Host+path), "/")
	}
	return ""
}

func normalizeHost(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		return strings.ToLower(parsed.Host)
	}
	return ""
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
