package main

import (
	"log"
	"github.com/gofiber/fiber/v2"
	"RUNE/api"
)

func main() {
	app := fiber.New(fiber.Config{DisableStartupMessage: true,})

	api.SetupRoutes(app)

	log.Println("RUNE Execution Engine listening on port 3000...")
	log.Fatal(app.Listen(":3000"))
}