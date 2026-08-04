package models


// UNUSED FILE.
type LanguageConfig struct {
	ID         int
	IsCompiled bool
	CompileCmd []string
	RunCmd     []string
}

var SupportedLanguages = map[int]LanguageConfig{
	54: { // C++
		ID:         54,
		IsCompiled: true,
		CompileCmd: []string{"/usr/bin/g++", "-O2", "-std=c++17", "{source}", "-o", "{output}"},
		RunCmd:     []string{"./{output}"},
	},
	71: { // Python 3
		ID:         71,
		IsCompiled: false,
		CompileCmd: nil, // No compilation
		RunCmd:     []string{"/usr/bin/python3", "{source}"},
	},
}