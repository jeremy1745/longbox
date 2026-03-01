package service

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupInfo describes a database backup file.
type BackupInfo struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// BackupService manages SQLite database backups.
type BackupService struct {
	dbPath    string
	backupDir string
}

// NewBackupService creates a new backup service.
func NewBackupService(dbPath, dataDir string) *BackupService {
	return &BackupService{
		dbPath:    dbPath,
		backupDir: filepath.Join(dataDir, "backups"),
	}
}

// BackupDir returns the backup directory path.
func (s *BackupService) BackupDir() string {
	return s.backupDir
}

// CreateBackup copies the database file to the backups directory.
func (s *BackupService) CreateBackup() (*BackupInfo, error) {
	if err := os.MkdirAll(s.backupDir, 0700); err != nil {
		return nil, fmt.Errorf("creating backup directory: %w", err)
	}

	timestamp := time.Now().UTC().Format("20060102-150405")
	backupName := fmt.Sprintf("longbox-%s.db", timestamp)
	backupPath := filepath.Join(s.backupDir, backupName)

	src, err := os.Open(s.dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return nil, fmt.Errorf("creating backup file: %w", err)
	}
	defer dst.Close()

	size, err := io.Copy(dst, src)
	if err != nil {
		os.Remove(backupPath)
		return nil, fmt.Errorf("copying database: %w", err)
	}

	if err := dst.Close(); err != nil {
		os.Remove(backupPath)
		return nil, fmt.Errorf("closing backup: %w", err)
	}

	slog.Info("database backup created", "name", backupName, "size", size)

	return &BackupInfo{
		Name:      backupName,
		Size:      size,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// ListBackups returns all backup files sorted by name descending (newest first).
func (s *BackupService) ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading backup directory: %w", err)
	}

	var backups []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "longbox-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, BackupInfo{
			Name:      e.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC(),
		})
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Name > backups[j].Name
	})

	return backups, nil
}

// DeleteBackup removes a backup file. Validates the name to prevent path traversal.
func (s *BackupService) DeleteBackup(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid backup name")
	}
	if !strings.HasPrefix(name, "longbox-") || !strings.HasSuffix(name, ".db") {
		return fmt.Errorf("invalid backup name format")
	}

	path := filepath.Join(s.backupDir, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup not found: %s", name)
		}
		return fmt.Errorf("deleting backup: %w", err)
	}

	slog.Info("database backup deleted", "name", name)
	return nil
}

// BackupFilePath returns the full path for a backup, with path traversal validation.
func (s *BackupService) BackupFilePath(name string) (string, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid backup name")
	}
	if !strings.HasPrefix(name, "longbox-") || !strings.HasSuffix(name, ".db") {
		return "", fmt.Errorf("invalid backup name format")
	}

	path := filepath.Join(s.backupDir, name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("backup not found: %s", name)
	}
	return path, nil
}

// PruneBackups keeps only the most recent `retention` backups.
func (s *BackupService) PruneBackups(retention int) (int, error) {
	if retention <= 0 {
		return 0, nil
	}

	backups, err := s.ListBackups()
	if err != nil {
		return 0, err
	}

	if len(backups) <= retention {
		return 0, nil
	}

	pruned := 0
	for _, b := range backups[retention:] {
		if err := s.DeleteBackup(b.Name); err != nil {
			slog.Warn("failed to prune backup", "name", b.Name, "error", err)
			continue
		}
		pruned++
	}

	return pruned, nil
}

// RunStartupBackup creates a backup on startup if the setting is enabled.
func (s *BackupService) RunStartupBackup(enabled bool, retention int) {
	if !enabled {
		return
	}

	backup, err := s.CreateBackup()
	if err != nil {
		slog.Warn("startup backup failed", "error", err)
		return
	}
	slog.Info("startup backup created", "name", backup.Name)

	if retention > 0 {
		pruned, err := s.PruneBackups(retention)
		if err != nil {
			slog.Warn("backup pruning failed", "error", err)
		} else if pruned > 0 {
			slog.Info("pruned old backups", "count", pruned)
		}
	}
}
