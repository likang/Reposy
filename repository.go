package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Repository struct {
	Path           string
	Status         SyncStatus
	Client         Client
	LastLocalFiles map[string]*FileItem
}

type FileItem struct {
	FilePath  string
	ModTime   int64
	Tombstone bool
}

type RemoteItem struct {
	SlashPath string `json:"-"`
	ModTime   int64  `json:"mod_time"`
	Tombstone bool   `json:"tombstone"`
}

type Client interface {
	List() (map[string]*RemoteItem, error)
	Put(data []byte, modTime time.Time, slashPath string) error
	Get(slashPath string) ([]byte, error)
	Delete(slashPath string) error
	MarkTombstone(slashPath string) error
	Finish(remoteFiles map[string]*RemoteItem, changed bool) error
}

func NewRepository(repoPath string, config *Config, repoConfig *RepositoryConfig) *Repository {
	client := NewClient(config, repoConfig)
	return &Repository{
		Path:   repoPath,
		Client: client,
	}
}

func NewClient(config *Config, repoConfig *RepositoryConfig) Client {
	switch repoConfig.Type {
	case "s3":
		return NewS3Client(config, repoConfig)
	default:
		log.Fatal("Unsupported remote type: " + repoConfig.Type)
		return nil
	}
}

func (repo *Repository) Sync() {
	// Mark as in progress
	status := &repo.Status
	status.InProgress = true
	status.Error = ""

	defer func() {
		status.InProgress = false
		status.LastSync = time.Now()
	}()

	// Get local files
	localFiles, err := repo.GetLocalFiles()
	if err != nil {
		status.Error = fmt.Sprintf("Failed to get local files: %v", err)
		return
	}

	// Check removed files since last sync
	if repo.LastLocalFiles != nil {
		for slashPath, item := range repo.LastLocalFiles {
			if _, found := localFiles[slashPath]; !found {
				localFiles[slashPath] = &FileItem{
					FilePath:  item.FilePath,
					ModTime:   time.Now().Unix(),
					Tombstone: true,
				}
			}
		}
	}

	// Get remote files
	remoteFiles, err := repo.GetRemoteFiles()
	if err != nil {
		status.Error = fmt.Sprintf("Failed to get remote files: %v", err)
		return
	}

	// Compare and sync files
	err = repo.compareAndSync(localFiles, remoteFiles)
	if err != nil {
		status.Error = fmt.Sprintf("Failed to sync files: %v", err)
		return
	}

	repo.LastLocalFiles = localFiles
}

func (repo *Repository) GetLocalFiles() (map[string]*FileItem, error) {
	repoPath := repo.Path
	result := make(map[string]*FileItem)

	// Run git ls-files command to get tracked and untracked (but not ignored) files
	cmd := exec.Command("git", "-C", repoPath, "ls-files", "--others", "--exclude-standard", "--cached")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files command failed: %w", err)
	}

	gitLsFiles := strings.Split(string(output), "\n") // slash path
	filePaths := make([]string, 0, len(gitLsFiles))
	for _, filePath := range gitLsFiles {
		if filePath == "" {
			continue
		}
		if filePath[0] == '"' {
			filePath, err = strconv.Unquote(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to unquote file path: %s", filePath)
			}
		}
		filePath = filepath.FromSlash(filePath)
		filePaths = append(filePaths, filePath)
	}

	// Walk through .git directory and collect file paths
	gitPath := filepath.Join(repoPath, ".git")
	err = filepath.Walk(gitPath, func(fullFilePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		filePath, err := filepath.Rel(repoPath, fullFilePath)
		if err != nil {
			return err
		}

		filePaths = append(filePaths, filePath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk .git directory: %w", err)
	}

	for _, filePath := range filePaths {
		if filePath == "" {
			continue
		}

		fullFilePath := filepath.Join(repoPath, filePath)
		info, err := os.Stat(fullFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				// maybe user remove file directly, not using git
				continue
			}
			return nil, fmt.Errorf("failed to stat file %s: %w", fullFilePath, err)
		}

		if !info.IsDir() {
			slashPath := filepath.ToSlash(filePath)
			result[slashPath] = &FileItem{
				FilePath:  filePath,
				ModTime:   info.ModTime().Unix(),
				Tombstone: false,
			}
		}
	}

	return result, nil
}

func (repo *Repository) GetRemoteFiles() (map[string]*RemoteItem, error) {
	return repo.Client.List()
}

func (repo *Repository) uploadFile(localFileItem *FileItem, slashPath string) error {
	localFilePath := filepath.Join(repo.Path, localFileItem.FilePath)
	fileInfo, err := os.Stat(localFilePath)
	if err != nil {
		return err
	}

	if fileInfo.IsDir() {
		log.Fatal("can not upload directory: " + localFilePath)
	}

	data, err := os.ReadFile(localFilePath)
	if err != nil {
		return err
	}

	return repo.Client.Put(data, fileInfo.ModTime(), slashPath)
}

