package ingest

import (
	"context"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"strings"
	"text/template"
)

// ingestPrompt is the template for the Claude CLI to ingest a raw document
const ingestPrompt = `你是一个知识库维护者。请读取以下源文档，并完成：

{{if .Summary}}
文档概述: {{.Summary}}
{{end}}

1. 在 {{.WikiDir}}/sources/{{.Name}}.md 创建源文档页：
   - 标题、作者、年份、期刊
   - 摘要
   - 关键发现（3-5 点）
   - 核心方法
   - 局限性

2. 更新 {{.WikiDir}}/index.md，添加此文档条目

3. 更新 {{.WikiDir}}/entities/ 下相关实体页（如提到的新方法、模型、人物）

4. 更新 {{.WikiDir}}/topics/ 下相关主题页（新发现 vs 已有论断）

5. 在 {{.WikiDir}}/log.md 追加操作日志

源文档路径: {{.RawPath}}

请用 Read 工具读取后，用 Write 工具完成以上所有更新。`

// Pipeline manages the ingestion of raw documents into the wiki
type Pipeline struct {
	WikiDir string       // Path to the wiki directory
	Claude  *claude.Client
}

// NewPipeline creates a new ingest pipeline
func NewPipeline(wikiDir string, claudeBin string) *Pipeline {
	return &Pipeline{
		WikiDir: wikiDir,
		Claude:  claude.NewClientWithPath(claudeBin),
	}
}

// Ingest reads a raw document and uses Claude to update the wiki
// docID is used to fetch the summary from DB if available
func (p *Pipeline) Ingest(ctx context.Context, rawPath, name string, docID uint) error {
	// Fetch summary from DB if available
	var doc db.Document
	summary := ""
	if docID > 0 {
		if err := db.DB.First(&doc, docID).Error; err == nil && doc.Summary != "" {
			summary = doc.Summary
			log.Printf("[ingest] Using existing summary for %s: %s", name, truncate(summary, 50))
		}
	}

	prompt, err := buildPrompt(ingestPrompt, map[string]string{
		"WikiDir": p.WikiDir,
		"Name":    name,
		"RawPath": rawPath,
		"Summary": summary,
	})
	if err != nil {
		return err
	}

	log.Printf("[ingest] Starting ingest for: %s", name)
	log.Printf("[ingest] Raw path: %s", rawPath)
	log.Printf("[ingest] Wiki dir: %s", p.WikiDir)

	eventCh := make(chan claude.StreamEvent)
	go func() {
		defer close(eventCh)
		if err := p.Claude.Send(ctx, prompt, eventCh); err != nil {
			log.Printf("[ingest] error sending to Claude: %v", err)
		}
	}()

	var lastContent string
	for evt := range eventCh {
		// Only log non-empty content and meaningful events
		content := strings.TrimSpace(evt.Content)
		if content != "" && content != lastContent {
			// Truncate for log readability
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			log.Printf("[ingest] %s: %s", evt.Type, content)
			lastContent = content
		}

		// Log errors always
		if evt.Type == "error" && evt.Error != "" {
			log.Printf("[ingest] ERROR: %s", evt.Error)
		}
	}

	log.Printf("[ingest] Completed ingest for: %s", name)
	return nil
}

// buildPrompt renders a template with the given data
func buildPrompt(tmplStr string, data map[string]string) (string, error) {
	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// truncate shortens a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
