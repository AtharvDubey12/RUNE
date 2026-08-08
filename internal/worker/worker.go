package worker

import (
	"database/sql"
	"log"
	"strings"
	"encoding/base64"
	"github.com/gofiber/fiber/v2"
	"RUNE/internal/executor"
	"RUNE/internal/queue"
	"RUNE/internal/models"
	"path/filepath"
	"encoding/json"
	"fmt"
)

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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


// func RunDedicatedBox(ctx context.Context, dbConn *sql.DB, redisBroker *broker.Broker, boxID int, workerID string) {
// 	log.Printf("[Worker] Box %d online and listening to global queue...", boxID)

// 	for {
		
// 		job, err := redisBroker.PopGlobal(ctx)
// 		if err != nil {
// 			if ctx.Err() != nil { return } // Shutdown signal
// 			continue
// 		}
// 		// Claim in PostgreSQL
// 		if !redisBroker.ClaimJobInDB(dbConn, job.Token, workerID) {
// 			continue // Another node grabbed it, loop back and BLPOP again
// 		}
// 		processJob(dbConn, *job, boxID)
// 	}
// }


// StartDispatcher continuously pulls jobs from the queue and assigns them to available boxes.
func StartDispatcher(dbConn *sql.DB, jobQueue *queue.EngineQueue, boxManager *executor.BoxManager, nodeCapacity chan struct{}) {
	log.Println("[Dispatcher] Starting worker dispatcher...")
	
	for {
		job, ok := jobQueue.Dequeue()
		if !ok {
			log.Println("[Dispatcher] Failed to dequeue job or queue closed.")
			return 
		}

		boxID := boxManager.Acquire()

		go func(j queue.Job, id int) {
			// Release the box back to the BoxManager pool
			defer boxManager.Release(id)
			
			if nodeCapacity != nil { // for standalone non redis version
				// Refund the capacity ticket back to the Poller
				defer func() { <-nodeCapacity }()
			}
			
			processJob(dbConn, j, id)
		}(job, boxID)
	}
}

func processJob(dbConn *sql.DB, job queue.Job, boxID int) {
	req := job.Request
	token := job.Token

	log.Printf("[Worker] Box %d picked up job %s", boxID, token)

	// Mark as Processing (Status 2) in Database
	updateDB(dbConn, token, models.Judge0Response{
		Status: models.Status{ID: 2, Description: "Processing"},
	})

	isBase64 := false
	if req.Base64Encoded != nil {
		isBase64 = *req.Base64Encoded
	}

	// Decode inputs
	sourceCode := req.SourceCode
	stdin := req.Stdin
	if isBase64 {
		var err error
		sourceCode, err = decodeBase64Str(req.SourceCode)
		if err != nil {
			failJob(dbConn, token, req, 13, "Internal Error - Invalid base64 in source_code")
			return
		}
		stdin, err = decodeBase64Str(req.Stdin)
		if err != nil {
			failJob(dbConn, token, req, 13, "Internal Error - Invalid base64 in stdin")
			return
		}
	}

	// Setup Sandbox Paths
	var sourceFileName, executableName string
	switch req.LanguageID {
	case 54:
		sourceFileName, executableName = "main.cpp", "prog"
	case 71:
		sourceFileName, executableName = "main.py", "main.py"
	case 62:
		sourceFileName, executableName = "Main.java", "Main"
	case 63:
		sourceFileName, executableName = "main.js", "main.js"
	default:
		failJob(dbConn, token, req, 13, "Internal Error - Unsupported language ID")
		return
	}

	box := executor.NewSandbox(boxID)
	if err := box.Init(); err != nil {
		failJob(dbConn, token, req, 13, "Internal Error - Sandbox initialization failed")
		return
	}
	defer func() {
		if err := box.Cleanup(); err != nil {
			log.Printf("Cleanup failed for box %d: %v", boxID, err)
		}
	}()

	if err := box.Write(sourceFileName, sourceCode); err != nil {
		failJob(dbConn, token, req, 13, "Internal Error - Failed to write source code")
		return
	}

	hasStdin := false
	if stdin != "" {
		if err := box.Write("stdin.txt", stdin); err != nil {
			failJob(dbConn, token, req, 13, "Internal Error - Failed to write stdin")
			return
		}
		hasStdin = true
	}

	// Compile
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
    	finalCompileOutput := compileOutput
    	if isBase64 {
        	finalCompileOutput = base64.StdEncoding.EncodeToString([]byte(compileOutput))
    	}

    	finalizeJob(dbConn, token, req, models.Judge0Response{
        	Status:        models.Status{ID: 6, Description: "Compilation Error"},
        	CompileOutput: &finalCompileOutput,
    	})
    	return
	}

	// Config Limits
	timeLimit := 2.0
	if req.CpuTimeLimit > 0 {
		timeLimit = float64(req.CpuTimeLimit)
	}
	memoryLimit := 262144.0
	if req.MemoryLimit > 0 {
		memoryLimit = float64(req.MemoryLimit)
	}

	// Execute
	var runOutput, runStderr string
	var runErr error
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

	// Parse Execution Meta
	metaPath := filepath.Join(box.Path, "meta.txt")
	metaData, metaErr := executor.ParseMetaFile(metaPath)
	if metaErr != nil {
		failJob(dbConn, token, req, 13, "Internal Error - Failed to parse execution metrics")
		return
	}

	// Determine Result Status
	statusID := 3
	statusDesc := "Accepted"

	if metaData.Status == "TO" {
		statusID, statusDesc = 5, "Time Limit Exceeded"
	} else if metaData.Status == "RE" || metaData.Status == "SG" {
		statusID, statusDesc = 11, "Runtime Error (NZEC)"
	} else if float64(metaData.Memory) > memoryLimit {
		statusID, statusDesc = 11, "Memory Limit Exceeded"
	} else if runErr != nil && statusID == 3 {
		statusID, statusDesc = 13, "Internal Error"
	}

	// Cmp Output if Accepted
	if statusID == 3 && strings.TrimSpace(req.ExpectedOutput) != "" {
		expectedOut := req.ExpectedOutput
		if isBase64 {
			if decodedExpected, err := decodeBase64Str(req.ExpectedOutput); err == nil {
				expectedOut = decodedExpected
			}
		}

		if !compareOutputs(runOutput, expectedOut) {
			statusID, statusDesc = 4, "Wrong Answer"
		}
	}

	// Encode Output
	if isBase64 {
		runOutput = base64.StdEncoding.EncodeToString([]byte(runOutput))
		if runStderr != "" {
			runStderr = base64.StdEncoding.EncodeToString([]byte(runStderr))
		}
	}


	finalResp := models.Judge0Response{
		Status: models.Status{ID: statusID, Description: statusDesc},
		Stdout: nilIfEmpty(runOutput),
		Stderr: nilIfEmpty(runStderr),
		Time:   metaData.Time,
		Memory: float64(metaData.Memory),
	}

	prettyJSON, err := json.MarshalIndent(finalResp, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling to JSON: %v\n", err)
	} else {
		fmt.Printf("Final Response:\n%s\n", string(prettyJSON))
	}

	finalizeJob(dbConn, token, req, finalResp)
	log.Printf("[Worker] Box %d finished job %s (Status: %s)", boxID, token, statusDesc)
}

