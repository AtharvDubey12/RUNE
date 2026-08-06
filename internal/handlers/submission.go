package handlers

import (
	"database/sql"
	"encoding/base64"
	"log"
	"path/filepath"
	"strings"

	"RUNE/internal/broker"
	"RUNE/internal/executor"
	"RUNE/internal/models"
	"RUNE/internal/queue"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type EngineWrapper struct {
	Broker     *broker.Broker
	BoxManager *executor.BoxManager
}

type SubmissionHandler struct {
	DB     *sql.DB
	Engine *EngineWrapper
}

func NewSubmissionHandler(db *sql.DB, engine *EngineWrapper) *SubmissionHandler {
	return &SubmissionHandler{
		DB:     db,
		Engine: engine,
	}
}

func decodeBase64Str(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func compareOutputs(actual, expected string) bool {
	actualTokens := strings.Fields(actual)
	expectedTokens := strings.Fields(expected)

	if len(actualTokens) != len(expectedTokens) {
		return false
	}

	for i, token := range actualTokens {
		if token != expectedTokens[i] {
			return false
		}
	}

	return true
}

// CreateSyncSubmission handles POST /submissions/?wait=true
func (h *SubmissionHandler) CreateSyncSubmission(c *fiber.Ctx) error {

	var req models.Judge0Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}

	isBase64 := false
	if req.Base64Encoded != nil {
		isBase64 = *req.Base64Encoded
	}

	if isBase64 {
		var err error
		req.SourceCode, err = decodeBase64Str(req.SourceCode)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid base64 in source_code"})
		}

		req.Stdin, err = decodeBase64Str(req.Stdin)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid base64 in stdin"})
		}
	}

	// Language Configs
	var sourceFileName, executableName string
	switch req.LanguageID {
	case 54: // C++
		sourceFileName = "main.cpp"
		executableName = "prog"
	case 71: // Python 3
		sourceFileName = "main.py"
		executableName = "main.py"
	case 62: // Java
		sourceFileName = "Main.java"
		executableName = "Main"
	case 63: // JavaScript
		sourceFileName = "main.js"
		executableName = "main.js"
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Unsupported language ID"})
	}

	// Code Execution begins below
	boxID := h.Engine.BoxManager.Acquire()
	defer h.Engine.BoxManager.Release(boxID)

	box := executor.NewSandbox(boxID)
	if err := box.Init(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Sandbox initialization failed"})
	}
	defer func() {
		if err := box.Cleanup(); err != nil {
			log.Printf("Cleanup failed for box %d: %v", boxID, err)
		}
	}()

	// Write source code and stdin to the sandbox
	if err := box.Write(sourceFileName, req.SourceCode); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to write source code"})
	}
	
	hasStdin := false
	if req.Stdin != "" {
		if err := box.Write("stdin.txt", req.Stdin); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to write stdin"})
		}
		hasStdin = true
	}

	// Compile Target
	var compileOutput string
	var compErr error
	switch req.LanguageID {
	case 54:
		compileOutput, compErr = box.CompileCPP(sourceFileName, executableName)
	case 71:
		compileOutput, compErr = box.CompilePython(sourceFileName)
	case 62:
		compileOutput, compErr = box.CompileJava(sourceFileName)
	case 63:
		compileOutput, compErr = box.CompileJS(sourceFileName)
	}

	if compErr != nil {
		// Compilation Error Status Code in Judge0 is 6
		return c.Status(fiber.StatusCreated).JSON(models.Judge0Response{
			Status:        models.Status{ID: 6, Description: "Compilation Error"},
			CompileOutput: &compileOutput,
		})
	}

	timeLimit := float64(req.CpuTimeLimit)
	if timeLimit <= 0 {
		timeLimit = 2.0 // Default: 2 seconds
	}

	memoryLimit := float64(req.MemoryLimit)
	if memoryLimit <= 0 {
		memoryLimit = 262144.0 // Default: 256 MB
	}

	// Run Target
	var runOutput string
	var runErr error
	var runStderr string
	switch req.LanguageID {
	case 54:
		runOutput, runStderr, runErr = box.RunCPP(executableName, timeLimit, memoryLimit, hasStdin)
	case 71:
		runOutput, runStderr, runErr = box.RunPython(executableName, timeLimit, memoryLimit, hasStdin)
	case 62:
		runOutput, runStderr, runErr = box.RunJava(executableName, timeLimit, memoryLimit, hasStdin)
	case 63:
		runOutput, runStderr, runErr = box.RunJS(executableName, timeLimit, memoryLimit, hasStdin)
	}

	metaPath := filepath.Join(box.Path, "meta.txt")
	metaData, metaErr := executor.ParseMetaFile(metaPath)
	if metaErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to parse execution metrics",
		})
	}

	// Map isolate status codes to Judge0 internal IDs
	var status models.Status
	switch metaData.Status {
	case "":
		// Isolate leaves status empty on a successful, clean exit
		status = models.Status{ID: 3, Description: "Accepted"}
	case "TO":
		status = models.Status{ID: 5, Description: "Time Limit Exceeded"}
	case "RE", "SG":
		status = models.Status{ID: 11, Description: "Runtime Error (NZEC)"}
	default:
		// "XX" means isolate internal errors or unexpected states
		status = models.Status{ID: 13, Description: "Internal Error"}
	}

	if float64(metaData.Memory) > memoryLimit {
		status = models.Status{ID: 11, Description: "Memory Limit Exceeded"}
	}

	if status.ID == 3 && req.ExpectedOutput != "" {

		expectedOut := req.ExpectedOutput
		if isBase64 {
			decodedExpected, err := decodeBase64Str(req.ExpectedOutput)
			if err == nil {
				expectedOut = decodedExpected
			}
		}

		if !compareOutputs(runOutput, expectedOut) {
			status = models.Status{ID: 4, Description: "Wrong Answer"}
		}
	}

	// Base64 encode the output if ?base64_encoded=true
	if isBase64 {
		encodedStdout := base64.StdEncoding.EncodeToString([]byte(runOutput))
		runOutput = encodedStdout
		if runStderr != "" {
			runStderr = base64.StdEncoding.EncodeToString([]byte(runStderr))
		}
	}

	var finalStderr *string
	if runStderr != "" {
		finalStderr = &runStderr
	}

	res := models.Judge0Response{
		Stdout: &runOutput,
		Time:   metaData.Time,            // execution time in seconds
		Memory: float64(metaData.Memory), // memory usage in KB
		Status: status,
		Stderr: finalStderr,
	}

	// Handle execution errors that aren't mapped via Isolate status 
	if runErr != nil && status.ID == 3 {
		status = models.Status{ID: 13, Description: "Internal Error"}
		res.Status = status
	}

	return c.Status(fiber.StatusCreated).JSON(res)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilIf0Int(i int) *int {
	if (i == 0) {
		return nil
	}
	return &i
}
func nilIf0Float(i float64) *float64 {
	if (i == 0) {
		return nil
	}
	return &i
}

