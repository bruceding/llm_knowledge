package ingest

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SyncIndexFiles scans wiki directories and rebuilds all index files deterministically.
// This ensures index.md, sources.md, entities.md, and topics.md stay in sync with actual files.
func SyncIndexFiles(wikiDir string) error {
	log.Printf("[sync] Starting index sync for wiki: %s", wikiDir)

	// 1. Scan sources directory
	sources, err := scanDirectory(filepath.Join(wikiDir, "sources"), "source")
	if err != nil {
		log.Printf("[sync] Error scanning sources: %v", err)
	}
	log.Printf("[sync] Found %d sources", len(sources))

	// 2. Scan entities directory
	entities, err := scanDirectory(filepath.Join(wikiDir, "entities"), "entity")
	if err != nil {
		log.Printf("[sync] Error scanning entities: %v", err)
	}
	log.Printf("[sync] Found %d entities", len(entities))

	// 3. Scan topics directory
	topics, err := scanDirectory(filepath.Join(wikiDir, "topics"), "topic")
	if err != nil {
		log.Printf("[sync] Error scanning topics: %v", err)
	}
	log.Printf("[sync] Found %d topics", len(topics))

	// 4. Update index.md with all three sections
	if err := updateIndexMD(wikiDir, sources, entities, topics); err != nil {
		log.Printf("[sync] Error updating index.md: %v", err)
		return err
	}

	// 5. Update sources.md
	if err := updateSourcesMD(wikiDir, sources); err != nil {
		log.Printf("[sync] Error updating sources.md: %v", err)
	}

	// 6. Update entities.md
	if err := updateEntitiesMD(wikiDir, entities); err != nil {
		log.Printf("[sync] Error updating entities.md: %v", err)
	}

	// 7. Update topics.md
	if err := updateTopicsMD(wikiDir, topics); err != nil {
		log.Printf("[sync] Error updating topics.md: %v", err)
	}

	log.Printf("[sync] Completed index sync")
	return nil
}

// Item represents a wiki page with its metadata
type Item struct {
	Name        string
	Description string
	Path        string // relative path from wiki root, e.g., "sources/Foo.md"
}

// scanDirectory scans a directory and extracts metadata from each .md file
func scanDirectory(dir string, itemType string) ([]Item, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // directory doesn't exist, return empty list
		}
		return nil, err
	}

	items := []Item{}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".md") {
			filePath := filepath.Join(dir, file.Name())
			content, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			name, description := parseFrontmatter(content)
			if name == "" {
				// Use filename as fallback if no frontmatter name
				name = strings.TrimSuffix(file.Name(), ".md")
			}
			if description == "" {
				description = "无描述"
			}

			// Build relative path
			relPath := filepath.Join(filepath.Base(dir), file.Name())

			items = append(items, Item{
				Name:        name,
				Description: description,
				Path:        relPath,
			})
		}
	}

	return items, nil
}

// parseFrontmatter extracts name and description from YAML frontmatter
func parseFrontmatter(content []byte) (name, description string) {
	// YAML frontmatter is between --- markers
	if !bytes.HasPrefix(content, []byte("---\n")) {
		return "", ""
	}

	// Find the closing --- (look for "\n---" after the first 4 bytes)
	endMarker := bytes.Index(content[4:], []byte("\n---"))
	if endMarker == -1 {
		return "", ""
	}

	// endMarker is relative to content[4:], so actual position is 4 + endMarker
	frontmatter := string(content[4 : 4+endMarker])

	// Parse name field
	namePattern := regexp.MustCompile(`(?m)^name:\s*(.+)$`)
	nameMatch := namePattern.FindStringSubmatch(frontmatter)
	if len(nameMatch) > 1 {
		name = strings.TrimSpace(nameMatch[1])
	}

	// Parse description field
	descPattern := regexp.MustCompile(`(?m)^description:\s*(.+)$`)
	descMatch := descPattern.FindStringSubmatch(frontmatter)
	if len(descMatch) > 1 {
		description = strings.TrimSpace(descMatch[1])
	}

	return name, description
}

// updateIndexMD rebuilds index.md with Sources, Entities, Topics sections
func updateIndexMD(wikiDir string, sources, entities, topics []Item) error {
	indexPath := filepath.Join(wikiDir, "index.md")

	// Read existing content to preserve title/header if present
	var header string
	if content, err := os.ReadFile(indexPath); err == nil {
		header = extractHeader(content)
	}

	// Build new content
	var buf strings.Builder
	buf.WriteString(header)
	buf.WriteString("\n")

	buf.WriteString("## Sources\n")
	if len(sources) == 0 {
		buf.WriteString("（暂无）\n\n")
	} else {
		for _, item := range sources {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
		buf.WriteString("\n")
	}

	buf.WriteString("## Entities\n")
	if len(entities) == 0 {
		buf.WriteString("（暂无）\n\n")
	} else {
		for _, item := range entities {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
		buf.WriteString("\n")
	}

	buf.WriteString("## Topics\n")
	if len(topics) == 0 {
		buf.WriteString("（暂无）\n\n")
	} else {
		for _, item := range topics {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
		buf.WriteString("\n")
	}

	return os.WriteFile(indexPath, []byte(buf.String()), 0644)
}

// extractHeader preserves the title/header from existing index.md content
func extractHeader(content []byte) string {
	lines := strings.Split(string(content), "\n")
	headerLines := []string{}

	for _, line := range lines {
		// Stop at first section header
		if strings.HasPrefix(line, "## ") {
			break
		}
		headerLines = append(headerLines, line)
	}

	// Trim trailing empty lines
	for len(headerLines) > 0 && headerLines[len(headerLines)-1] == "" {
		headerLines = headerLines[:len(headerLines)-1]
	}

	return strings.Join(headerLines, "\n")
}

// updateSourcesMD rebuilds sources.md with all source documents
func updateSourcesMD(wikiDir string, sources []Item) error {
	path := filepath.Join(wikiDir, "sources.md")

	var buf strings.Builder
	buf.WriteString("# Sources\n\n")

	if len(sources) == 0 {
		buf.WriteString("（暂无源文档）\n")
	} else {
		for _, item := range sources {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
	}

	return os.WriteFile(path, []byte(buf.String()), 0644)
}

// updateEntitiesMD rebuilds entities.md with all entities
func updateEntitiesMD(wikiDir string, entities []Item) error {
	path := filepath.Join(wikiDir, "entities.md")

	var buf strings.Builder
	buf.WriteString("# Entities\n\n")

	if len(entities) == 0 {
		buf.WriteString("（暂无实体）\n")
	} else {
		for _, item := range entities {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
	}

	return os.WriteFile(path, []byte(buf.String()), 0644)
}

// updateTopicsMD rebuilds topics.md with all topics
func updateTopicsMD(wikiDir string, topics []Item) error {
	path := filepath.Join(wikiDir, "topics.md")

	var buf strings.Builder
	buf.WriteString("# Topics\n\n")

	if len(topics) == 0 {
		buf.WriteString("（暂无主题）\n")
	} else {
		for _, item := range topics {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
	}

	return os.WriteFile(path, []byte(buf.String()), 0644)
}