//Helpers 

// failJob handles setup or infrastructure errors where execution didn't happen
func failJob(dbConn *sql.DB, token string, req models.Judge0Request, statusID int, description string) {
	finalizeJob(dbConn, token, req, models.Judge0Response{
		Status: models.Status{ID: statusID, Description: description},
	})
}

// finalizeJob updates the database and fires the fiber webhook
func finalizeJob(dbConn *sql.DB, token string, req models.Judge0Request, resp models.Judge0Response) {
	updateDB(dbConn, token, resp)

	if req.CallbackUrl != nil && *req.CallbackUrl != "" {
		// Fire webhook in a detached goroutine
		go func(url string, payload models.Judge0Response) {
			agent := fiber.Put(url)
			payloadWithToken := map[string]interface{}{
				"token":              token,
				"status":             payload.Status,
				"compile_output":     payload.CompileOutput,
				"stdout":             payload.Stdout,
				"stderr":             payload.Stderr,
				"time":               payload.Time,
				"memory":             payload.Memory,
			}

			agent.JSON(payloadWithToken)
			statusCode, _, errs := agent.Bytes()

			if len(errs) > 0 {
				log.Printf("[Callback Error] Request failed for %s: %v", token, errs[0])
				return
			}

			log.Printf("[Callback] Successfully hit webhook for %s (HTTP %d)", token, statusCode)
		}(*req.CallbackUrl, resp)
	}
}

// updateDB handles the SQL execution
func updateDB(dbConn *sql.DB, token string, resp models.Judge0Response) {
	query := `
		UPDATE submissions 
		SET status_id = $1, 
			status_description = $2,
			compile_output = COALESCE($3, compile_output),
			stdout = COALESCE($4, stdout), 
			stderr = COALESCE($5, stderr), 
			time = COALESCE($6, time), 
			memory = COALESCE($7, memory) 
		WHERE token = $8
	`

	_, err := dbConn.Exec(
		query,
		resp.Status.ID,
		resp.Status.Description,
		resp.CompileOutput,
		resp.Stdout,
		resp.Stderr,
		resp.Time,
		resp.Memory,
		token,
	)

	if err != nil {
		log.Printf("[DB Error] Failed to update token %s in database: %v", token, err)
	}
}
