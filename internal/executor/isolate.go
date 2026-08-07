package executor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const IsolateCmd = "isolate"

// Sandbox represents a single isolate environment
type Sandbox struct {
	BoxID int    // sandbox id
	Path  string // sanbox path
}

type ExecutionMeta struct {
	Time    float64 // Execution time in seconds
	Memory  int     // Memory used in kilobytes (from max-rss) ==> has around 3.5 MB of extra overhead from experimentation on my end :)
	Status  string  // "RE", "SG", "TO", "XX", or "" for Accepted
	Message string  // Additional error details
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

// Cleanup to destroy the sandbox
func (s *Sandbox) Cleanup() error {
	cmd := exec.Command(IsolateCmd, "--cg", fmt.Sprintf("--box-id=%d", s.BoxID), "--cleanup")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to cleanup sandbox %d: %v", s.BoxID, err)
	}
	return nil
}

// ==========================================
// C++ Execution
// ==========================================

func (s *Sandbox) CompileCPP(sourceFile, outputFile string) (string, error) {
	cmd := exec.Command(IsolateCmd,
		"--cg",
		fmt.Sprintf("--box-id=%d", s.BoxID),
		"--env=PATH=/usr/bin:/bin",
		"--processes",    // BUG FIX ==>> Allow g++ to fork child processes like cc1plus, as, ld
		"--wall-time=10", // max to 10 seconds to compile
		"--run",
		"--",
		"/usr/bin/g++", "-O2", "-std=c++20", sourceFile, "-o", outputFile,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("compilation failed: %v", err)
	}
	return string(output), nil
}

func (s *Sandbox) RunCPP(executableName string, timeLimit float64, memoryLimit float64, hasStdin bool) (string, string, error) {
	maxProcessLim := 1
	return s.executeBinary(executableName, timeLimit, memoryLimit, hasStdin, []string{executableName}, maxProcessLim)
}

// ==========================================
// Python Execution
// ==========================================

func (s *Sandbox) CompilePython(sourceFile string) (string, error) {
	// No-op for Python
	return "", nil
}

func (s *Sandbox) RunPython(sourceFile string, timeLimit float64, memoryLimit float64, hasStdin bool) (string, string, error) {
	runArgs := []string{"/usr/bin/python3", sourceFile}
	maxProcessLim := 1
	return s.executeBinary(sourceFile, timeLimit, memoryLimit, hasStdin, runArgs, maxProcessLim)
}

// ==========================================
// Java Execution
// ==========================================

func (s *Sandbox) CompileJava(sourceFile string) (string, error) {
	cmd := exec.Command(IsolateCmd,
		"--cg",
		fmt.Sprintf("--box-id=%d", s.BoxID),
		"--env=PATH=/usr/bin:/bin",
		"--dir=/etc/alternatives", 
		"--dir=/etc/java-21-openjdk",
        "--dir=/usr/lib/jvm",
		"--processes",
		//"--cg-mem=512000",
		"--wall-time=10",
		"--run",
		"--",
		"/usr/bin/javac", sourceFile,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("compilation failed: %v", err)
	}
	return string(output), nil
}

func (s *Sandbox) RunJava(className string, timeLimit float64, memoryLimit float64, hasStdin bool) (string, string, error) {
maxHeapMB := int(memoryLimit / 1024)
    if maxHeapMB < 64 {
        maxHeapMB = 64 // min allocation for JVM
    }

    runCmd := []string{
        "/usr/bin/java",
        "-Xms64m",
        fmt.Sprintf("-Xmx%dm", maxHeapMB),
        className,
    }

	maxProcessLim := 128
    return s.executeBinary(className, timeLimit, memoryLimit, hasStdin, runCmd, maxProcessLim)
}

// ==========================================
// JavaScript (Node.js) Execution
// ==========================================

func (s *Sandbox) CompileJS(sourceFile string) (string, error) {
	// No-op for JS
	return "", nil
}

func (s *Sandbox) RunJS(sourceFile string, timeLimit float64, memoryLimit float64, hasStdin bool) (string, string, error) {
	runArgs := []string{"/usr/bin/node", sourceFile}
	maxProcessLim := 64
	return s.executeBinary(sourceFile, timeLimit, memoryLimit, hasStdin, runArgs, maxProcessLim)
}


// executeBinary handles the boilerplate isolate arguments for all languages
func (s *Sandbox) executeBinary(target string, timeLimit float64, memoryLimit float64, hasStdin bool, runCmd []string, maxProcessLim int) (string, string, error) {
	metaPath := filepath.Join(s.Path, "meta.txt")
	stdoutFile := "run.out"
	stderrFile := "run.err"
	args := []string{
		fmt.Sprintf("--box-id=%d", s.BoxID),
		"--cg",
		fmt.Sprintf("--time=%f", timeLimit),
		fmt.Sprintf("--wall-time=%f", timeLimit+10.0),
		fmt.Sprintf("--cg-mem=%d", int(memoryLimit+65536.0)),
		fmt.Sprintf("--meta=%s", metaPath),
		fmt.Sprintf("--processes=%d", maxProcessLim),
		"--dir=/etc/alternatives",
        "--dir=/usr/lib/jvm",
		"--dir=/etc/java-21-openjdk",
		fmt.Sprintf("--stdout=%s", stdoutFile),
		fmt.Sprintf("--stderr=%s", stderrFile),
		"--env=PATH=/usr/bin:/bin",
	}

	if hasStdin {
		args = append(args, "--stdin=stdin.txt")
	}

	args = append(args, "--run", "--")
	args = append(args, runCmd...)

	cmd := exec.Command(IsolateCmd, args...)
	var isolateStderr bytes.Buffer
	cmd.Stderr = &isolateStderr

	err := cmd.Run()
	if err != nil {
		fmt.Printf("[Sandbox %d] Isolate command failed: %v\nIsolate Stderr: %s\n", s.BoxID, err, isolateStderr.String())
	}

	outPath := filepath.Join(s.Path, stdoutFile)
	cleanStdout, readErr := os.ReadFile(outPath)
	errPath := filepath.Join(s.Path, stderrFile)
	cleanStderr, _ := os.ReadFile(errPath)

	if readErr != nil {
		return "", "", fmt.Errorf("failed to read stdout: %v (execution error: %v)", readErr, err)
	}

	return string(cleanStdout), string(cleanStderr), err
}

// ==========================================
// Meta Parsing
// ==========================================

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
			// fmt.Print(value)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading meta file: %w", err)
	}

	return meta, nil
}