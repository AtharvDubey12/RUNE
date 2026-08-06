// this is the script to run in case you're using RUNE Core as a standalone execution system for single machine infra
// api + execution 
// also, if you want to use sync routes, RUNE Cluster is incompatible with it, you must use RUNE Core standalone.
// go run cmd/RUNE/main.go
package main

import (
	"database/sql"
	"log"

	"github.com/gofiber/fiber/v2"

	"RUNE/internal/executor"
	"RUNE/internal/handlers"
	"RUNE/internal/queue"
	"RUNE/internal/worker"
	
	_ "github.com/lib/pq"
)

func main() {
	// Init Database
	dbConn, err := sql.Open("postgres", "postgres://postgres:RUNEpost@localhost:5432/runedb?sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbConn.Close()

	if err := dbConn.Ping(); err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	log.Println("[Database] Successfully connected to PostgreSQL!")

	jobQueue := queue.NewEngineQueue(1000) 
	boxManager := executor.NewBoxManager(50) // The semaphores of 50 boxes

	// Start Background Worker
	go worker.StartDispatcher(dbConn, jobQueue, boxManager)

	// Inject the shared jobQueue and boxManager into the EngineWrapper
	engine := &handlers.EngineWrapper{
		JobQueue:   jobQueue,
		BoxManager: boxManager,
	}
	
	submissionHandler := handlers.NewSubmissionHandler(dbConn, engine)

	app := fiber.New()

	app.Post("/submissions", func(c *fiber.Ctx) error {  // temporarily placed here, to be shifted to dedicated routes file
		wait := c.Query("wait")
		if wait == "true" {
			return submissionHandler.CreateSyncSubmission(c)
		}
		return submissionHandler.CreateAsyncSubmission(c)
	})
	app.Get("/submissions/batch", submissionHandler.GetBatchSubmissions)
	app.Get("/submissions/:token", submissionHandler.GetSubmission)
	app.Post("/submissions/batch", submissionHandler.CreateBatchSubmission) // ASYNC ONLY ROUTE.

	log.Println("Starting RUNE CORE on port 3000...")
	if err := app.Listen(":3000"); err != nil {
		log.Fatalf("Fiber server failed: %v", err)
	}
}