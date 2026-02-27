package handlers

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log-server/config"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Zip dosya adından tarihi çıkarmak için regex
// Format: home_id_xxx_DD_MM_YYYY_all_event_log.zip veya ..._2.zip
var dateRegex = regexp.MustCompile(`_(\d{2}_\d{2}_\d{4})_all_event_log`)

const dateLayout = "02_01_2006" // DD_MM_YYYY

// Request body struct'ları
type LogRequest struct {
	HomeId    string `json:"home_id"`
	StartDate string `json:"start_date"` // DD_MM_YYYY (zorunlu)
	EndDate   string `json:"end_date"`   // DD_MM_YYYY (opsiyonel, boşsa start_date ile aynı)
}

// ──────────────────────────────────────────────────
// GET /logs — Tüm home_id'ler için log zip dosyalarını döner
// ──────────────────────────────────────────────────

// GetAllLogs tüm home_id klasörlerindeki zip dosyalarını
// (tarih filtresine göre) tek bir zip içinde toplar.
// Ziplenen yapı: home_id_XXX/filename.zip
// Body: { "start_date": "DD_MM_YYYY", "end_date": "DD_MM_YYYY" }
func GetAllLogs(c *fiber.Ctx) error {
	var req LogRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz request body",
		})
	}

	if req.StartDate == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "start_date parametresi gerekli",
		})
	}

	startDate, err := time.Parse(dateLayout, req.StartDate)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz start_date formatı. Beklenen: DD_MM_YYYY",
		})
	}

	// end_date boşsa start_date ile aynı kabul et (tek gün)
	endDate := startDate
	if req.EndDate != "" {
		endDate, err = time.Parse(dateLayout, req.EndDate)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Geçersiz end_date formatı. Beklenen: DD_MM_YYYY",
			})
		}
	}

	if endDate.Before(startDate) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "end_date, start_date'den önce olamaz",
		})
	}

	cfg := config.Get()
	backupRoot := cfg.KettasLog.Backup.BackupDir

	// Backup kök dizinini oku
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		slog.Error("Backup kök dizini okunamadı", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Backup dizini okunamadı",
		})
	}

	// Response headerları
	var bundleName string
	if req.EndDate == "" || req.StartDate == req.EndDate {
		bundleName = fmt.Sprintf("all_homes_logs_%s.zip", req.StartDate)
	} else {
		bundleName = fmt.Sprintf("all_homes_logs_%s_to_%s.zip", req.StartDate, req.EndDate)
	}

	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", bundleName))

	// Stream writer ile zip oluştur
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		zipWriter := zip.NewWriter(w)
		defer zipWriter.Close()

		for _, entry := range entries {
			// Sadece home_id_ ile başlayan klasörleri işle
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "home_id_") {
				continue
			}

			homeDirName := entry.Name()
			homePath := filepath.Join(backupRoot, homeDirName)
			
			matchingFiles, err := findZipsByDateRange(homePath, startDate, endDate)
			if err != nil {
				// Bir klasörde hata olsa bile diğerlerine devam et
				slog.Error("Home dizini taranırken hata", "dir", homePath, "error", err)
				continue
			}

			for _, filePath := range matchingFiles {
				// Zip içindeki yapı: home_id_XXX/dosya.zip
				archiveName := filepath.Join(homeDirName, filepath.Base(filePath))
				if err := addFileToZip(zipWriter, filePath, archiveName); err != nil {
					slog.Error("Dosya zip'e eklenemedi", "file", filePath, "error", err)
				}
			}
		}
	})

	return nil
}

// ──────────────────────────────────────────────────
// GET /logs/home — Tek bir home_id log zip dosyalarını döner
// ──────────────────────────────────────────────────

// GetLogByHomeId belirli tarih veya tarih aralığındaki zip dosyalarını döner.
// Sadece start_date → o günün zip'lerini döner
// start_date + end_date → aralıktaki (dahil) zip'leri döner
// Body: { "home_id": "...", "start_date": "DD_MM_YYYY", "end_date": "DD_MM_YYYY" }
func GetLogByHomeId(c *fiber.Ctx) error {
	var req LogRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz request body",
		})
	}

	if req.HomeId == "" || req.StartDate == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "home_id ve start_date parametreleri gerekli",
		})
	}

	startDate, err := time.Parse(dateLayout, req.StartDate)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Geçersiz start_date formatı. Beklenen: DD_MM_YYYY",
		})
	}

	// end_date boşsa start_date ile aynı kabul et (tek gün)
	endDate := startDate
	if req.EndDate != "" {
		endDate, err = time.Parse(dateLayout, req.EndDate)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Geçersiz end_date formatı. Beklenen: DD_MM_YYYY",
			})
		}
	}

	if endDate.Before(startDate) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "end_date, start_date'den önce olamaz",
		})
	}

	cfg := config.Get()
	homeIdDir := fmt.Sprintf("home_id_%s", req.HomeId)
	backupPath := filepath.Join(cfg.KettasLog.Backup.BackupDir, homeIdDir)

	// Backup dizini var mı kontrol et
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Bu home_id için backup bulunamadı",
		})
	}

	// Tarih aralığına uyan zip dosyalarını bul
	matchingFiles, err := findZipsByDateRange(backupPath, startDate, endDate)
	if err != nil {
		slog.Error("Zip dosyaları aranamadı", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Backup dosyaları okunamadı",
		})
	}

	if len(matchingFiles) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Belirtilen tarih(ler) için zip dosyası bulunamadı",
		})
	}

	// Tek zip ise direkt gönder
	if len(matchingFiles) == 1 {
		filePath := matchingFiles[0]
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(filePath)))
		return c.SendFile(filePath)
	}

	// Birden fazla zip → hepsini tek bir zip'e sar
	var bundleName string
	if req.EndDate == "" || req.StartDate == req.EndDate {
		bundleName = fmt.Sprintf("%s_%s_all_event_log_merged.zip", homeIdDir, req.StartDate)
	} else {
		bundleName = fmt.Sprintf("%s_%s_to_%s_logs.zip", homeIdDir, req.StartDate, req.EndDate)
	}

	c.Set("Content-Type", "application/zip")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", bundleName))

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		zipWriter := zip.NewWriter(w)
		defer zipWriter.Close()

		for _, filePath := range matchingFiles {
			if err := addFileToZip(zipWriter, filePath, filepath.Base(filePath)); err != nil {
				slog.Error("Zip'e dosya eklenemedi", "file", filePath, "error", err)
				continue
			}
		}
	})

	return nil
}

