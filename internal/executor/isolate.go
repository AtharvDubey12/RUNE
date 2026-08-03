package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"strconv"
	"bufio"
)

const IsolateCmd = "isolate"

// Sandbox represents a single isolate environment
type Sandbox struct {
	BoxID int	// sandbox id
	Path  string // sanbox path
}

type ExecutionMeta struct {
	Time     float64 // Execution time in seconds
	Memory   int     // Memory used in kilobytes (from max-rss) ==> has around 3.5 MB of extra overhead from experimentation on my end :)
	Status   string  // "RE", "SG", "TO", "XX", or "" for Accepted
	Message  string  // Additional error details
}

func NewSandbox(boxID int) *Sandbox {
	return &Sandbox{
		BoxID: boxID,
		Path:  fmt.Sprintf("/var/local/lib/isolate/%d/box", boxID), 
	}
}

// Init creates a new sandbox, resetting it if it already exists
func (s *Sandbox) Init() error {
	cmd := exec.Command(IsolateCmd, "--cg", fmt.Sprintf("--box-id=%d", s.BoxID), "--init")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to init sandbox %d: %v", s.BoxID, err)
	}
	return nil
}

// Write writes the submitted code to the sandbox directory
func (s *Sandbox) Write(filename string, code string) error {
	path := fmt.Sprintf("/var/local/lib/isolate/%d/box/%s", s.BoxID, filename)
	return os.WriteFile(path, []byte(code), 0644)
}

// Compile compiles the source code inside the sandbox using g++, currently only cpp is supported.
// TODO: Add an abstraction over Compile and make it support multi lang.
func (s *Sandbox) Compile(sourceFile, outputFile string) (string, error) {
	// Execute g++ inside the sandbox environment
	cmd := exec.Command(IsolateCmd, 
		"--cg", 
		fmt.Sprintf("--box-id=%d", s.BoxID),
		"--env=PATH=/usr/bin:/bin", 
		"--processes",    // BUG FIX ==>> Allow g++ to fork child processes like cc1plus, as, ld (will crash without it.)
		"--mem=256000",   // allot compiler 256MB of RAM [Magic Number as of now, TODO: Experiment with ideal size]
		"--wall-time=10", // max to 10 seconds to compile [Magic Number again.]
		"--run", 
		"--", 
		"/usr/bin/g++", "-O3", "-std=c++17", sourceFile, "-o", outputFile,
	)
	
	output, err := cmd.CombinedOutput() 
	if err != nil { 
		return string(output), fmt.Errorf("compilation failed: %v", err)
	}
	return string(output), nil
}

// Run executes the compiled binary inside the sandbox
func (s *Sandbox) Run(executableName string) (string, error) {
	metaPath := filepath.Join(s.Path, "meta.txt")
	stdoutFile := "run.out"
	stderrFile := "run.err"

	cmd := exec.Command(
		"isolate",
		fmt.Sprintf("--box-id=%d", s.BoxID),
		"--cg",
		fmt.Sprintf("--meta=%s", metaPath),
		fmt.Sprintf("--stdout=%s", stdoutFile),
		fmt.Sprintf("--stderr=%s", stderrFile),
		"--run",
		"--",
		executableName,
	)

	err := cmd.Run()

	outPath := filepath.Join(s.Path, stdoutFile)
	cleanStdout, readErr := os.ReadFile(outPath)
	if readErr != nil {
		return "", fmt.Errorf("failed to read stdout: %v (execution error: %v)", readErr, err)
	}

	return string(cleanStdout), err
}

// Cleanup to destroy the sandbox
func (s *Sandbox) Cleanup() error {
	cmd := exec.Command(IsolateCmd, "--cg", fmt.Sprintf("--box-id=%d", s.BoxID), "--cleanup")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to cleanup sandbox %d: %v", s.BoxID, err)
	}
	return nil
}

// to parse meta.txt for time and memory data
func ParseMetaFile(metaPath string) (*ExecutionMeta, error) {
	file, err := os.Open(metaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open meta file: %w", err)
	}
	defer file.Close()

	meta := &ExecutionMeta{}
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		switch key {
		case "time":
			meta.Time, _ = strconv.ParseFloat(value, 64)
		case "max-rss":
			meta.Memory, _ = strconv.Atoi(value)
		case "status":
			meta.Status = value
		case "message":
			meta.Message = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading meta file: %w", err)
	}

	return meta, nil
}