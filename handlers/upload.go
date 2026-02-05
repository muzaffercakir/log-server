package handlers

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log-server/config"

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

	if file.Size > cfg.KettasLog.MaxFileSize {
		slog.Warn("File too large", "filename", file.Filename, "size", file.Size)
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": fmt.Sprintf("File size exceeds limit of %d MB", cfg.KettasLog.MaxFileSize/1024/1024),
		})
	}

	ext := filepath.Ext(file.Filename)
	if !strings.EqualFold(ext, ".zip") {
		slog.Warn("Invalid file extension", "filename", file.Filename, "extension", ext)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Only .zip files are allowed",
		})
	}

	filename := file.Filename
	parts := strings.Split(filename, "_")
	if len(parts) < 2 {
		slog.Warn("Invalid filename format", "filename", filename)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid filename format. Expected MAC_TIMESTAMP.zip",
		})
	}
	mac := parts[0]

	tempFilePath := filepath.Join(cfg.KettasLog.UploadDir, filename)
	if err := c.SaveFile(file, tempFilePath); err != nil {
		slog.Error("Failed to save file", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save file",
		})
	}
	defer os.Remove(tempFilePath)

	targetDir := filepath.Join(cfg.KettasLog.LogsDir, mac)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		slog.Error("Failed to create target directory", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create target directory",
		})
	}

	cmd := exec.Command("unzip", "-P", cfg.KettasLog.ZipPassword, "-o", tempFilePath, "-d", targetDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("Unzip error", "error", err.Error(), "output", string(output))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error":  "Failed to unzip file",
			"detail": "Processing failed",
		})
	}

	slog.Info("File processed successfully", "filename", filename, "mac", mac)
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "File uploaded and extracted successfully",
		"mac":     mac,
	})
}