func ensureWritableIfExist(path string) (exist bool, err error) {
	// Check if the file already exists
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return true, err
	}

	// Check if the file is writable
	if fileInfo.Mode()&0200 == 0 {
		// Add write permission
		err = os.Chmod(path, fileInfo.Mode()|0200)
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

func (repo *Repository) compareAndSync(localItems map[string]*FileItem, remoteItems map[string]*RemoteItem) error {

	remoteChanged := false

	localNewerItems := make(map[string]*FileItem)
	remoteNewerItems := make(map[string]*RemoteItem)

	// Check for files only in local
	for slashPath, localItem := range localItems {
		_, exists := remoteItems[slashPath]
		if !exists {
			localNewerItems[slashPath] = localItem
		}
	}

	// Check for files only in remote
	for slashPath, remoteItem := range remoteItems {
		localItem, exists := localItems[slashPath]
		if !exists {
			remoteNewerItems[slashPath] = remoteItem
		} else {
			if localItem.ModTime > remoteItem.ModTime {
				localNewerItems[slashPath] = localItem
			} else if localItem.ModTime < remoteItem.ModTime {
				remoteNewerItems[slashPath] = remoteItem
			}
		}
	}

	for slashPath, localItem := range localNewerItems {
		if localItem.Tombstone {
			// mark remote file as tombstone
			err := repo.Client.MarkTombstone(slashPath)
			if err != nil {
				return fmt.Errorf("failed to mark remote file as tombstone: %w", err)
			}
			remoteItems[slashPath] = &RemoteItem{
				SlashPath: slashPath,
				ModTime:   time.Now().Unix(),
				Tombstone: true,
			}
			remoteChanged = true
		} else {
			// upload local file
			err := repo.uploadFile(localItem, slashPath)
			if err != nil {
				return fmt.Errorf("failed to upload file %s: %w", slashPath, err)
			}
			remoteItems[slashPath] = &RemoteItem{
				SlashPath: slashPath,
				ModTime:   localItem.ModTime,
				Tombstone: false,
			}
			remoteChanged = true
		}
	}

	for slashPath, remoteItem := range remoteNewerItems {

		filePath := filepath.FromSlash(slashPath)
		fullLocalPath := filepath.Join(repo.Path, filePath)
		if !remoteItem.Tombstone {
			// download remote file
			data, err := repo.Client.Get(slashPath)
			if err != nil {
				return fmt.Errorf("failed to download file %s: %w", slashPath, err)
			}

			// create parent dir if not exists
			parentDir := filepath.Dir(fullLocalPath)
			err = os.MkdirAll(parentDir, 0755)
			if err!= nil {
				return fmt.Errorf("failed to create parent dir %s: %w", parentDir, err)
			}

			_, err = ensureWritableIfExist(fullLocalPath)
			if err != nil {
				return fmt.Errorf("failed to ensure writable for file %s: %w", fullLocalPath, err)
			}

			err = os.WriteFile(fullLocalPath, data, 0644)
			if err != nil {
				return fmt.Errorf("failed to write file %s: %w", fullLocalPath, err)
			}
			// change modtime
			err = os.Chtimes(fullLocalPath, time.Now(), time.Unix(remoteItem.ModTime, 0))
			if err != nil {
				return fmt.Errorf("failed to change modtime of file %s: %w", fullLocalPath, err)
			}
		} else {
			// remove local file
			exists, err := ensureWritableIfExist(fullLocalPath)
			if err != nil {
				return fmt.Errorf("failed to ensure writable for file %s: %w", fullLocalPath, err)
			}
			if exists {
				err = os.Remove(fullLocalPath)
				if err != nil {
					return fmt.Errorf("failed to remove file %s: %w", fullLocalPath, err)
				}
			}
		}
	}

	// Remove outdated tombstone files in remote
	for slashPath, remoteItem := range remoteItems {
		if remoteItem.Tombstone {
			// Check if tombstone is older than 30 days
			if time.Now().Unix()-remoteItem.ModTime > 30*24*60*60 {
				err := repo.Client.Delete(slashPath)
				if err != nil {
					return fmt.Errorf("failed to delete tombstone file %s: %w", slashPath, err)
				}
				delete(remoteItems, slashPath)

				remoteChanged = true
			}
		}
	}
	err := repo.Client.Finish(remoteItems, remoteChanged)
	if err != nil {
		return fmt.Errorf("failed to finish sync: %w", err)
	}

	return nil
}
