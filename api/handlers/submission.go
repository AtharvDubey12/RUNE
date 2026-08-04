package handlers

import (
	"encoding/base64"
	"log"
	"path/filepath"
	"strings"

	"RUNE/internal/executor"
	"RUNE/internal/models"
	"github.com/gofiber/fiber/v2"
)

// Initialize the 50-box pool for the handlers package ==> 50 concurrent sandboxes [todo: find optimal number]
var boxPool = executor.NewBoxManager(50)

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
func CreateSyncSubmission(c *fiber.Ctx) error {
	if c.Query("wait") != "true" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "This RUNE route only supports synchronous execution. Ensure ?wait=true is set.",
		})
	}

	var req models.Judge0Request
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}

	isBase64 := true
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
	boxID := boxPool.Acquire()
	defer boxPool.Release(boxID)

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