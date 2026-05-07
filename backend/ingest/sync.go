package ingest

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SyncIndexFiles scans wiki directories and rebuilds all index files deterministically.
// This ensures index.md, sources.md, entities.md, and topics.md stay in sync with actual files.
// Scan errors cause an immediate return to prevent overwriting valid index content with empty placeholders.
func SyncIndexFiles(wikiDir string) error {
	log.Printf("[sync] Starting index sync for wiki: %s", wikiDir)

	// 1. Scan sources directory
	sources, err := scanDirectory(filepath.Join(wikiDir, "sources"), "source")
	if err != nil {
		return fmt.Errorf("scanning sources: %w", err)
	}
	log.Printf("[sync] Found %d sources", len(sources))

	// 2. Scan entities directory
	entities, err := scanDirectory(filepath.Join(wikiDir, "entities"), "entity")
	if err != nil {
		return fmt.Errorf("scanning entities: %w", err)
	}
	log.Printf("[sync] Found %d entities", len(entities))

	// 3. Scan topics directory
	topics, err := scanDirectory(filepath.Join(wikiDir, "topics"), "topic")
	if err != nil {
		return fmt.Errorf("scanning topics: %w", err)
	}
	log.Printf("[sync] Found %d topics", len(topics))

	// 4. Update index.md with all three sections
	if err := updateIndexMD(wikiDir, sources, entities, topics); err != nil {
		return fmt.Errorf("updating index.md: %w", err)
	}

	// 5. Update sources.md
	if err := updateSectionMD(wikiDir, "sources.md", "Sources", "（暂无源文档）", sources); err != nil {
		return fmt.Errorf("updating sources.md: %w", err)
	}

	// 6. Update entities.md
	if err := updateSectionMD(wikiDir, "entities.md", "Entities", "（暂无实体）", entities); err != nil {
		return fmt.Errorf("updating entities.md: %w", err)
	}

	// 7. Update topics.md
	if err := updateSectionMD(wikiDir, "topics.md", "Topics", "（暂无主题）", topics); err != nil {
		return fmt.Errorf("updating topics.md: %w", err)
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

	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})

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

// updateIndexMD rebuilds index.md with Sources, Entities, Topics sections.
// NOTE: This function only preserves content before the first "## " header.
// Any custom content between section headers (## Sources, ## Entities, ## Topics)
// will be lost on rebuild. Do not add custom paragraphs between sections in index.md.
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

	writeSection := func(title string, items []Item) {
		buf.WriteString("## " + title + "\n")
		if len(items) == 0 {
			buf.WriteString("（暂无）\n\n")
		} else {
			for _, item := range items {
				buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
			}
			buf.WriteString("\n")
		}
	}

	writeSection("Sources", sources)
	writeSection("Entities", entities)
	writeSection("Topics", topics)

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

// updateSectionMD rebuilds a single-section index file (sources.md, entities.md, topics.md)
func updateSectionMD(wikiDir, filename, title, emptyText string, items []Item) error {
	path := filepath.Join(wikiDir, filename)

	var buf strings.Builder
	buf.WriteString("# " + title + "\n\n")

	if len(items) == 0 {
		buf.WriteString(emptyText + "\n")
	} else {
		for _, item := range items {
			buf.WriteString(fmt.Sprintf("- [%s](%s) — %s\n", item.Name, item.Path, item.Description))
		}
	}

	return os.WriteFile(path, []byte(buf.String()), 0644)
}
