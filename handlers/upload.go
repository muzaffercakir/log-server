package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"log-server/config"
	"log-server/db"

	"github.com/gofiber/fiber/v2"
)

func Upload(c *fiber.Ctx) error {
	cfg := config.Get()

	file, err := c.FormFile("file")
	if err != nil {
		slog.Warn("File upload failed", "error", err.Error())
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "File upload failed",
		})
	}

	// 1. Path Traversal Koruması
	filename := filepath.Base(file.Filename)
	if filename == "." || filename == "/" {
		slog.Warn("Invalid filename", "filename", file.Filename)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid filename"})
	}

	// 2. Uzantı Kontrolü
	if filepath.Ext(filename) != ".zip" {
		slog.Warn("Invalid file type", "filename", filename)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Only zip files are allowed",
		})
	}

	// 3. Magic Bytes (Dosya İçeriği) Kontrolü
	src, err := file.Open()
	if err != nil {
		slog.Error("Failed to open uploaded file", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "File processing failed"})
	}
	defer src.Close()

	header := make([]byte, 4)
	if _, err := src.Read(header); err != nil {
		slog.Error("Failed to read file header", "error", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "File too short"})
	}

	// Zip Magic Bytes: PK\x03\x04 (50 4B 03 04)
	if string(header) != "PK\x03\x04" {
		slog.Warn("Invalid file content (not a zip)", "header", fmt.Sprintf("%x", header))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid file content"})
	}

	if file.Size > cfg.KettasLog.MaxFileSizeMB*1024*1024 {
		slog.Warn("File too large", "filename", filename, "size", file.Size)
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": fmt.Sprintf("File size exceeds limit of %d MB", cfg.KettasLog.MaxFileSizeMB),
		})
	}

	// Dosya adı formatı: HOMEID_TIMESTAMP.zip veya home_id_HOMEID_TIMESTAMP.zip
	cleanFilename := strings.TrimPrefix(filename, "home_id_")

	parts := strings.Split(cleanFilename, "_")
	if len(parts) < 2 {
		slog.Warn("Invalid filename format", "filename", filename)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid filename format. Expected HOMEID_TIMESTAMP.zip or home_id_HOMEID_TIMESTAMP.zip",
		})
	}
	homeId := parts[0]

	// Zip dosyasını geçici dizine kaydet
	tempFilePath := filepath.Join(cfg.KettasLog.UploadDir, filename)
	if err := c.SaveFile(file, tempFilePath); err != nil {
		slog.Error("Failed to save file", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save file",
		})
	}
	defer os.Remove(tempFilePath)

	// Hedef dizin oluştur (homeId bazlı, örn: logs/home_id_UUID)
	targetDirName := fmt.Sprintf("home_id_%s", homeId)
	targetDir := filepath.Join(cfg.KettasLog.LogsDir, targetDirName)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		slog.Error("Failed to create target directory", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create target directory",
		})
	}

	// Zip'i aç
	cmd := exec.Command("unzip", "-P", cfg.KettasLog.ZipPassword, "-o", tempFilePath, "-d", targetDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("Unzip error", "error", err.Error(), "output", string(output))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":  "Failed to unzip file",
			"detail": "Processing failed",
		})
	}

	slog.Info("File processed successfully", "filename", filename, "home_id", homeId)

	// JSON dosyalarını oku ve MongoDB'ye ekle (eğer aktifse)
	var insertedCount int
	var dbErr error

	if cfg.DB.Enabled {
		insertedCount, dbErr = processAndInsertLogs(targetDir, homeId)
		if dbErr != nil {
			slog.Error("MongoDB insert hatası", "error", dbErr, "home_id", homeId)
			// Dosya kaydedildi ama DB insert başarısız - yine de 200 dönebiliriz
			return c.Status(fiber.StatusOK).JSON(fiber.Map{
				"message":  "File uploaded and extracted, but db insert failed",
				"home_id":  homeId,
				"db_error": dbErr.Error(),
			})
		}
	} else {
		slog.Info("MongoDB insert atlandı (devredışı)", "home_id", homeId)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message":        "File uploaded, extracted and processed",
		"home_id":        homeId,
		"inserted_count": insertedCount,
		"db_enabled":     cfg.DB.Enabled,
	})
}

// processAndInsertLogs hedef dizindeki JSON dosyalarını okur ve MongoDB'ye ekler
func processAndInsertLogs(targetDir, homeId string) (int, error) {
	files, err := os.ReadDir(targetDir)
	if err != nil {
		return 0, fmt.Errorf("dizin okunamadı: %v", err)
	}

	var allDocs []interface{}

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(targetDir, f.Name())
		file, err := os.Open(filePath)
		if err != nil {
			slog.Warn("JSON dosyası açılamadı", "file", f.Name(), "error", err)
			continue
		}
		defer file.Close()

		decoder := json.NewDecoder(file)
		for decoder.More() {
			var logEntry map[string]interface{}
			if err := decoder.Decode(&logEntry); err != nil {
				slog.Warn("JSON decode hatası", "file", f.Name(), "error", err)
				break // Hatalı kayıtta bu dosyayı geç, diğerine bak
			}

			// Her log kaydına db fields ekle
			now := time.Now().UTC()
			logEntry["db_server_received_at_utc"] = now
			logEntry["db_server_received_at_timestamp"] = now.Unix()
			allDocs = append(allDocs, logEntry)
		}
	}

	if len(allDocs) == 0 {
		slog.Info("Eklenecek log kaydı bulunamadı", "home_id", homeId)
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.InsertMany(ctx, allDocs); err != nil {
		return 0, err
	}

	return len(allDocs), nil
}
