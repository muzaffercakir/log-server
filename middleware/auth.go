package middleware

import (
	"log/slog"

	"log-server/config"

	"github.com/gofiber/fiber/v2"
)

func Auth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		cfg := config.Get()

		// api_key config'deki header adını kullan (ör: "inohom-api-key")
		headerName := cfg.Auth.ApiKey
		if headerName == "" {
			headerName = "inohom-api-key"
		}

		value := c.Get(headerName)

		if value != cfg.Auth.ApiValue {
			slog.Warn("Unauthorized access attempt", "ip", c.IP(), "header", headerName, "provided_value", value)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Unauthorized",
			})
		}

		return c.Next()
	}
}
