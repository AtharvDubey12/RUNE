package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"

	"RUNE/internal/broker"
	"RUNE/internal/db"
	"RUNE/internal/executor"
	"RUNE/internal/models"
	"RUNE/internal/queue"
	"RUNE/internal/worker"
)

func getOrGenerateWorkerID() string {
	const filename = "worker_id.txt"
	if data, err := os.ReadFile(filename); err == nil {
		return string(data)
	}

	hostname, _ := os.Hostname()
	newID := fmt.Sprintf("%s-%s", hostname, uuid.New().String()[:8])
	_ = os.WriteFile(filename, []byte(newID), 0644)
	return newID
}

func main() {
	dsn := "postgres://postgres:RUNEpost@localhost:5432/runedb?sslmode=disable"
	dbConn, err := db.Connect(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbConn.Close()

	redisBroker, err := broker.NewBroker("redis://localhost:6379/0")
	if err != nil {
		log.Fatalf("Failed to connect to Redis broker: %v", err)
	}

	workerID := getOrGenerateWorkerID()
	
	jobQueue := queue.NewEngineQueue(1000)
	boxManager := executor.NewBoxManager(50)

	//  1:1 Node Capacity Semaphore (first is for poller and second is for boxes)
	nodeCapacity := make(chan struct{}, 50)

	go worker.StartDispatcher(dbConn, jobQueue, boxManager, nodeCapacity)

	// job recovery (in case of a crash, for the local queue)
	recoverOrphanedJobs(dbConn, workerID, jobQueue, nodeCapacity)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Poller to grab job from global redis queue
	go redisBroker.StartPoller(ctx, workerID, dbConn, jobQueue, nodeCapacity)

	log.Printf("RUNE Core [%s] initialized and actively polling Redis...", workerID)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down RUNE Core...")
}

func recoverOrphanedJobs(dbConn *sql.DB, workerID string, localQueue *queue.EngineQueue, nodeCapacity chan struct{}) {
	log.Printf("[Recovery] Sweeping DB for orphaned jobs assigned to %s...", workerID)

	query := `
        SELECT token, source_code, language_id, stdin, expected_output, cpu_time_limit, memory_limit, callback_url
        FROM submissions
        WHERE worker_id = $1 AND status_id IN (1, 2)
    `
	rows, err := dbConn.Query(query, workerID)
	if err != nil {
		log.Printf("[Recovery Error] Failed to query orphaned jobs: %v", err)
		return
	}

	var pendingRecovery []queue.Job
	for rows.Next() {
		var token, sourceCode string
		var langID int
		var stdin, expOut *string
		var cpuLimit *float64
		var memLimit *int
		var cbUrl *string

		if err := rows.Scan(&token, &sourceCode, &langID, &stdin, &expOut, &cpuLimit, &memLimit, &cbUrl); err != nil {
			continue
		}

		req := models.Judge0Request{
			SourceCode:  sourceCode,
			LanguageID:  langID,
			CallbackUrl: cbUrl,
		}
		if stdin != nil { req.Stdin = *stdin }
		if expOut != nil { req.ExpectedOutput = *expOut }
		if cpuLimit != nil { req.CpuTimeLimit = *cpuLimit }
		if memLimit != nil { req.MemoryLimit = *memLimit }

		pendingRecovery = append(pendingRecovery, queue.Job{Token: token, Request: req})
	}
	rows.Close()

	count := 0
	for _, job := range pendingRecovery {	// MAX ORPHANED JOBS = MAX NUMBER OF BOXES POSSIBLE --> NO WAITING
		// Acquire a ticket before pushing to the internal queue.
		nodeCapacity <- struct{}{}
		_ = localQueue.Enqueue(job)
		count++
	}

	log.Printf("[Recovery] Successfully recovered and re-queued %d jobs.", count)
}