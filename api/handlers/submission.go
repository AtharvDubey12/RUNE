package handlers

import (
	"github.com/gofiber/fiber/v2"
	"RUNE/internal/models"
	// "RUNE/internal/executor" // TODO: Build executor
)

// CreateSyncSubmission --> handles POST /submissions/?wait=true [ for synchronous submissions ]
func CreateSyncSubmission(c *fiber.Ctx) error {
	// incoming JSON
	var req models.Judge0Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}

	// mock executor, TODO: build executor
	// result, err := executor.RunCode(req)
	
	// temp mock res
	status := models.Status{ID: 3, Description: "Accepted"}
	mockStdout := "Hello World\n"
	
	res := models.Judge0Response{
		Stdout: &mockStdout,
		Time:   0.012,
		Memory: 1024.0,
		Status: status,
	}

	return c.Status(fiber.StatusCreated).JSON(res)
}