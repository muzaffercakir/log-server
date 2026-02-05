package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	ApiKey      string            `mapstructure:"api_key"`
	InternalLog InternalLogConfig `mapstructure:"internal_log"`
	KettasLog   KettasLogConfig   `mapstructure:"kettas_log"`
}

type ServerConfig struct {
	Port string `mapstructure:"port"`
}

type InternalLogConfig struct {
	LogFile string `mapstructure:"log_file"`
}

type KettasLogConfig struct {
	UploadDir   string `mapstructure:"upload_dir"`
	LogsDir     string `mapstructure:"logs_dir"`
	ZipPassword string `mapstructure:"zip_password"`
	MaxFileSize int64  `mapstructure:"max_file_size"`
}

var cfg Config

func initConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Error reading config file: %v\n", err)
		os.Exit(1)
	}

	if err := viper.Unmarshal(&cfg); err != nil {
		fmt.Printf("Error unmarshalling config: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	initConfig()

	// Setup Lumberjack for log rotation
	logRotator := &lumberjack.Logger{
		Filename:   cfg.InternalLog.LogFile,
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	}

	// Setup slog with MultiWriter (Stdout + File)
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stdout, logRotator), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		BodyLimit: int(cfg.KettasLog.MaxFileSize + 1024*1024), // Allow slightly more for headers/overhead
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			slog.Error("Fiber Error", "status", code, "error", err.Error(), "path", c.Path())
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	// Request Logging Middleware
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start)

		status := c.Response().StatusCode()
		level := slog.LevelInfo
		if status >= 400 && status < 500 {
			level = slog.LevelWarn
		} else if status >= 500 {
			level = slog.LevelError
		}

		logger.Log(c.Context(), level, "Request handled",
			"method", c.Method(),
			"path", c.Path(),
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"ip", c.IP(),
		)
		return err
	})

	// Auth Middleware
	app.Use(func(c *fiber.Ctx) error {
		key := c.Get("X-API-Key")
		if key != cfg.ApiKey {
			slog.Warn("Unauthorized access attempt", "ip", c.IP(), "provided_key", key)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Unauthorized",
			})
		}
		return c.Next()
	})

	// Routes
	app.Post("/upload", handleUpload)

	// Ensure directories exist
	if err := os.MkdirAll(cfg.KettasLog.UploadDir, 0755); err != nil {
		slog.Error("Failed to create working directories", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.KettasLog.LogsDir, 0755); err != nil {
		slog.Error("Failed to create logs directory", "error", err)
		os.Exit(1)
	}

	slog.Info("Server starting", "port", cfg.Server.Port)
	if err := app.Listen(cfg.Server.Port); err != nil {
		slog.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}

func handleUpload(c *fiber.Ctx) error {
	// Get the file
	file, err := c.FormFile("file")
	if err != nil {
		slog.Warn("File upload failed", "error", err.Error())
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "File upload failed",
		})
	}

	// Validate Size
	if file.Size > cfg.KettasLog.MaxFileSize {
		slog.Warn("File too large", "filename", file.Filename, "size", file.Size)
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": fmt.Sprintf("File size exceeds limit of %d MB", cfg.KettasLog.MaxFileSize/1024/1024),
		})
	}

	// Validate Extension
	ext := filepath.Ext(file.Filename)
	if !strings.EqualFold(ext, ".zip") {
		slog.Warn("Invalid file extension", "filename", file.Filename, "extension", ext)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Only .zip files are allowed",
		})
	}

	// Validate filename format: MAC_TIMESTAMP.zip
	filename := file.Filename
	parts := strings.Split(filename, "_")
	if len(parts) < 2 {
		slog.Warn("Invalid filename format", "filename", filename)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid filename format. Expected MAC_TIMESTAMP.zip",
		})
	}
	mac := parts[0]

	// Save file temporarily
	tempFilePath := filepath.Join(cfg.KettasLog.UploadDir, filename)
	if err := c.SaveFile(file, tempFilePath); err != nil {
		slog.Error("Failed to save file", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save file",
		})
	}
	defer os.Remove(tempFilePath) // Clean up temp file

	// Create target directory for MAC
	targetDir := filepath.Join(cfg.KettasLog.LogsDir, mac)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		slog.Error("Failed to create target directory", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create target directory",
		})
	}

	// Unzip file
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
