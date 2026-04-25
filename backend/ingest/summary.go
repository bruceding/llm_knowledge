package ingest

import (
	"context"
	"fmt"
	"llm-knowledge/claude"
	"os"
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
func GenerateSummary(dataDir string, rawPath string, claudeBin string) (string, error) {
	// Construct paper.md file path
	paperPath := fmt.Sprintf("%s/%s/paper.md", dataDir, rawPath)

	// Check if file exists
	if _, err := os.Stat(paperPath); err != nil {
		return "", fmt.Errorf("paper.md not found: %s", paperPath)
	}

	// Create Claude client
	client := claude.NewClientWithPath(claudeBin)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Generate summary - Claude will read the file using its Read tool
	prompt := fmt.Sprintf(summaryPrompt, paperPath)
	summary, err := client.SendWithTools(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	// Clean up summary (remove leading/trailing whitespace only)
	summary = strings.TrimSpace(summary)

	return summary, nil
}