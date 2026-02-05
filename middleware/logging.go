package middleware

import (
	"log/slog"
	"time"

	"log-server/logger"

	"github.com/gofiber/fiber/v2"
)

func RequestLogger() fiber.Handler {
	return func(c *fiber.Ctx) error {
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

		logger.Get().Log(c.Context(), level, "Request handled",
			"method", c.Method(),
			"path", c.Path(),
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"ip", c.IP(),
		)

		return err
	}
}
