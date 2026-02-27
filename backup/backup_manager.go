package backup

import (
	"log-server/config"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	slog.Info("Backup manager starting")
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

// checkAndRotate logs klasörü boyutunu kontrol eder, limit aşılmışsa
// her home_id için tüm JSON'ları birleştirip zipleyerek backups'a taşır.
func (bm *BackupManager) checkAndRotate() {
	slog.Info("checkAndRotate")
	cfg := config.Get()
	logsDir := cfg.KettasLog.LogsDir

	// Klasör boyutunu hesapla
	size, err := getDirSize(logsDir)
	if err != nil {
		slog.Error("Failed to calculate logs dir size", "error", err)
		return
	}

	maxSizeBytes := cfg.KettasLog.MaxFolderSizeMB * 1024 * 1024
	slog.Debug("Checking logs dir size", "current_bytes", size, "max_bytes", maxSizeBytes)

	if size > maxSizeBytes {
		slog.Info("Logs dir size exceeded limit, starting rotation",
			"current_size_mb", size/1024/1024,
			"max_size_mb", cfg.KettasLog.MaxFolderSizeMB,
		)
		bm.rotateLogsPerHomeId(logsDir, cfg.KettasLog.Backup.BackupDir, cfg.KettasLog.ZipPassword)
	}
}

// rotateLogsPerHomeId her home_id klasörü için ayrı ayrı:
// tüm JSON'ları birleştirip backups/{home_id}/full_DD_MM_YYYY.zip olarak zipleyip siler.
func (bm *BackupManager) rotateLogsPerHomeId(logsDir, backupDir, password string) {
	today := time.Now().Format("02_01_2006")

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		slog.Error("Logs dizini okunamadı", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		homeIdDir := entry.Name()
		homeIdPath := filepath.Join(logsDir, homeIdDir)

		// Bu home_id'nin tüm JSON dosyalarını bul
		allJSONFiles := findAllJSONFiles(homeIdPath)
		if len(allJSONFiles) == 0 {
			continue
		}

		slog.Info("Size rotation: home_id için dosyalar birleştiriliyor",
			"home_id_dir", homeIdDir,
			"file_count", len(allJSONFiles),
		)

		zipPath, deletedCount, err := mergeAndZipFiles(allJSONFiles, homeIdPath, backupDir, homeIdDir, today, password)
		if err != nil {
			slog.Error("Size rotation hatası", "home_id_dir", homeIdDir, "error", err)
			continue
		}

		if zipPath != "" {
			slog.Info("Size rotation zip oluşturuldu",
				"zip_path", zipPath,
				"home_id_dir", homeIdDir,
				"file_count", len(allJSONFiles),
				"deleted_count", deletedCount,
			)
		}
	}

	// Boş kalan home_id klasörlerini temizle
	cleanEmptyDirs(logsDir)

	slog.Info("Size rotation tamamlandı")
}

// cleanupBackups, yedekleme klasöründe saklama kurallarını uygular.
func (bm *BackupManager) cleanupBackups() {
	slog.Info("cleanupBackups")
	cfg := config.Get()
	backupDir := cfg.KettasLog.Backup.BackupDir

	// backups/ altındaki tüm zip dosyalarını recursive bul
	var backupFiles []backupFileInfo
	var totalSize int64

	err := filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".zip") {
			return nil
		}
		backupFiles = append(backupFiles, backupFileInfo{path: path, info: info})
		totalSize += info.Size()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Error("Failed to walk backup dir", "error", err)
		return
	}

	// 1. Time Retention Check
	for i := len(backupFiles) - 1; i >= 0; i-- {
		f := backupFiles[i]
		age := time.Since(f.info.ModTime())
		if age.Hours() > float64(cfg.KettasLog.Backup.RetentionDays*24) {
			slog.Info("Deleting old backup due to retention time", "file", f.path, "age_days", int(age.Hours()/24))
			if err := os.Remove(f.path); err == nil {
				totalSize -= f.info.Size()
			}
			backupFiles = append(backupFiles[:i], backupFiles[i+1:]...)
		}
	}

	// 2. Size Retention Check
	maxSizeBytes := cfg.KettasLog.Backup.MaxBackupSizeMB * 1024 * 1024
	if totalSize > maxSizeBytes {
		slog.Info("Backup dir size exceeded limit, cleaning old backups",
			"current_mb", totalSize/1024/1024,
			"max_mb", cfg.KettasLog.Backup.MaxBackupSizeMB,
		)

		// Dosyaları eskiden yeniye sırala
		sort.Slice(backupFiles, func(i, j int) bool {
			return backupFiles[i].info.ModTime().Before(backupFiles[j].info.ModTime())
		})

		for _, f := range backupFiles {
			if totalSize <= maxSizeBytes {
				break
			}
			slog.Info("Deleting backup to free space", "file", f.path)
			if err := os.Remove(f.path); err == nil {
				totalSize -= f.info.Size()
			}
		}
	}

	// Boş backup alt klasörlerini temizle
	cleanEmptyDirs(backupDir)
}

// Helpers

type backupFileInfo struct {
	path string
	info os.FileInfo
}

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

// findAllJSONFiles bir dizindeki tüm JSON dosyalarını döner (dosya adları, yol değil).
func findAllJSONFiles(dirPath string) []string {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}

	var jsonFiles []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		jsonFiles = append(jsonFiles, f.Name())
	}
	return jsonFiles
}

// cleanEmptyDirs bir dizin altındaki boş alt klasörleri siler.
func cleanEmptyDirs(parentDir string) {
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(parentDir, entry.Name())
		subEntries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		// .DS_Store gibi gizli dosyaları sayma
		isEmpty := true
		for _, se := range subEntries {
			if !strings.HasPrefix(se.Name(), ".") {
				isEmpty = false
				break
			}
		}
		if isEmpty {
			os.RemoveAll(subDir)
			slog.Info("Boş klasör silindi", "path", subDir)
		}
	}
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
