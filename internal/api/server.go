package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gtimonitor/internal/gitops"
	"gtimonitor/internal/model"
	"gtimonitor/internal/scheduler"
)

type Store interface {
	ConfigDir() string
	ListRepositories() []model.RepoConfig
	GetRepository(id string) (model.RepoConfig, bool)
	SaveRepository(repo model.RepoConfig) (model.RepoConfig, error)
	DeleteRepository(id string) error
	ListCredentials() []model.Credential
	SaveCredential(cred model.Credential) (model.Credential, error)
	DeleteCredential(id string) error
	GetCredential(id string) (model.Credential, bool, error)
	ResolveCredential(repo model.RepoConfig) (model.Credential, bool, error)
}

type Server struct {
	store      Store
	manager    *gitops.Manager
	scheduler  *scheduler.Service
	staticRoot string
}

type scanResponse struct {
	Imported int                     `json:"imported"`
	Skipped  int                     `json:"skipped"`
	Items    []model.ScannedRepository `json:"items,omitempty"`
}

type repoPayload struct {
	Name              string `json:"name"`
	RemoteURL         string `json:"remoteUrl"`
	Branch            string `json:"branch"`
	SyncIntervalSec   int    `json:"syncIntervalSec"`
	CredentialID      string `json:"credentialId"`
	AutoCommitEnabled bool   `json:"autoCommitEnabled"`
	AutoPullEnabled   bool   `json:"autoPullEnabled"`
	Enabled           bool   `json:"enabled"`
}

type testResponse struct {
	State   string `json:"state,omitempty"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type credentialPayload struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	MatchURL   string `json:"matchUrl"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

func NewServer(store Store, manager *gitops.Manager, syncService *scheduler.Service, staticRoot string) *Server {
	return &Server{store: store, manager: manager, scheduler: syncService, staticRoot: staticRoot}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/credentials", s.handleCredentials)
	mux.HandleFunc("/api/credentials/", s.handleCredentialByID)
	mux.HandleFunc("/api/repositories", s.handleRepositories)
	mux.HandleFunc("/api/repositories/", s.handleRepositoryByID)
	mux.HandleFunc("/api/repositories/scan", s.handleScanRepositories)
	mux.HandleFunc("/api/repositories/test-pull", s.handleTestPull)
	mux.HandleFunc("/api/repositories/test-commit", s.handleTestCommit)
	mux.Handle("/", s.handleStatic())
	return withCORS(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now()})
}

