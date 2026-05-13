package ingest

import (
	"context"
	"fmt"
	"llm-knowledge/claude"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const summaryPrompt = `请阅读文件 %s 的内容，并用200-300字概括其核心内容。
摘要应包含：
- 文档主题和目的
- 核心观点或关键发现（2-3个）
- 主要方法或技术（如有）
- 结论或意义

只输出摘要内容，不要添加任何其他解释或格式。`

// GenerateSummary generates a summary by providing file path to Claude
// Claude uses its Read tool to read the paper.md content
// userDir is the user's directory for Claude session isolation
// rawRelPath is the path relative to userDir (e.g., "raw/papers/title" or "raw/rss/feed/title.md")
func GenerateSummary(userDir string, rawRelPath string, claudeBin string) (string, error) {
	// Determine the actual file path relative to userDir
	var paperRelPath string
	if strings.HasSuffix(rawRelPath, ".md") {
		// Direct .md file path (RSS/Web format)
		paperRelPath = rawRelPath
	} else {
		// Directory path with paper.md (PDF format)
		paperRelPath = rawRelPath + "/paper.md"
	}

	// Check if file exists
	paperAbsPath := filepath.Join(userDir, paperRelPath)
	if _, err := os.Stat(paperAbsPath); err != nil {
		return "", fmt.Errorf("file not found: %s", paperAbsPath)
	}

	// Create Claude client
	client := claude.NewClientWithPath(claudeBin)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Generate summary using -p mode (faster than stream-json)
	// Claude runs in userDir, so Read tool sees paths relative to userDir
	prompt := fmt.Sprintf(summaryPrompt, paperRelPath)
	summary, err := client.SendSimpleWithRead(ctx, prompt, userDir)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	return summary, nil
}