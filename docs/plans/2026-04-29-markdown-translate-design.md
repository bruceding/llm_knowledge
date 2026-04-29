# Markdown 翻译功能设计

## 概述

为 Web/RSS 文档增加翻译功能，生成双语对照的翻译文件。

## 现有配置

使用已有的翻译配置：
- `TranslationApiBase`: API endpoint（默认 dashscope）
- `TranslationApiKey`: 认证 key
- `TranslationModel`: 模型名称（默认 deepseek-v4-flash）

## 后端设计

### 新建文件

`backend/api/markdown_translate.go`

### API Endpoints

#### 1. POST /api/markdown-translate

SSE 流式翻译 Markdown 文档。

**输入**：
```json
{
  "docId": 123,
  "targetLang": "zh"  // 或 "en"
}
```

**输出**：SSE 事件流
- `progress`: 翻译进度消息
- `complete`: 翻译完成，返回翻译文件路径
- `error`: 错误信息

**流程**：
1. 获取文档，验证是 Web/RSS 类型
2. 检查原始 Markdown 文件存在
3. 获取翻译配置（API Key、Model 等）
4. 构建翻译 prompt，调用 LLM API
5. SSE 流式返回翻译结果
6. 完成后写入文件：`<title>_zh.md` 或 `<title>_en.md`

#### 2. GET /api/documents/:id/markdown-translation-status

检测 Markdown 翻译文件是否存在。

**返回**：
```json
{
  "exists": true,
  "path": "/data/raw/rss/FeedName/Title_zh.md",
  "targetLang": "zh"
}
```

### 翻译 Prompt

要求 LLM 输出双语对照格式：

```
请将以下 Markdown 内容翻译为中文。

要求：
1. 保持原文的 Markdown 格式（标题、链接、引用等）
2. 每个段落原文后，添加翻译内容，格式为：
   > 翻译：译文内容
3. 代码块和 URL 不需要翻译

原文内容：
[原始 Markdown 内容]
```

### 文件存储

翻译文件存储位置：
- RSS: `raw/rss/<feed>/<title>_zh.md`
- Web: `raw/web/<title>/paper_zh.md`

### 依赖

引入 OpenAI Go SDK：`github.com/sashabaranov/go-openai`

## 前端设计

### 修改文件

`frontend/src/components/DocDetail.tsx`

### 新增功能

1. **翻译按钮**：Web/RSS 文档显示"翻译"按钮
2. **视图模式**：新增 `bilingual` 模式（双语对照）
3. **状态检测**：页面加载时检测翻译文件是否存在
4. **重新翻译**：已有翻译时显示"重新翻译"按钮

### API 修改

`frontend/src/api.ts` 新增：

```typescript
export function translateMarkdown(
  docId: number,
  targetLang: string,
  onEvent: (event: SSEEvent) => void
): Promise<void>

export async function checkMarkdownTranslationStatus(docId: number): Promise<{
  exists: boolean
  path: string
  targetLang: string
}>
```

## 错误处理

| 场景 | 处理 |
|------|------|
| API Key 未配置 | 返回错误："API key not configured" |
| 翻译功能未启用 | 返回错误："translation not enabled" |
| 原始文件不存在 | 返回错误："source file not found" |
| API 调用失败 | SSE 返回 error 事件 |
| 翻译文件已存在 | 显示已有翻译，提供重新翻译按钮 |