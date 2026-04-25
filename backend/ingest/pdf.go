package ingest

import (
	"bytes"
	"os/exec"
	"strings"
)

// ExtractedPDF holds the result of PDF text extraction
type ExtractedPDF struct {
	FullText string
	Pages    []string
}

// ExtractPDFText extracts text content from a PDF file using pdftotext
func ExtractPDFText(filePath string) (*ExtractedPDF, error) {
	// Use pdftotext with -layout flag to preserve formatting
	cmd := exec.Command("pdftotext", "-layout", filePath, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	fullText := stdout.String()
	if fullText == "" {
		return nil, nil
	}

	// Split by page breaks (pdftotext uses \f for page breaks)
	pages := strings.Split(fullText, "\f")

	// Clean each page
	var cleanedPages []string
	for _, page := range pages {
		cleaned := CleanPDFText(page)
		if cleaned != "" {
			cleanedPages = append(cleanedPages, cleaned)
		}
	}

	return &ExtractedPDF{
		FullText: strings.Join(cleanedPages, "\n\n--- Page Break ---\n\n"),
		Pages:    cleanedPages,
	}, nil
}

// CleanPDFText removes common PDF artifacts like page numbers and headers/footers
func CleanPDFText(text string) string {
	// Remove page headers/footers (simple heuristic: skip short numeric-only lines)
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			cleaned = append(cleaned, line)
			continue
		}
		// Skip page number lines (short numeric strings)
		if len(line) < 5 && isNumeric(line) {
			continue
		}
		// Keep content
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

// isNumeric checks if a string contains only numeric characters
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