func (h *SubmissionHandler) CreateAsyncSubmission(c *fiber.Ctx) error {
	var req models.Judge0Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}

	token := uuid.New().String()

	// Insert into DB (status_id = 0 -> "In Global Queue")
	_, err := h.DB.Exec(`
		INSERT INTO submissions 
		(token, source_code, language_id, stdin, expected_output, cpu_time_limit, memory_limit, callback_url, status_id, status_description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, 'In Global Queue')
	`, 
		token, 
		req.SourceCode, 
		req.LanguageID, 
		nilIfEmpty(req.Stdin), 
		nilIfEmpty(req.ExpectedOutput), 
		nilIf0Float(req.CpuTimeLimit), 
		nilIf0Int(req.MemoryLimit), 
		req.CallbackUrl,
	)

	if err != nil {
		log.Printf("[Engine] Database insert failed for async job: %v\n", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to save submission"})
	}

	job := queue.Job{
		Token:   token,
		Request: req,
	}

	// Push to global redis queue
	err = h.Engine.Broker.EnqueueGlobal(c.Context(), job)
	if err != nil {
		// Enqueue failed, rollback DB insert
		_, _ = h.DB.Exec(`DELETE FROM submissions WHERE token = $1`, token)
		log.Printf("[Engine] ERROR: Global queue push failed: %v\n", err)
		
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Execution broker is unavailable. Try again later.",
		})
	}

	// Job successfully queued
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"token": token})
}

