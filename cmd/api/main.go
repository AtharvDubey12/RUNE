package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"

	"RUNE/internal/broker"
	"RUNE/internal/db"
	"RUNE/internal/handlers"
)

func main() {
	// Init Database
	dsn := "postgres://postgres:RUNEpost@localhost:5432/runedb?sslmode=disable"
	dbConn, err := db.Connect(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbConn.Close()

	// Init Redis Broker
	redisBroker, err := broker.NewBroker("redis://localhost:6379/0")
	if err != nil {
		log.Fatalf("Failed to connect to Redis broker: %v", err)
	}

	// Init EngineWrapper with Broker
	engine := &handlers.EngineWrapper{
		Broker:     redisBroker,
		BoxManager: nil, // note for self: API nodes do not execute sandboxes
	}

	submissionHandler := handlers.NewSubmissionHandler(dbConn, engine)
	app := fiber.New()

	app.Post("/submissions", func(c *fiber.Ctx) error {
		wait := c.Query("wait")
		if wait == "true" {
			return submissionHandler.CreateSyncSubmission(c)
		}
		return submissionHandler.CreateAsyncSubmission(c)
	})
	app.Get("/submissions/batch", submissionHandler.GetBatchSubmissions)
	app.Get("/submissions/:token", submissionHandler.GetSubmission)
	app.Post("/submissions/batch", submissionHandler.CreateBatchSubmission)

	go func() {
		log.Println("Starting RUNE API on port 3000...")
		if err := app.Listen(":3000"); err != nil {
			log.Fatalf("Fiber server failed: %v", err)
		}
	}()
	// grace shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down RUNE API...")
}