package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name            string
		content         string
		expectedName    string
		expectedDesc    string
	}{
		{
			name: "complete frontmatter",
			content: `---
name: Test Document
description: A test description
---
# Content`,
			expectedName: "Test Document",
			expectedDesc: "A test description",
		},
		{
			name: "only name",
			content: `---
name: OnlyName
---
# Content`,
			expectedName: "OnlyName",
			expectedDesc: "",
		},
		{
			name: "no frontmatter",
			content: `# No Frontmatter
Just content`,
			expectedName: "",
			expectedDesc: "",
		},
		{
			name: "empty frontmatter",
			content: `---
---
# Content`,
			expectedName: "",
			expectedDesc: "",
		},
		{
			name: "multiline description",
			content: `---
name: Doc Name
description: Description with spaces and more text
---
# Content`,
			expectedName: "Doc Name",
			expectedDesc: "Description with spaces and more text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, desc := parseFrontmatter([]byte(tt.content))
			if name != tt.expectedName {
				t.Errorf("parseFrontmatter name = %q, want %q", name, tt.expectedName)
			}
			if desc != tt.expectedDesc {
				t.Errorf("parseFrontmatter description = %q, want %q", desc, tt.expectedDesc)
			}
		})
	}
}

func TestScanDirectory(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	sourcesDir := filepath.Join(tmpDir, "sources")
	if err := os.MkdirAll(sourcesDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files
	files := []struct {
		filename    string
		content     string
	}{
		{
			filename: "Doc1.md",
			content: `---
name: Document One
description: First document
---
# Document One`,
		},
		{
			filename: "Doc2.md",
			content: `---
name: Document Two
description: Second document
---
# Document Two`,
		},
		{
			filename: "NoFrontmatter.md",
			content: `# No Frontmatter
Just content`,
		},
	}

	for _, f := range files {
		path := filepath.Join(sourcesDir, f.filename)
		if err := os.WriteFile(path, []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Scan directory
	items, err := scanDirectory(sourcesDir, "source")
	if err != nil {
		t.Fatalf("scanDirectory failed: %v", err)
	}

	if len(items) != 3 {
		t.Errorf("scanDirectory returned %d items, want 3", len(items))
	}

	// Check items
	expectedItems := map[string]string{
		"Document One":   "First document",
		"Document Two":   "Second document",
		"NoFrontmatter":  "无描述",
	}

	for _, item := range items {
		expectedDesc, ok := expectedItems[item.Name]
		if !ok {
			t.Errorf("Unexpected item name: %s", item.Name)
			continue
		}
		if item.Description != expectedDesc {
			t.Errorf("Item %s description = %q, want %q", item.Name, item.Description, expectedDesc)
		}
	}
}

func TestSyncIndexFiles(t *testing.T) {
	// Create temp wiki directory structure
	tmpWiki := t.TempDir()

	// Create directories
	dirs := []string{
		filepath.Join(tmpWiki, "sources"),
		filepath.Join(tmpWiki, "entities"),
		filepath.Join(tmpWiki, "topics"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create test files
	testFiles := []struct {
		dir         string
		filename    string
		content     string
	}{
		{
			dir:      "sources",
			filename: "TestSource.md",
			content: `---
name: Test Source
description: A source document
---
# Test Source`,
		},
		{
			dir:      "entities",
			filename: "TestEntity.md",
			content: `---
name: Test Entity
description: An entity
---
# Test Entity`,
		},
		{
			dir:      "topics",
			filename: "TestTopic.md",
			content: `---
name: Test Topic
description: A topic
---
# Test Topic`,
		},
	}

	for _, tf := range testFiles {
		path := filepath.Join(tmpWiki, tf.dir, tf.filename)
		if err := os.WriteFile(path, []byte(tf.content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Run sync
	if err := SyncIndexFiles(tmpWiki); err != nil {
		t.Fatalf("SyncIndexFiles failed: %v", err)
	}

	// Verify index.md was created
	indexPath := filepath.Join(tmpWiki, "index.md")
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("index.md not created: %v", err)
	}

	// Check index.md contains expected sections
	indexStr := string(content)
	if !containsSection(indexStr, "## Sources") {
		t.Error("index.md missing Sources section")
	}
	if !containsSection(indexStr, "## Entities") {
		t.Error("index.md missing Entities section")
	}
	if !containsSection(indexStr, "## Topics") {
		t.Error("index.md missing Topics section")
	}

	// Verify sources.md
	sourcesPath := filepath.Join(tmpWiki, "sources.md")
	sourcesContent, err := os.ReadFile(sourcesPath)
	if err != nil {
		t.Fatalf("sources.md not created: %v", err)
	}
	if !containsLink(string(sourcesContent), "Test Source") {
		t.Error("sources.md missing Test Source link")
	}

	// Verify entities.md
	entitiesPath := filepath.Join(tmpWiki, "entities.md")
	entitiesContent, err := os.ReadFile(entitiesPath)
	if err != nil {
		t.Fatalf("entities.md not created: %v", err)
	}
	if !containsLink(string(entitiesContent), "Test Entity") {
		t.Error("entities.md missing Test Entity link")
	}

	// Verify topics.md
	topicsPath := filepath.Join(tmpWiki, "topics.md")
	topicsContent, err := os.ReadFile(topicsPath)
	if err != nil {
		t.Fatalf("topics.md not created: %v", err)
	}
	if !containsLink(string(topicsContent), "Test Topic") {
		t.Error("topics.md missing Test Topic link")
	}
}

func TestSyncIndexFilesEmpty(t *testing.T) {
	// Create empty wiki directory
	tmpWiki := t.TempDir()

	// Run sync on empty wiki
	if err := SyncIndexFiles(tmpWiki); err != nil {
		t.Fatalf("SyncIndexFiles failed: %v", err)
	}

	// Verify index.md was created with empty placeholders
	indexPath := filepath.Join(tmpWiki, "index.md")
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("index.md not created: %v", err)
	}

	indexStr := string(content)
	if !containsText(indexStr, "（暂无）") {
		t.Error("index.md should show empty placeholders")
	}
}

func TestUpdateIndexMDPreservesHeader(t *testing.T) {
	tmpWiki := t.TempDir()

	// Create existing index.md with header
	existingIndex := `# Knowledge Base

This is a knowledge base.

## Sources
- [Old](sources/Old.md) — Old description

## Entities
- [OldEntity](entities/OldEntity.md) — Old entity

## Topics
- [OldTopic](topics/OldTopic.md) — Old topic
`
	indexPath := filepath.Join(tmpWiki, "index.md")
	if err := os.WriteFile(indexPath, []byte(existingIndex), 0644); err != nil {
		t.Fatal(err)
	}

	// Create new sources directory
	sourcesDir := filepath.Join(tmpWiki, "sources")
	if err := os.MkdirAll(sourcesDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create new source file
	newSource := `---
name: New Source
description: New description
---
# New Source`
	newSourcePath := filepath.Join(sourcesDir, "New.md")
	if err := os.WriteFile(newSourcePath, []byte(newSource), 0644); err != nil {
		t.Fatal(err)
	}

	// Scan and update
	sources, _ := scanDirectory(sourcesDir, "source")
	if err := updateIndexMD(tmpWiki, sources, nil, nil); err != nil {
		t.Fatalf("updateIndexMD failed: %v", err)
	}

	// Read updated content
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	// Check header preserved
	updatedStr := string(content)
	if !containsText(updatedStr, "# Knowledge Base") {
		t.Error("Header not preserved")
	}
	if !containsText(updatedStr, "This is a knowledge base.") {
		t.Error("Header content not preserved")
	}

	// Check new source added
	if !containsLink(updatedStr, "New Source") {
		t.Error("New source not added")
	}
}

// Helper functions
func containsSection(content, section string) bool {
	return strings.Contains(content, section)
}

func containsLink(content, name string) bool {
	return strings.Contains(content, "["+name+"]")
}

func containsText(content, text string) bool {
	return strings.Contains(content, text)
}