// CreateBatchSubmission handles async array submissions
func (h *SubmissionHandler) CreateBatchSubmission(c *fiber.Ctx) error {
	var req models.BatchRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid JSON payload"})
	}

	if len(req.Submissions) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Submissions array cannot be empty"})
	}

	var responses []models.BatchResponseItem
	var jobsToQueue []queue.Job 

	tx, err := h.DB.Begin()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to start database transaction"})
	}

	insertQuery := `
		INSERT INTO submissions 
		(token, source_code, language_id, stdin, expected_output, cpu_time_limit, memory_limit, callback_url, status_id, status_description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 'In Queue')
	`
	
	stmt, err := tx.Prepare(insertQuery)
	if err != nil {
		tx.Rollback()
		return c.Status(500).JSON(fiber.Map{"error": "Failed to prepare query"})
	}
	defer stmt.Close()

	for _, sub := range req.Submissions {
		token := uuid.New().String()
		responses = append(responses, models.BatchResponseItem{Token: token})

		_, err := stmt.Exec(
			token, 
			sub.SourceCode, 
			sub.LanguageID, 
			nilIfEmpty(sub.Stdin), 
			nilIfEmpty(sub.ExpectedOutput), 
			nilIf0Float(sub.CpuTimeLimit), 
			nilIf0Int(sub.MemoryLimit), 
			sub.CallbackUrl,
		)
		
		if err != nil {
			tx.Rollback()
			return c.Status(500).JSON(fiber.Map{"error": "Database insert failed", "details": err.Error()})
		}

		jobsToQueue = append(jobsToQueue, queue.Job{
			Token:   token,
			Request: sub,
		})
	}

	if err := tx.Commit(); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to commit transaction"})
	}

	// 3. Push batch to Global Redis Queue
	for _, job := range jobsToQueue {
		if err := h.Engine.Broker.EnqueueGlobal(c.Context(), job); err != nil {
			log.Printf("[Engine Error] Failed to push token %s to Redis: %v", job.Token, err)
		}
	}

	return c.Status(201).JSON(responses)
}

// // CreateAsyncSubmission puts data in DB, creates a Job, and queues it
// func (h *SubmissionHandler) CreateAsyncSubmission(c *fiber.Ctx) error {
// 	var req models.Judge0Request
// 	if err := c.BodyParser(&req); err != nil {
// 		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
// 			"error": "Invalid request payload",
// 		})
// 	}

// 	token := uuid.New().String()

// 	// 1. Insert into DB (status_id = 1 -> "In Queue")
// 	_, err := h.DB.Exec(`
// 		INSERT INTO submissions 
// 		(token, source_code, language_id, stdin, expected_output, cpu_time_limit, memory_limit, callback_url, status_id, status_description)
// 		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 'In Queue')
// 	`, 
// 		token, 
// 		req.SourceCode, 
// 		req.LanguageID, 
// 		nilIfEmpty(req.Stdin), 
// 		nilIfEmpty(req.ExpectedOutput), 
// 		nilIf0Float(req.CpuTimeLimit), 
// 		nilIf0Int(req.MemoryLimit), 
// 		req.CallbackUrl, // already a ptr
// 	)

// 	if err != nil {
// 		log.Printf("[Engine] Database insert failed for async job: %v\n", err)
// 		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to save submission"})
// 	}

// 	job := queue.Job{
// 		Token:   token,
// 		Request: req,
// 	}

// 	err = h.Engine.JobQueue.Enqueue(job)
// 	if err != nil {
// 		// Queue is full, revert action.
// 		_, _ = h.DB.Exec(`DELETE FROM submissions WHERE token = $1`, token)
// 		log.Println("[Engine] WARNING: Job queue at maximum capacity. Submission rejected.")
		
// 		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
// 			"error": "Execution queue is full. Try again later.",
// 		})
// 	}

// 	// Job successfully queued
// 	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"token": token})
// }

// func (h *SubmissionHandler) CreateBatchSubmission(c *fiber.Ctx) error {
// 	var req models.BatchRequest
// 	if err := c.BodyParser(&req); err != nil {
// 		return c.Status(400).JSON(fiber.Map{"error": "Invalid JSON payload"})
// 	}

// 	if len(req.Submissions) == 0 {
// 		return c.Status(400).JSON(fiber.Map{"error": "Submissions array cannot be empty"})
// 	}

// 	var responses []models.BatchResponseItem
// 	var jobsToQueue []queue.Job 

// 	tx, err := h.DB.Begin()
// 	if err != nil {
// 		return c.Status(500).JSON(fiber.Map{"error": "Failed to start database transaction"})
// 	}

