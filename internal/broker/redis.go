package broker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	//"RUNE/internal/models"
	"RUNE/internal/queue"

	"github.com/redis/go-redis/v9"
)

const GlobalQueueKey = "rune:queue:submissions"

type Broker struct {
	Client *redis.Client
}

func (b *Broker) PopGlobal(ctx context.Context) (*queue.Job, error) {
	result, err := b.Client.BLPop(ctx, 0, GlobalQueueKey).Result()
	if err != nil {
		return nil, err
	}

	var job queue.Job
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, err
	}
	return &job, nil
}


func NewBroker(redisURL string) (*Broker, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	log.Println("[Broker] Successfully connected to Redis!")
	return &Broker{Client: client}, nil
}

// EnqueueGlobal pushes a job payload to the global Redis list
func (b *Broker) EnqueueGlobal(ctx context.Context, job queue.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	return b.Client.RPush(ctx, GlobalQueueKey, data).Err()
}

// StartPoller continuously pulls jobs from Redis using BLPOP, claims them in Postgres,
// and pushes them into RUNE Core's internal EngineQueue.
func (b *Broker) StartPoller(ctx context.Context, workerID string, dbConn *sql.DB, localQueue *queue.EngineQueue, nodeCapacity chan struct{}) {
	log.Printf("[Poller] Starting Redis BLPOP worker for worker ID: %s", workerID)

	for {
		// grab a semaphore to access the redis queue
		select {
		case nodeCapacity <- struct{}{}: // if sema acquired.
		case <-ctx.Done():
			log.Println("[Poller] Worker poller shutting down...")
			return
		}

		// pull from redis after getting a semaphore.
		result, err := b.Client.BLPop(ctx, 0*time.Second, GlobalQueueKey).Result()
		if err != nil {
			<-nodeCapacity // Refund semaphore on fail
			if ctx.Err() != nil {
				return
			}
			log.Printf("[Poller Error] BLPop failed: %v. Retrying in 2s...", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var job queue.Job
		if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
			<-nodeCapacity
			log.Printf("[Poller Error] Corrupted payload in Redis: %v", err)
			continue
		}

		stamped := ClaimJobInDB(dbConn, job.Token, workerID)
		if !stamped {
			<-nodeCapacity // Refund the ticket due to race condition
			continue
		}

		// 3. Push to internal queue
		if err := localQueue.Enqueue(job); err != nil {
			<-nodeCapacity
			log.Printf("[Poller Error] Local queue closed: %v", err)
			_ = b.EnqueueGlobal(ctx, job)
		}
	}
}

// claimJobInDB stamps the worker_id on the submission record
func ClaimJobInDB(dbConn *sql.DB, token string, workerID string) bool {
	query := `
		UPDATE submissions 
		SET worker_id = $1, status_id = 1, status_description = 'In Queue'
		WHERE token = $2 AND (worker_id IS NULL OR worker_id = $1)
		RETURNING token
	`
	var returnedToken string
	err := dbConn.QueryRow(query, workerID, token).Scan(&returnedToken)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[Poller DB Error] Failed to claim token %s: %v", token, err)
		}
		return false
	}
	return true
}