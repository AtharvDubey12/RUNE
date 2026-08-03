package api

import (
	"github.com/gofiber/fiber/v2"
	"RUNE/api/handlers"
)

func SetupRoutes(app *fiber.App) {
	// Synchronous submission endpoint
	app.Post("/submissions", handlers.CreateSyncSubmission)
}