// 	insertQuery := `
// 		INSERT INTO submissions 
// 		(token, source_code, language_id, stdin, expected_output, cpu_time_limit, memory_limit, callback_url, status_id, status_description)
// 		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 'In Queue')
// 	`
	
// 	stmt, err := tx.Prepare(insertQuery)
// 	if err != nil {
// 		tx.Rollback()
// 		return c.Status(500).JSON(fiber.Map{"error": "Failed to prepare query"})
// 	}
// 	defer stmt.Close()

// 	// insert all into db
// 	for _, sub := range req.Submissions {
// 		token := uuid.New().String()
// 		responses = append(responses, models.BatchResponseItem{Token: token})

// 		_, err := stmt.Exec(
// 			token, 
// 			sub.SourceCode, 
// 			sub.LanguageID, 
// 			nilIfEmpty(sub.Stdin), 
// 			nilIfEmpty(sub.ExpectedOutput), 
// 			nilIf0Float(sub.CpuTimeLimit), 
// 			nilIf0Int(sub.MemoryLimit), 
// 			sub.CallbackUrl,
// 		)
		
// 		if err != nil {
// 			tx.Rollback() // If one fails, abort the whole batch
// 			return c.Status(500).JSON(fiber.Map{"error": "Database insert failed", "details": err.Error()})
// 		}

// 		jobsToQueue = append(jobsToQueue, queue.Job{
// 			Token:   token,
// 			Request: sub,
// 		})
// 	}

// 	// Commit txn
// 	if err := tx.Commit(); err != nil {
// 		return c.Status(500).JSON(fiber.Map{"error": "Failed to commit transaction"})
// 	}

// 	// push them to async worker queue
// 	for _, job := range jobsToQueue {
// 		h.Engine.JobQueue.Enqueue(job)
// 	}

// 	//  return array of token objects
// 	return c.Status(201).JSON(responses)
// }

// // GetSubmission handles single polling: GET /submissions/:token
func (h *SubmissionHandler) GetSubmission(c *fiber.Ctx) error {
	token := c.Params("token")

	var statusID int
	var statusDesc string
	var compileOutput, stdout, stderr *string
	var time, memory float64

	query := `
		SELECT status_id, status_description, compile_output, stdout, stderr, time, memory 
		FROM submissions 
		WHERE token = $1
	`
	err := h.DB.QueryRow(query, token).Scan(
		&statusID, &statusDesc, &compileOutput, &stdout, &stderr, &time, &memory,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return c.Status(404).JSON(fiber.Map{"error": "Submission not found"})
		}
		return c.Status(500).JSON(fiber.Map{"error": "Database error", "details": err.Error()})
	}

	return c.JSON(fiber.Map{
		"token":          token,
		"status":         fiber.Map{"id": statusID, "description": statusDesc},
		"compile_output": compileOutput,
		"stdout":         stdout,
		"stderr":         stderr,
		"time":           time,
		"memory":         memory,
	})
}

// GetBatchSubmissions handles multiple tokens: GET /submissions/batch?tokens=t1,t2
func (h *SubmissionHandler) GetBatchSubmissions(c *fiber.Ctx) error {
	tokensStr := c.Query("tokens")
	if tokensStr == "" {
		return c.Status(400).JSON(fiber.Map{"error": "tokens query parameter is required"})
	}

	tokens := strings.Split(tokensStr, ",")

	query := `
		SELECT token, status_id, status_description, compile_output, stdout, stderr, time, memory 
		FROM submissions 
		WHERE token = ANY($1)
	`
	
	rows, err := h.DB.Query(query, pq.Array(tokens))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Database query failed", "details": err.Error()})
	}
	defer rows.Close()

	var results []fiber.Map

	for rows.Next() {
		var t, statusDesc string
		var statusID int
		var compileOutput, stdout, stderr *string
		var time, memory float64

		if err := rows.Scan(&t, &statusID, &statusDesc, &compileOutput, &stdout, &stderr, &time, &memory); err != nil {
			continue // Skip corrupted rows
		}

		results = append(results, fiber.Map{
			"token":          t,
			"status":         fiber.Map{"id": statusID, "description": statusDesc},
			"compile_output": compileOutput,
			"stdout":         stdout,
			"stderr":         stderr,
			"time":           time,
			"memory":         memory,
		})
	}

	// Judge0 batch format returns an object with a "submissions" array
	return c.JSON(fiber.Map{
		"submissions": results,
	})
}