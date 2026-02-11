package main

import (
	"os/signal"
	"syscall"

	"log/slog"
	"os"

	"log-server/backup"
	"log-server/config"
	"log-server/db"
	"log-server/logger"
	"log-server/router"

	"github.com/gofiber/fiber/v2"
)

func main() {
	config.Load()
	logger.Init()

	cfg := config.Get()

	// MongoDB bağlantısı (eğer aktifse)
	if cfg.DB.Enabled {
		if err := db.Connect(); err != nil {
			slog.Error("MongoDB bağlantısı başarısız", "error", err)
			os.Exit(1)
		}
		defer db.Disconnect()
	} else {
		slog.Info("MongoDB bağlantısı atlandı (devredışı)")
	}

	app := fiber.New(fiber.Config{
		BodyLimit: int(cfg.KettasLog.MaxFileSizeMB*1024*1024 + 1024*1024),
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

	router.Setup(app)

	if err := os.MkdirAll(cfg.KettasLog.UploadDir, 0755); err != nil {
		slog.Error("Failed to create working directories", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.KettasLog.LogsDir, 0755); err != nil {
		slog.Error("Failed to create logs directory", "error", err)
		os.Exit(1)
	}

	// Start Backup Manager
	bm := backup.NewBackupManager()
	bm.Start()

	// Graceful Shutdown Channel
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("Server starting", "port", cfg.Server.Port)
		if err := app.Listen(":" + cfg.Server.Port); err != nil {
			slog.Error("Server error", "error", err)
		}
	}()

	<-quit // Wait for signal
	slog.Info("Shutting down server...")

	// 1. Web sunucusunu durdur
	if err := app.Shutdown(); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	// 2. Backup Manager'ı durdur (Varsa süren işlemi bekle)
	bm.Stop()

	slog.Info("Server exited")
}
