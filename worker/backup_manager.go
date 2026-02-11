package worker

import (
	"fmt"
	"io"
	"log-server/config"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	yzip "github.com/yeka/zip"
)

type BackupManager struct {
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewBackupManager() *BackupManager {
	return &BackupManager{
		stopChan: make(chan struct{}),
	}
}

func (bm *BackupManager) Start() {
	cfg := config.Get()
	if !cfg.KettasLog.Backup.Enabled {
		slog.Info("Backup manager is disabled")
		return
	}

	bm.wg.Add(1)
	go func() {
		defer bm.wg.Done()
		slog.Info("Backup manager started", "interval_min", cfg.KettasLog.Backup.CheckIntervalMin)

		ticker := time.NewTicker(time.Duration(cfg.KettasLog.Backup.CheckIntervalMin) * time.Minute)
		defer ticker.Stop()

		// İlk başlangıçta bir kez çalıştır
		bm.checkAndRotate()
		bm.cleanupBackups()

		for {
			select {
			case <-ticker.C:
				bm.checkAndRotate()
				bm.cleanupBackups()
			case <-bm.stopChan:
				slog.Info("Backup manager stopping")
				return
			}
		}
	}()
}

func (bm *BackupManager) Stop() {
	close(bm.stopChan)
	bm.wg.Wait()
}

// checkAndRotate monitors logs folder size and rotates if needed
func (bm *BackupManager) checkAndRotate() {
	cfg := config.Get()
	logsDir := cfg.KettasLog.LogsDir

	// Klasör boyutunu hesapla
	size, err := getDirSize(logsDir)
	if err != nil {
		slog.Error("Failed to calculate logs dir size", "error", err)
		return
	}

	maxSizeBytes := cfg.KettasLog.Backup.MaxFolderSizeMB * 1024 * 1024
	slog.Debug("Checking logs dir size", "current_bytes", size, "max_bytes", maxSizeBytes)

	if size > maxSizeBytes {
		slog.Info("Logs dir size exceeded limit, starting rotation", "current_size_mb", size/1024/1024)
		if err := bm.rotateLogs(logsDir, cfg.KettasLog.Backup.BackupDir, cfg.KettasLog.ZipPassword); err != nil {
			slog.Error("Failed to rotate logs", "error", err)
		}
	}
}

func (bm *BackupManager) rotateLogs(srcDir, destDir, password string) error {
	// Backup klasörünü oluştur
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("backup dir creation failed: %w", err)
	}

	timestamp := time.Now().Format("02_01_2006_15_04_05")
	zipName := fmt.Sprintf("kettas_logs_%s.zip", timestamp)
	zipPath := filepath.Join(destDir, zipName)

	// 1. Klasörü zip'le (Şifreli)
	if err := zipDirectory(srcDir, zipPath, password); err != nil {
		return fmt.Errorf("zipping failed: %w", err)
	}
	slog.Info("Logs rotated and zipped", "path", zipPath)

	// 2. Orijinal dosyaları sil
	if err := removeDirContents(srcDir); err != nil {
		return fmt.Errorf("cleaning logs dir failed: %w", err)
	}
	slog.Info("Logs directory cleaned")

	return nil
}

// cleanupBackups enforces retention policies on backup folder
func (bm *BackupManager) cleanupBackups() {
	cfg := config.Get()
	backupDir := cfg.KettasLog.Backup.BackupDir

	files, err := os.ReadDir(backupDir)
	if err != nil {
		// Klasör yoksa sorun değil
		if os.IsNotExist(err) {
			return
		}
		slog.Error("Failed to read backup dir", "error", err)
		return
	}

	var backupFiles []os.FileInfo
	var totalSize int64

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".zip") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		backupFiles = append(backupFiles, info)
		totalSize += info.Size()

		// 1. Time Retention Check
		age := time.Since(info.ModTime())
		if age.Hours() > float64(cfg.KettasLog.Backup.RetentionDays*24) {
			slog.Info("Deleting old backup due to retention time", "file", f.Name(), "age_days", int(age.Hours()/24))
			os.Remove(filepath.Join(backupDir, f.Name()))
			totalSize -= info.Size()
		}
	}

	// 2. Size Retention Check
	maxSizeBytes := cfg.KettasLog.Backup.MaxBackupSizeMB * 1024 * 1024
	if totalSize > maxSizeBytes {
		slog.Info("Backup dir size exceeded limit, cleaning old backups", "current_mb", totalSize/1024/1024, "max_mb", cfg.KettasLog.Backup.MaxBackupSizeMB)

		// Dosyaları eskiden yeniye sırala
		sort.Slice(backupFiles, func(i, j int) bool {
			return backupFiles[i].ModTime().Before(backupFiles[j].ModTime())
		})

		for _, f := range backupFiles {
			// Silinmiş olabilir (time check'te), kontrol et
			path := filepath.Join(backupDir, f.Name())
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}

			if totalSize <= maxSizeBytes {
				break
			}

			slog.Info("Deleting backup to free space", "file", f.Name())
			if err := os.Remove(path); err == nil {
				totalSize -= f.Size()
			}
		}
	}
}

// Helpers

func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func zipDirectory(source, target, password string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := yzip.NewWriter(zipfile)
	defer archive.Close()

	info, err := os.Stat(source)
	if err != nil {
		return nil
	}

	var baseDir string
	if info.IsDir() {
		baseDir = filepath.Base(source)
	}

	filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := yzip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		if baseDir != "" {
			header.Name = filepath.Join(baseDir, strings.TrimPrefix(path, source))
		}

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = yzip.Deflate
		}

		var writer io.Writer
		if password != "" {
			header.SetPassword(password)
			writer, err = archive.Encrypt(header.Name, password, yzip.AES256Encryption)
			if err != nil {
				return err
			}
		} else {
			writer, err = archive.CreateHeader(header)
			if err != nil {
				return err
			}
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

func removeDirContents(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		err = os.RemoveAll(filepath.Join(dir, name))
		if err != nil {
			return err
		}
	}
	return nil
}
