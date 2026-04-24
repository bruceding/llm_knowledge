package ingest

import (
	"strings"

	"github.com/ledongthuc/pdf"
)

// ExtractedPDF holds the result of PDF text extraction
type ExtractedPDF struct {
	FullText string
	Pages    []string
}

// ExtractPDFText extracts text content from a PDF file
func ExtractPDFText(filePath string) (*ExtractedPDF, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pages []string
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, _ := p.GetPlainText(nil)
		pages = append(pages, text)
	}

	return &ExtractedPDF{
		FullText: strings.Join(pages, "\n\n"),
		Pages:    pages,
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