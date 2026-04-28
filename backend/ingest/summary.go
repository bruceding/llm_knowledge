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
// rawPath can be either:
//   - A directory path (e.g., "raw/pdf/title") containing paper.md
//   - A direct .md file path (e.g., "raw/rss/feed/title.md")
func GenerateSummary(dataDir string, rawPath string, claudeBin string) (string, error) {
	// Determine the actual file path
	var paperPath string
	if strings.HasSuffix(rawPath, ".md") {
		// Direct .md file path (RSS format)
		paperPath = filepath.Join(dataDir, rawPath)
	} else {
		// Directory path with paper.md (PDF/Web format)
		paperPath = filepath.Join(dataDir, rawPath, "paper.md")
	}

	// Check if file exists
	if _, err := os.Stat(paperPath); err != nil {
		return "", fmt.Errorf("file not found: %s", paperPath)
	}

	// Create Claude client
	client := claude.NewClientWithPath(claudeBin)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Generate summary using -p mode (faster than stream-json)
	prompt := fmt.Sprintf(summaryPrompt, paperPath)
	summary, err := client.SendSimpleWithRead(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	return summary, nil
}