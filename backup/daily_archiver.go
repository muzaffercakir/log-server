package backup

import (
	"fmt"
	"log-server/config"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DailyLogArchiver config dosyasındaki saatte kettas_logs içindeki her home_id için
// o günün JSON log dosyalarını birleştirip backups klasörüne şifreli zip olarak kaydeder.
type DailyLogArchiver struct {
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewDailyLogArchiver() *DailyLogArchiver {
	return &DailyLogArchiver{
		stopChan: make(chan struct{}),
	}
}

// Start günlük log arşivleme zamanlayıcısını başlatır.
func (dc *DailyLogArchiver) Start() {
	dc.wg.Add(1)
	go func() {
		defer dc.wg.Done()
		slog.Info("Daily log archiver başlatıldı, her gün belirlenen saatte çalışacak")

		// Başlangıçta kaçırılmış arşivleri kontrol et ve işle
		dc.scanAndArchivePastLogs()

		// Periyodik kontrol için ticker (Her 5 saatte bir)
		// Bu, eğer o günkü job bir şekilde fail olduysa (sunucu kapanmasa bile)
		// tekrar deneyip geçmişte kalmış dosyaları yakalamak için.
		missedArchiveCheckTicker := time.NewTicker(5 * time.Hour)
		defer missedArchiveCheckTicker.Stop()

		for {
			now := time.Now()
			// Config'den hedef zamanı al (Varsayılan: 23:58)
			hour, minute := 23, 58
			targetTimeStr := config.Get().KettasLog.Backup.DailyArchiveTargetTime
			if parts := strings.Split(targetTimeStr, ":"); len(parts) == 2 {
				if h, err := strconv.Atoi(parts[0]); err == nil {
					hour = h
				}
				if m, err := strconv.Atoi(parts[1]); err == nil {
					minute = m
				}
			}

			// Bugün belirlenen saati hesapla
			target := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())

			// Eğer hedef zaman geçtiyse yarınki hedef zamanı belirle
			if now.After(target) {
				target = target.Add(24 * time.Hour)
			}

			waitDuration := target.Sub(now)
			slog.Info("Daily log archiver  bir sonraki çalışma zamanı",
				"target", target.Format("02/01/2006 15:04:05"),
				"wait_duration", waitDuration.String(),
			)

			select {
			case <-time.After(waitDuration):
				// O anki günün (veya işlem anındaki günün) loglarını arşivle
				// Not: Normalde 23:58'de çalışacağı için "bugün"ü arşivliyoruz.
				today := time.Now().Format("02_01_2006")
				dc.archiveLogsForDate(today)

			case <-missedArchiveCheckTicker.C:
				// Periyodik olarak geçmişte kalmış, ziplenmemiş dosya var mı bak
				slog.Info("Periyodik log arşiv kontrolü (Missed Archive Check) çalışıyor...")
				dc.scanAndArchivePastLogs()

			case <-dc.stopChan:
				slog.Info("Daily log archiver durduruluyor")
				return
			}
		}
	}()
}

// Günlük log arşivleme zamanlayıcısını durdurur ve mevcut işlemin bitmesini bekler.
func (dc *DailyLogArchiver) Stop() {
	close(dc.stopChan)
	dc.wg.Wait()
}

// archiveLogsForDate belirtilen tarih için tüm home_id'lerdeki logları zipleyip arşivler.
func (dc *DailyLogArchiver) archiveLogsForDate(dateStr string) {
	cfg := config.Get()
	logsDir := cfg.KettasLog.LogsDir
	backupDir := cfg.KettasLog.Backup.BackupDir
	password := cfg.KettasLog.ZipPassword

	slog.Info("Log arşivleme işlemi başlatılıyor", "date", dateStr)

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		slog.Error("Logs dizini okunamadı", "error", err, "path", logsDir)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		homeIdDir := entry.Name()
		homeIdPath := filepath.Join(logsDir, homeIdDir)

		// Belirtilen tarih patternine uyan JSON dosyalarını bul
		datePattern := "_" + dateStr + "_"
		matchingFiles := findMatchingJSONFiles(homeIdPath, datePattern)

		if len(matchingFiles) == 0 {
			continue
		}

		slog.Info("Birleştirilecek log dosyaları bulundu",
			"home_id_dir", homeIdDir,
			"count", len(matchingFiles),
			"date", dateStr,
		)

		zipPath, deletedCount, err := mergeAndZipFiles(matchingFiles, homeIdPath, backupDir, homeIdDir, dateStr, password)
		if err != nil {
			slog.Error("Home ID log archiver hatası", "home_id_dir", homeIdDir, "error", err)
			continue
		}

		if zipPath != "" {
			slog.Info("Log zip oluşturuldu",
				"zip_path", zipPath,
				"home_id_dir", homeIdDir,
				"file_count", len(matchingFiles),
				"deleted_count", deletedCount,
			)
		}
	}
}

// scanAndArchivePastLogs geçmiş tarihlerden kalan (arşivlenmemiş) logları bulup arşivler.
func (dc *DailyLogArchiver) scanAndArchivePastLogs() {
	cfg := config.Get()
	logsDir := cfg.KettasLog.LogsDir

	slog.Info("Geçmiş log taraması başlatılıyor...")

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		slog.Error("Logs dizini taranırken hata", "error", err)
		return
	}

	// Arşivlenecek tarihleri topla (Set mantığı)
	datesToArchive := make(map[string]struct{})
	today := time.Now().Format("02_01_2006")

	// Dosya adından tarih çıkarmak için regex (örn: ..._15_02_2026_...)
	// helpers.go'daki dateRegex benzeri ama burada string manipülasyonu daha hızlı olabilir
	// Format: ..._DD_MM_YYYY_...

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		homeIdPath := filepath.Join(logsDir, entry.Name())
		files, err := os.ReadDir(homeIdPath)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}

			// Dosya adından tarih çıkar
			// Örnek dosya: log_15_02_2026_123456.json veya command_171_15_02_2026_...
			parts := strings.Split(f.Name(), "_")
			for i := 0; i < len(parts)-2; i++ {
				// DD_MM_YYYY arıyoruz
				if len(parts[i]) == 2 && len(parts[i+1]) == 2 && len(parts[i+2]) == 4 {
					possibleDate := fmt.Sprintf("%s_%s_%s", parts[i], parts[i+1], parts[i+2])
					// Tarihi doğrula
					if _, err := time.Parse("02_01_2006", possibleDate); err == nil {
						if possibleDate != today {
							datesToArchive[possibleDate] = struct{}{}
						}
						break
					}
				}
			}
		}
	}

	if len(datesToArchive) == 0 {
		slog.Info("Arşivlenmemiş geçmiş log bulunamadı.")
		return
	}

	slog.Info("Arşivlenmemiş geçmiş tarihler bulundu", "count", len(datesToArchive))

	for dateStr := range datesToArchive {
		slog.Info("Geçmiş tarih arşivleniyor", "date", dateStr)
		dc.archiveLogsForDate(dateStr)
	}
}

// findMatchingJSONFiles belirli bir pattern'e uyan JSON dosyalarını bulur.
func findMatchingJSONFiles(dirPath, pattern string) []string {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}

	var matching []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		if strings.Contains(f.Name(), pattern) {
			matching = append(matching, f.Name())
		}
	}
	return matching
}