func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListCredentials())
	case http.MethodPost:
		var payload credentialPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cred, err := s.store.SaveCredential(model.Credential{
			Name:       payload.Name,
			Type:       model.CredentialType(payload.Type),
			MatchURL:   payload.MatchURL,
			Username:   payload.Username,
			Password:   payload.Password,
			PrivateKey: payload.PrivateKey,
			PublicKey:  payload.PublicKey,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, cred)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCredentialByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/credentials/"), "/")
	if id == "" {
		writeError(w, http.StatusNotFound, errors.New("credential not found"))
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	for _, repo := range s.store.ListRepositories() {
		if repo.CredentialID == id {
			writeError(w, http.StatusBadRequest, fmt.Errorf("credential is still used by repository %s", repo.Name))
			return
		}
	}

	if err := s.store.DeleteCredential(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRepositories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.scheduler.GetStatuses())
	case http.MethodPost:
		var payload repoPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		repo := model.RepoConfig{
			Name:              payload.Name,
			RemoteURL:         payload.RemoteURL,
			Branch:            defaultString(payload.Branch, "main"),
			LocalPath:         s.manager.ResolvePath(slugify(payload.Name)),
			SyncIntervalSec:   payload.SyncIntervalSec,
			CredentialID:      payload.CredentialID,
			AutoCommitEnabled: payload.AutoCommitEnabled,
			AutoPullEnabled:   payload.AutoPullEnabled,
			Enabled:           payload.Enabled,
		}

		cred, err := s.resolveRepositoryCredential(repo)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.manager.Clone(r.Context(), repo, cred); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		saved, err := s.store.SaveRepository(repo)
		if err != nil {
			_ = os.RemoveAll(repo.LocalPath)
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.scheduler.Upsert(r.Context(), saved); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleScanRepositories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	scanned, err := s.manager.ScanLocalRepos(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	existingByPath := map[string]model.RepoConfig{}
	for _, repo := range s.store.ListRepositories() {
		existingByPath[repo.LocalPath] = repo
	}

	response := scanResponse{
		Items: make([]model.ScannedRepository, 0, len(scanned)),
	}

	for _, item := range scanned {
		response.Items = append(response.Items, model.ScannedRepository{
			Name:      item.Name,
			RemoteURL: item.RemoteURL,
			Branch:    item.Branch,
			LocalPath: item.LocalPath,
			Revision:  item.Revision,
		})

		if _, ok := existingByPath[item.LocalPath]; ok {
			response.Skipped++
			continue
		}

		saved, err := s.store.SaveRepository(model.RepoConfig{
			Name:              item.Name,
			RemoteURL:         item.RemoteURL,
			Branch:            defaultString(item.Branch, "main"),
			LocalPath:         item.LocalPath,
			SyncIntervalSec:   300,
			AutoCommitEnabled: true,
			AutoPullEnabled:   true,
			Enabled:           true,
			LastRevision:      item.Revision,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.scheduler.Upsert(r.Context(), saved); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		response.Imported++
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleTestPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	repo, cred, err := s.buildRepoFromPayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.manager.TestPull(r.Context(), repo, cred); err != nil {
		writeJSON(w, http.StatusOK, testResponse{State: "error", OK: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testResponse{State: "success", OK: true, Message: "远端访问正常"})
}

func (s *Server) handleTestCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	repo, cred, err := s.buildRepoFromPayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.manager.TestCommit(r.Context(), repo, cred)
	if err != nil {
		writeJSON(w, http.StatusOK, testResponse{State: "error", OK: false, Message: err.Error()})
		return
	}
	state := "success"
	if !result.Ready {
		state = "warning"
	}
	writeJSON(w, http.StatusOK, testResponse{State: state, OK: result.Ready, Message: result.Message})
}

func (s *Server) handleRepositoryByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/repositories/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("repository not found"))
		return
	}
	id := parts[0]

	if len(parts) == 2 && parts[1] == "sync" && r.Method == http.MethodPost {
		result, err := s.scheduler.Trigger(context.Background(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}

	switch r.Method {
	case http.MethodPut:
		current, ok := s.store.GetRepository(id)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("repository not found"))
			return
		}
		var payload repoPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		current.Name = payload.Name
		current.RemoteURL = payload.RemoteURL
		current.Branch = defaultString(payload.Branch, "main")
		current.SyncIntervalSec = payload.SyncIntervalSec
		current.CredentialID = payload.CredentialID
		current.AutoCommitEnabled = payload.AutoCommitEnabled
		current.AutoPullEnabled = payload.AutoPullEnabled
		current.Enabled = payload.Enabled

		if _, err := s.resolveRepositoryCredential(current); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		saved, err := s.store.SaveRepository(current)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.scheduler.Upsert(r.Context(), saved); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		repo, ok := s.store.GetRepository(id)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("repository not found"))
			return
		}
		s.scheduler.Remove(id)
		if err := s.store.DeleteRepository(id); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = os.RemoveAll(repo.LocalPath)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleStatic() http.Handler {
	indexPath := filepath.Join(s.staticRoot, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		return http.FileServer(http.Dir(s.staticRoot))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "frontend assets not found, build web app first",
		})
	})
}

func (s *Server) resolveCredential(id string) (*model.Credential, error) {
	if id == "" {
		return nil, nil
	}
	cred, ok, err := s.store.GetCredential(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("credential not found")
	}
	return &cred, nil
}

func (s *Server) resolveRepositoryCredential(repo model.RepoConfig) (*model.Credential, error) {
	if repo.CredentialID != "" {
		return s.resolveCredential(repo.CredentialID)
	}
	cred, ok, err := s.store.ResolveCredential(repo)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return &cred, nil
}

func (s *Server) buildRepoFromPayload(r *http.Request) (model.RepoConfig, *model.Credential, error) {
	var payload repoPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return model.RepoConfig{}, nil, err
	}
	repo := model.RepoConfig{
		Name:              payload.Name,
		RemoteURL:         payload.RemoteURL,
		Branch:            defaultString(payload.Branch, "main"),
		LocalPath:         s.manager.ResolvePath(slugify(payload.Name)),
		SyncIntervalSec:   payload.SyncIntervalSec,
		CredentialID:      payload.CredentialID,
		AutoCommitEnabled: payload.AutoCommitEnabled,
		AutoPullEnabled:   payload.AutoPullEnabled,
		Enabled:           payload.Enabled,
	}
	cred, err := s.resolveRepositoryCredential(repo)
	if err != nil {
		return model.RepoConfig{}, nil, err
	}
	return repo, cred, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return -1
		}
	}, value)
	if value == "" {
		return "repo"
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
