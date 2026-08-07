package main

import (
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	_ "github.com/lib/pq"

	"RUNE/internal/executor"
	"RUNE/internal/handlers"
	"RUNE/internal/queue"
	"RUNE/internal/worker"
)

func main() {
	// 1. Init Database
	dsn := "postgres://postgres:RUNEpost@localhost:5432/runedb?sslmode=disable"
	dbConn, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbConn.Close()

	if err := dbConn.Ping(); err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	log.Println("[Database] Successfully connected to PostgreSQL!")

	// 2. Setup Local Infrastructure (No Redis)
	jobQueue := queue.NewEngineQueue(1000) 
	boxManager := executor.NewBoxManager(50) 

	// 3. Start Local Dispatcher (pass nil for nodeCapacity since there's no Poller)
	go worker.StartDispatcher(dbConn, jobQueue, boxManager, nil)

	// 4. Inject Local JobQueue and BoxManager into EngineWrapper
	engine := &handlers.EngineWrapper{
		JobQueue:   jobQueue,
		BoxManager: boxManager,
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

	// 5. Start Server
	go func() {
		log.Println("Starting RUNE Standalone Monolith on port 3000...")
		if err := app.Listen(":3000"); err != nil {
			log.Fatalf("Fiber server failed: %v", err)
		}
	}()

	// 6. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down RUNE Standalone...")
}