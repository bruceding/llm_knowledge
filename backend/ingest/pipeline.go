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

## 1. 创建源文档页

在 {{.WikiDir}}/sources/{{.Name}}.md 创建：
---
name: {{.Name}}
description: 一句话描述（50字以内）
type: source
---
# 文档标题

## Metadata
- 标题、作者、年份、期刊/来源、URL

## Abstract
摘要内容

## Key Findings
- 发现1
- 发现2
- 发现3

## Core Methods
方法描述

## Limitations
局限性

## Related
- 实体链接（如：[CSA](../entities/CSA.md)）
- 主题链接（如：[Long-Context Efficiency](../topics/Long-Context-Efficiency.md)）

## 2. 创建/更新实体页

对于文档中提到的新方法、模型、技术、人物等实体，在 {{.WikiDir}}/entities/ 下创建或更新页面：
---
name: 实体名称
description: 一句话描述（50字以内）
type: entity
---
# 实体名称

## Overview
简要介绍

## Core Mechanism/Details
核心内容（按实体类型调整）

## Configuration/Parameters（如适用）
参数配置

## Evidence Sources
证据来源（论文中的具体章节）

## Related
- 相关实体链接
- Source: [{{.Name}}](../sources/{{.Name}}.md)  ← 必须包含此字段

## 3. 创建/更新主题页

对于文档涉及的研究主题，在 {{.WikiDir}}/topics/ 下创建或更新：
---
name: 主题名称
description: 一句话描述（50字以内）
type: topic
---
# 主题名称

## Problem Statement
问题描述

## Prior State
之前的研究状态

## New Breakthrough/Contribution
本次文档的贡献

## Evidence Sources
具体证据

## Related
- 相关实体链接（如：[CSA](../entities/CSA.md)）
- Source: [{{.Name}}](../sources/{{.Name}}.md)  ← 必须包含此字段

## 4. 更新索引文件

更新 {{.WikiDir}}/index.md，添加文档条目：
- [{{.Name}}](sources/{{.Name}}.md) — 一句话简介

更新 {{.WikiDir}}/sources.md（如不存在则创建）：
# Sources
列出所有源文档（格式：[名称](sources/路径.md) — 简介）

更新 {{.WikiDir}}/entities.md（如不存在则创建）：
# Entities
按类别分组列出所有实体（格式：[名称](entities/名称.md) — 简介）

更新 {{.WikiDir}}/topics.md（如不存在则创建）：
# Topics
列出所有主题（格式：[名称](topics/名称.md) — 简介）

## 5. 追加操作日志

在 {{.WikiDir}}/log.md 追加：
- YYYY-MM-DD: 导入 {{.Name}}，创建了 X 个实体，Y 个主题

---

重要提示：
- Markdown链接的URL路径中如果有空格，必须将空格编码为 %20
- 例如：[文档名称](sources/文档%20名称.md)
- 显示文本可以包含空格，但URL路径必须编码

源文档路径: {{.RawPath}}

请用 Read 工具读取源文档和现有 wiki 文件，用 Write 工具完成以上所有更新。`

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
