package model

import "time"

type CredentialType string

const (
	CredentialTypeSSH  CredentialType = "ssh"
	CredentialTypeHTTP CredentialType = "http"
)

type Credential struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             CredentialType `json:"type"`
	MatchURL         string         `json:"matchUrl,omitempty"`
	Username         string         `json:"username,omitempty"`
	PrivateKey       string         `json:"privateKey,omitempty"`
	PublicKey        string         `json:"publicKey,omitempty"`
	Password         string         `json:"password,omitempty"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
	MaskedSecretHint string         `json:"maskedSecretHint,omitempty"`
}

type RepoConfig struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	RemoteURL         string    `json:"remoteUrl"`
	Branch            string    `json:"branch"`
	LocalPath         string    `json:"localPath"`
	SyncIntervalSec   int       `json:"syncIntervalSec"`
	CredentialID      string    `json:"credentialId,omitempty"`
	AutoCommitEnabled bool      `json:"autoCommitEnabled"`
	AutoPullEnabled   bool      `json:"autoPullEnabled"`
	CommitMessage     string    `json:"commitMessage"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	LastSyncAt        *time.Time `json:"lastSyncAt,omitempty"`
	LastError         string    `json:"lastError,omitempty"`
	LastStatus        string    `json:"lastStatus,omitempty"`
	LastPushAt        *time.Time `json:"lastPushAt,omitempty"`
	LastPushStatus    string    `json:"lastPushStatus,omitempty"`
	LastPushError     string    `json:"lastPushError,omitempty"`
	LastPullAt        *time.Time `json:"lastPullAt,omitempty"`
	LastPullStatus    string    `json:"lastPullStatus,omitempty"`
	LastPullError     string    `json:"lastPullError,omitempty"`
	LastRevision      string    `json:"lastRevision,omitempty"`
}

type AppConfig struct {
	Repositories []RepoConfig `json:"repositories"`
}

type CredentialStore struct {
	Credentials []Credential `json:"credentials"`
}

type SyncResult struct {
	StartedAt          time.Time `json:"startedAt"`
	FinishedAt         time.Time `json:"finishedAt"`
	Status             string    `json:"status"`
	Message            string    `json:"message"`
	RemoteUpdated      bool      `json:"remoteUpdated"`
	LocalCommitted     bool      `json:"localCommitted"`
	Pushed             bool      `json:"pushed"`
	ResolvedConflicts  bool      `json:"resolvedConflicts"`
	RepositoryRevision string    `json:"repositoryRevision"`
	PullAttempted      bool      `json:"pullAttempted"`
	PullSucceeded      bool      `json:"pullSucceeded"`
	PullFinishedAt     *time.Time `json:"pullFinishedAt,omitempty"`
	PullMessage        string    `json:"pullMessage,omitempty"`
	PushAttempted      bool      `json:"pushAttempted"`
	PushSucceeded      bool      `json:"pushSucceeded"`
	PushFinishedAt     *time.Time `json:"pushFinishedAt,omitempty"`
	PushMessage        string    `json:"pushMessage,omitempty"`
	Logs               []LogEntry `json:"logs,omitempty"`
}

type RepoStatus struct {
	Repo      RepoConfig  `json:"repo"`
	LastRun   *SyncResult `json:"lastRun,omitempty"`
	Running   bool        `json:"running"`
	NextRunAt *time.Time  `json:"nextRunAt,omitempty"`
	Logs      []LogEntry  `json:"logs,omitempty"`
}

type ScannedRepository struct {
	Name       string `json:"name"`
	RemoteURL  string `json:"remoteUrl"`
	Branch     string `json:"branch"`
	LocalPath  string `json:"localPath"`
	Revision   string `json:"revision,omitempty"`
}

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}
