package models

/*
RUNE is a drop in replacement for Judge0 with support for 4 languages only (C++20, JavaScript, Python3, Java 21) to avoid excessive memory bloat.
Thus, RUNE expects input and produces an output payload identical to that of Judge0.
This file contains the input and output structure of Judge0 payloads.

Judge0 Documentation: https://ce.judge0.com/ 
*/

// Judge0Request incoming JSON payload
type Judge0Request struct {
	SourceCode     string  `json:"source_code"`
	LanguageID     int     `json:"language_id"`
	Stdin          string  `json:"stdin"`
	ExpectedOutput string  `json:"expected_output"`
	CpuTimeLimit   float64 `json:"cpu_time_limit"`
	MemoryLimit    int     `json:"memory_limit"`
	Base64Encoded  *bool   `json:"base64_encoded"` // Pointer to handle defaults
}

// Judge0Response mimics the synchronous return payload
type Judge0Response struct {
	Stdout        *string `json:"stdout"`
	Stderr        *string `json:"stderr"`
	CompileOutput *string `json:"compile_output"`
	Time          float64 `json:"time"`
	Memory        float64 `json:"memory"`
	Status        Status  `json:"status"`
}

type Status struct {
	ID          int    `json:"id"`
	Description string `json:"description"`
}