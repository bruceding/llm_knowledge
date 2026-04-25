package ingest

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CleanWikiForDocument removes wiki content related to a deleted document
// It scans entities and topics, removing only those whose sole Source is this document
func CleanWikiForDocument(wikiDir string, docName string) error {
	log.Printf("[cleanwiki] Cleaning wiki for document: %s", docName)

	// Pattern to match Source references
	sourcePattern := regexp.MustCompile(`Source:\s*\[([^\]]+)\]\([^)]+\)`)

	// 1. Clean entities directory
	entitiesDir := filepath.Join(wikiDir, "entities")
	if err := cleanDirectory(entitiesDir, docName, sourcePattern, "entity"); err != nil {
		log.Printf("[cleanwiki] Error cleaning entities: %v", err)
	}

	// 2. Clean topics directory
	topicsDir := filepath.Join(wikiDir, "topics")
	if err := cleanDirectory(topicsDir, docName, sourcePattern, "topic"); err != nil {
		log.Printf("[cleanwiki] Error cleaning topics: %v", err)
	}

	// 3. Remove source document page from wiki root (e.g., wiki/DeepSeek_V4.md)
	sourcePage := filepath.Join(wikiDir, docName+".md")
	if _, err := os.Stat(sourcePage); err == nil {
		os.Remove(sourcePage)
		log.Printf("[cleanwiki] Removed source page: %s", sourcePage)
	}

	// 4. Update index files
	if err := updateIndexFiles(wikiDir, docName); err != nil {
		log.Printf("[cleanwiki] Error updating index files: %v", err)
	}

	return nil
}

// cleanDirectory scans a directory and removes files whose sole Source is the deleted document
func cleanDirectory(dir string, docName string, sourcePattern *regexp.Regexp, itemType string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".md") {
			filePath := filepath.Join(dir, file.Name())
			content, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			// Find all Source references
			matches := sourcePattern.FindAllStringSubmatch(string(content), -1)
			if len(matches) == 0 {
				continue
			}

			// Extract source document names
			sources := []string{}
			for _, match := range matches {
				if len(match) > 1 {
					sources = append(sources, match[1])
				}
			}

			// Remove if the only source is this document
			if len(sources) == 1 && sources[0] == docName {
				os.Remove(filePath)
				log.Printf("[cleanwiki] Removed %s: %s (sole source was %s)", itemType, file.Name(), docName)
			} else if contains(sources, docName) {
				// Document is referenced but shared with others - update to remove reference
				removeSourceReference(filePath, docName)
				log.Printf("[cleanwiki] Updated %s: %s (removed reference to %s, kept other sources)", itemType, file.Name(), docName)
			}
		}
	}

	return nil
}

// contains checks if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// removeSourceReference removes the Source reference to a specific document from a file
func removeSourceReference(filePath string, docName string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	newLines := []string{}
	skipNext := false

	for _, line := range lines {
		// Skip lines that reference the deleted document
		if skipNext {
			skipNext = false
			continue
		}

		// Check for Source reference line
		if strings.Contains(line, "Source:") && strings.Contains(line, docName) {
			// Check if this is a multi-source line or single source
			if strings.Contains(line, fmt.Sprintf("Source: [%s]", docName)) {
				// Single source to this document - remove the whole Related section entry
				continue
			}
		}

		// Check for Related section entries that might be in bullet list format
		if strings.HasPrefix(line, "- Source:") && strings.Contains(line, docName) {
			continue
		}

		// Keep other lines
		newLines = append(newLines, line)
	}

	// Write back the updated content
	return os.WriteFile(filePath, []byte(strings.Join(newLines, "\n")), 0644)
}

// updateIndexFiles removes references to the deleted document from index.md, sources.md, entities.md, topics.md
func updateIndexFiles(wikiDir string, docName string) error {
	indexFiles := []string{
		filepath.Join(wikiDir, "index.md"),
		filepath.Join(wikiDir, "sources.md"),
		filepath.Join(wikiDir, "entities.md"),
		filepath.Join(wikiDir, "topics.md"),
	}

	linkPattern := regexp.MustCompile(fmt.Sprintf(`-?\s*\[([^\]]*\b%s\b[^\]]*)\]\([^)]+\)[^\n]*`, docName))

	for _, indexPath := range indexFiles {
		if _, err := os.Stat(indexPath); err != nil {
			continue
		}

		content, err := os.ReadFile(indexPath)
		if err != nil {
			continue
		}

		// Remove lines containing links to the deleted document
		lines := strings.Split(string(content), "\n")
		newLines := []string{}

		for _, line := range lines {
			// Skip lines that link to the deleted document
			if linkPattern.MatchString(line) {
				continue
			}
			// Also check for simpler pattern: document name in link
			if strings.Contains(line, docName+".md") || strings.Contains(line, docName+"]") {
				// Check if this is a bullet item linking to the document
				if strings.HasPrefix(strings.TrimSpace(line), "-") && strings.Contains(line, "["+docName+"]") {
					continue
				}
			}
			newLines = append(newLines, line)
		}

		// Write back
		if err := os.WriteFile(indexPath, []byte(strings.Join(newLines, "\n")), 0644); err != nil {
			log.Printf("[cleanwiki] Error updating %s: %v", indexPath, err)
		} else {
			log.Printf("[cleanwiki] Updated index: %s", indexPath)
		}
	}

	return nil
}