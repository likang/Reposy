package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type SyncEngine struct {
	repositories []*Repository
	syncTicker   *time.Ticker
	stopChan     chan struct{}
	syncing      bool
}

type SyncStatus struct {
	LastSync   time.Time
	InProgress bool
	Error      string
}

func NewSyncEngine() (*SyncEngine, error) {
	engine := SyncEngine{}
	err := engine.UpdateConfig()
	if err != nil {
		return nil, err
	}

	return &engine, nil
}

func (s *SyncEngine) Start() {
	// Initial sync for all repositories
	s.SyncAll()

	// Start periodic sync
	go func() {
		for {
			select {
			case <-s.syncTicker.C:
				s.SyncAll()
			case <-s.stopChan:
				s.syncTicker.Stop()
				return
			}
		}
	}()
}

func (s *SyncEngine) IsSyncing() bool {
	return s.syncing
}

func (s *SyncEngine) SyncAll() {
	if s.syncing {
		log.Println("Sync already in progress")
		return
	}
	s.syncing = true
	defer func() {
		s.syncing = false
	}()

	for _, repository := range s.repositories {
		repository.Sync()
	}
}

func (s *SyncEngine) Stop() {
	if s.syncTicker != nil {
		s.syncTicker.Stop()
	}
	if s.stopChan != nil {
		close(s.stopChan)
	}
}

func (s *SyncEngine) UpdateConfig() error {
	s.Stop()
	config, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	repositories := make([]*Repository, 0, len(config.Repositories))
	for localPath, repoConfig := range config.Repositories {
		repo := NewRepository(localPath, config, repoConfig)
		repositories = append(repositories, repo)
	}
	s.repositories = repositories
	// s.syncTicker = time.NewTicker(10 * time.Minute)
	s.syncTicker = time.NewTicker(10 * time.Second)
	s.stopChan = make(chan struct{})

	s.Start()
	return nil
}

func (s *SyncEngine) GetStatus() string {
	var sb strings.Builder

	if len(s.repositories) == 0 {
		return "No repositories configured"
	}

	for _, repository := range s.repositories {
		status := &repository.Status
		sb.WriteString(fmt.Sprintf("Repository: %s\n", repository.Path))

		if status.LastSync.IsZero() {
			sb.WriteString("  Never synced\n")
		} else {
			sb.WriteString(fmt.Sprintf("  Last sync: %s\n", status.LastSync.Format(time.RFC3339)))
		}

		if status.InProgress {
			sb.WriteString("  Status: In progress\n")
		} else if status.Error != "" {
			sb.WriteString(fmt.Sprintf("  Status: Error - %s\n", status.Error))
		} else {
			sb.WriteString("  Status: Idle\n")
		}

		sb.WriteString("\n")
	}

	return sb.String()
}
