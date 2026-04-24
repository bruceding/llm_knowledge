package ingest

import (
	"os"
	"testing"
)

func TestExtractPDFText(t *testing.T) {
	// Check if test file exists
	testFile := "testdata/sample.pdf"
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skip("Skipping test: testdata/sample.pdf not found")
	}

	result, err := ExtractPDFText(testFile)
	if err != nil {
		t.Fatalf("ExtractPDFText failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Note: minimal PDFs may not have extractable text
	// This test mainly verifies the function doesn't crash
}

func TestExtractPDFTextInvalidFile(t *testing.T) {
	_, err := ExtractPDFText("nonexistent.pdf")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}

func TestCleanPDFText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "removes page numbers",
			input:    "1\nContent here\n2\nMore content",
			expected: "Content here\nMore content",
		},
		{
			name:     "preserves normal content",
			input:    "This is a normal paragraph\nwith multiple lines.",
			expected: "This is a normal paragraph\nwith multiple lines.",
		},
		{
			name:     "removes short numeric lines",
			input:    "123\nReal content here\n456",
			expected: "Real content here",
		},
		{
			name:     "preserves longer numbers",
			input:    "Reference: 12345\nSome text",
			expected: "Reference: 12345\nSome text",
		},
		{
			name:     "preserves empty lines",
			input:    "Line one\n\nLine two",
			expected: "Line one\n\nLine two",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanPDFText(tt.input)
			if result != tt.expected {
				t.Errorf("CleanPDFText(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"123", true},
		{"0", true},
		{"abc", false},
		{"12a", false},
		{"", false},
		{" 123 ", false},
		{"-123", false},
		{"12.34", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isNumeric(tt.input)
			if result != tt.expected {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}