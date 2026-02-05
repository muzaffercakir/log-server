package middleware

import (
	"log/slog"

	"log-server/config"

	"github.com/gofiber/fiber/v2"
)

func Auth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		cfg := config.Get()
		key := c.Get("X-API-Key")

		if key != cfg.ApiKey {
			slog.Warn("Unauthorized access attempt", "ip", c.IP(), "provided_key", key)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Unauthorized",
			})
		}

		return c.Next()
	}
}
