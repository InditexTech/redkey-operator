// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNonEmptyLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "single line",
			input:    "line1",
			expected: []string{"line1"},
		},
		{
			name:     "multiple lines",
			input:    "line1\nline2\nline3",
			expected: []string{"line1", "line2", "line3"},
		},
		{
			name:     "lines with trailing newline",
			input:    "line1\nline2\n",
			expected: []string{"line1", "line2"},
		},
		{
			name:     "lines with multiple empty newlines",
			input:    "\nline1\n\nline2\n\n\n",
			expected: []string{"line1", "line2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := GetNonEmptyLines(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestGetProjectDir(t *testing.T) {
	originalWd, err := os.Getwd()
	assert.NoError(t, err)
	defer func() { _ = os.Chdir(originalWd) }()

	dir, err := GetProjectDir()
	assert.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.False(t, strings.Contains(dir, "/test/e2e"))
}

func TestUncommentCode(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-uncomment-*.go")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	content := "package main\n\n// func hello() {\n// \tprintln(\"hello\")\n// }\n"
	err = os.WriteFile(tmpFile.Name(), []byte(content), 0644)
	assert.NoError(t, err)

	target := "// func hello() {\n// \tprintln(\"hello\")\n// }"

	err = UncommentCode(tmpFile.Name(), target, "// ")
	assert.NoError(t, err)

	updatedContent, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)

	expectedStr := "package main\n\nfunc hello() {\n\tprintln(\"hello\")\n}\n"
	assert.Equal(t, expectedStr, string(updatedContent))

	// Error case: File not found
	err = UncommentCode("non-existent-file.txt", target, "// ")
	assert.Error(t, err)

	// Error case: Target not found
	err = UncommentCode(tmpFile.Name(), "foo-bar-baz", "// ")
	assert.Error(t, err)
}

func TestRun(t *testing.T) {
	cmd := exec.Command("echo", "hello-operator")
	output, err := Run(cmd)
	assert.NoError(t, err)
	assert.Contains(t, output, "hello-operator")

	badCmd := exec.Command("ls", "/does-not-exist-dir-12345")
	_, err = Run(badCmd)
	assert.Error(t, err)
}
