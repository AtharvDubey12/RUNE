package main

import (
	"log"
	"github.com/gofiber/fiber/v2"
)

func main() {
	app := fiber.New() // fiber instance

    // temp route
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("RUNE Execution Engine is running!")
	})

    log.Println("Starting RUNE server on port 3000...")
	log.Fatal(app.Listen(":3000"))
}