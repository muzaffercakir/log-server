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

	// Tüm evlerin loglarını tarih bazlı zip olarak döner
	// Body: start_date, (end_date opsiyonel)
	app.Get("/all-logs", handlers.GetAllLogs)

	// Belirli bir evin loglarını döner
	// Body: home_id, start_date, (end_date opsiyonel)
	app.Get("/home-logs", handlers.GetLogByHomeId)
}
