package main

import (
	"log/slog"
	"os"

	"log-server/config"
	"log-server/db"
	"log-server/logger"
	"log-server/router"
	"log-server/worker"

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
		BodyLimit: int(cfg.KettasLog.MaxFileSize + 1024*1024),
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
	bm := worker.NewBackupManager()
	bm.Start()
	defer bm.Stop()

	slog.Info("Server starting", "port", cfg.Server.Port)
	if err := app.Listen(cfg.Server.Port); err != nil {
		slog.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}
