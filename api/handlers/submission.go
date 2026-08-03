package handlers

import (
	"encoding/base64"
	"log"
	"strings"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
	"RUNE/internal/executor"
	"RUNE/internal/models"
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

	// TODO: Replace this hardcoded mapping with a proper LanguageConfig
	var sourceFileName, executableName string
	if req.LanguageID == 54 { // Judge0 C++ Language ID...
		sourceFileName = "main.cpp"
		executableName = "prog"
	} else {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Unsupported language ID"})
	}

	// Write source code and stdin to the sandbox
	if err := box.Write(sourceFileName, req.SourceCode); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to write source code"})
	}
	if req.Stdin != "" {
		if err := box.Write("stdin.txt", req.Stdin); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to write stdin"})
		}
	}

	// Compile
	compileOutput, err := box.Compile(sourceFileName, executableName)
	if err != nil {
		// Compilation Error Status Code in Judge0 is 6
		return c.Status(fiber.StatusCreated).JSON(models.Judge0Response{
			Status: models.Status{ID: 6, Description: "Compilation Error"},
			CompileOutput: &compileOutput,
		})
	}

	// Run
	runOutput, err := box.Run(executableName) // todo: make Run() support stdin inputs
	
	
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

	// Base64 encode the output if ?base64_encoded=true
	if isBase64 {
		encodedStdout := base64.StdEncoding.EncodeToString([]byte(runOutput))
		runOutput = encodedStdout
	}
	
	res := models.Judge0Response{
		Stdout: &runOutput,
		Time:   metaData.Time,            // execution time in seconds
		Memory: float64(metaData.Memory), // memory usage in KB
		Status: status,
	}

	return c.Status(fiber.StatusCreated).JSON(res)
}