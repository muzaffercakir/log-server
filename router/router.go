package router

import (
	"log-server/handlers"
	"log-server/middleware"

	"github.com/gofiber/fiber/v2"
)

func Setup(app *fiber.App) {
	app.Use(middleware.RequestLogger())
	app.Use(middleware.Auth())

	app.Post("/upload", handlers.Upload)
}