// ──────────────────────────────────────────────────
// SendToAiService — AI model servisine zip gönderir
// ──────────────────────────────────────────────────

// SendToAiService belirtilen home_id ve tarihe ait zip dosyasını
// AI model servisine POST isteği ile gönderir.
// Tetikleme mekanizması henüz belirlenmedi (TODO).
// homeId: ev kimliği (UUID)
// date: tarih string'i (DD_MM_YYYY)
//todo 
func SendToAiService(homeId, date string) error {
	cfg := config.Get()

	// AI servis URL'i kontrol et
	if cfg.AiService.Url == "" {
		slog.Warn("AI service URL yapılandırılmamış, istek atlanıyor")
		return fmt.Errorf("ai_service.url yapılandırılmamış")
	}

	homeIdDir := fmt.Sprintf("home_id_%s", homeId)
	backupPath := filepath.Join(cfg.KettasLog.Backup.BackupDir, homeIdDir)

	// Bu tarihe ait zip dosyalarını bul
	dateTime, err := time.Parse(dateLayout, date)
	if err != nil {
		return fmt.Errorf("geçersiz tarih formatı: %w", err)
	}

	matchingFiles, err := findZipsByDateRange(backupPath, dateTime, dateTime)
	if err != nil {
		return fmt.Errorf("zip dosyaları bulunamadı: %w", err)
	}

	if len(matchingFiles) == 0 {
		return fmt.Errorf("bu tarih için zip dosyası bulunamadı: home_id=%s, date=%s", homeId, date)
	}

	// Her bir zip dosyasını AI servisine gönder
	targetUrl := cfg.AiService.Url + cfg.AiService.Endpoint

	for _, zipPath := range matchingFiles {
		if err := postZipToAiService(targetUrl, homeId, date, zipPath); err != nil {
			slog.Error("AI servisine zip gönderilemedi",
				"zip_path", zipPath,
				"error", err,
			)
			continue
		}

		slog.Info("AI servisine zip gönderildi",
			"zip_path", zipPath,
			"home_id", homeId,
			"date", date,
			"target_url", targetUrl,
		)
	}

	return nil
}

// postZipToAiService tek bir zip dosyasını multipart/form-data ile AI servisine POST eder.
func postZipToAiService(targetUrl, homeId, date, zipPath string) error {
	file, err := os.Open(zipPath)
	if err != nil {
		return fmt.Errorf("zip dosyası açılamadı: %w", err)
	}
	defer file.Close()

	// Multipart form oluştur
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// home_id field
	if err := writer.WriteField("home_id", homeId); err != nil {
		return fmt.Errorf("home_id field yazılamadı: %w", err)
	}

	// date field
	if err := writer.WriteField("date", date); err != nil {
		return fmt.Errorf("date field yazılamadı: %w", err)
	}

	// zip file field
	part, err := writer.CreateFormFile("file", filepath.Base(zipPath))
	if err != nil {
		return fmt.Errorf("form file oluşturulamadı: %w", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("dosya kopyalanamadı: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("multipart writer kapatılamadı: %w", err)
	}

	// POST isteği
	req, err := http.NewRequest(http.MethodPost, targetUrl, body)
	if err != nil {
		return fmt.Errorf("request oluşturulamadı: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("AI servisine istek başarısız: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AI servisi hata döndü: %s", resp.Status)
	}

	return nil
}

// findZipsByDateRange backupPath içinde tarih aralığına uyan zip dosyalarını bulur.
func findZipsByDateRange(backupPath string, startDate, endDate time.Time) ([]string, error) {
	files, err := os.ReadDir(backupPath)
	if err != nil {
		return nil, err
	}

	var matchingFiles []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".zip") {
			continue
		}

		fileDate, ok := extractDateFromZipName(f.Name())
		if !ok {
			continue
		}

		// startDate <= fileDate <= endDate (dahil)
		if (fileDate.Equal(startDate) || fileDate.After(startDate)) &&
			(fileDate.Equal(endDate) || fileDate.Before(endDate)) {
			matchingFiles = append(matchingFiles, filepath.Join(backupPath, f.Name()))
		}
	}

	return matchingFiles, nil
}

// extractDateFromZipName zip dosya adından tarihi çıkarır.
func extractDateFromZipName(fileName string) (time.Time, bool) {
	matches := dateRegex.FindStringSubmatch(fileName)
	if len(matches) < 2 {
		return time.Time{}, false
	}

	t, err := time.Parse(dateLayout, matches[1])
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}

// addFileToZip bir dosyayı zip archive'a ekler.
func addFileToZip(zipWriter *zip.Writer, filePath string, archiveName string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = archiveName
	header.Method = zip.Deflate

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	return err
}
