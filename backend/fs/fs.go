package fs

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed dist/*
var DistFS embed.FS

// defaultSchema is the initial schema.md content
var defaultSchema = `# LLM Knowledge Schema

This document defines the structure and conventions for the knowledge base.

## Directories

- **raw/papers** - Academic papers and research documents
- **raw/articles** - Blog posts and articles from feeds
- **raw/feeds** - RSS/Atom feed sources
- **wiki/entities** - Entity pages (people, organizations, concepts)
- **wiki/topics** - Topic pages (thematic collections)
- **wiki/sources** - Source pages (origin of information)
- **data** - Generated data files

## File Naming Conventions

- Use lowercase with hyphens: ` + "`" + `my-topic.md` + "`" + `
- Include date prefix for time-sensitive content: ` + "`" + `2024-01-15-paper-review.md` + "`" + `
- Entity files should be prefixed by type: ` + "`" + `person-john-doe.md` + "`" + `, ` + "`" + `org-example-corp.md` + "`" + `

## Wiki Pages

Each wiki page should follow this structure:

` + "```markdown" + `
# Title

Brief description

## Content

Main content here...

## Related

- [[other-page]]
- [[entity:name]]
` + "```" + `

## Metadata

Use YAML frontmatter for metadata:

` + "```yaml" + `
---
created: 2024-01-15
updated: 2024-01-15
tags: [tag1, tag2]
---
` + "```" + `
`

// InitDirs creates the directory structure for the knowledge base
func InitDirs(dataDir string) error {
	dirs := []string{
		"raw/papers",
		"raw/articles",
		"raw/feeds",
		"wiki/entities",
		"wiki/topics",
		"wiki/sources",
		"data",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0755); err != nil {
			return err
		}
	}

	// Create initial files
	if err := writeIfNotExist(filepath.Join(dataDir, "wiki/index.md"), "# Index\n\nWelcome to your LLM Knowledge base.\n\n"); err != nil {
		return err
	}
	if err := writeIfNotExist(filepath.Join(dataDir, "wiki/log.md"), "# Log\n\n"); err != nil {
		return err
	}
	if err := writeIfNotExist(filepath.Join(dataDir, "schema.md"), defaultSchema); err != nil {
		return err
	}

	return nil
}

// writeIfNotExist creates a file with the given content only if it doesn't exist
func writeIfNotExist(path, content string) error {
	// Check if file already exists
	if _, err := os.Stat(path); err == nil {
		return nil // File exists, skip
	} else if !os.IsNotExist(err) {
		return err // Some other error
	}

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	// Write the file
	return os.WriteFile(path, []byte(content), 0644)
}