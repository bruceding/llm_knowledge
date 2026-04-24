# LLM Wiki 个人知识库 — 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 构建一个基于 LLM Wiki 模式的个人知识库系统，支持 PDF 论文解析、Wiki 自动维护、SSE 流式问答。

**Architecture:** Go (Echo + GORM + SQLite) 作为后端，React 作为前端 UI，通过 os/exec 调用 Claude CLI 完成 LLM 任务。raw/ 存原始文件，wiki/ 存 LLM 维护的 markdown，SQLite 存元数据和对话记录。编译为单 Go 二进制文件部署。

**Tech Stack:** Go 1.22+, Echo v4, GORM, SQLite (mattn/go-sqlite3), React 18, TypeScript, Vite

---

### Task 1: Go 项目脚手架

**Files:**
- Create: `backend/go.mod`
- Create: `backend/main.go`
- Create: `backend/config/config.go`

**Step 1: 初始化 Go module**

```bash
cd backend && go mod init llm-knowledge
```

**Step 2: 安装核心依赖**

```bash
go get github.com/labstack/echo/v4
go get gorm.io/gorm
go get gorm.io/driver/sqlite
go get github.com/mattn/go-sqlite3
```

**Step 3: 创建 main.go 骨架**

```go
package main

import (
    "github.com/labstack/echo/v4"
    "github.com/labstack/echo/v4/middleware"
)

func main() {
    e := echo.New()
    e.Use(middleware.Logger())
    e.Use(middleware.CORS())

    e.GET("/api/health", func(c echo.Context) error {
        return c.JSON(200, map[string]string{"status": "ok"})
    })

    e.Logger.Fatal(e.Start(":3456"))
}
```

**Step 4: 创建 config 包**

```go
// config/config.go
package config

import "os"

type Config struct {
    DataDir  string // ~/.llm-knowledge
    Port     string
    ClaudeBin string // claude binary path
}

func Load() *Config {
    home, _ := os.UserHomeDir()
    return &Config{
        DataDir:   home + "/.llm-knowledge",
        Port:      "3456",
        ClaudeBin: "claude",
    }
}
```

**Step 5: 验证**

Run: `cd backend && go run main.go`
Expected: server starts on :3456, `curl localhost:3456/api/health` returns `{"status":"ok"}`

**Step 6: Commit**

```bash
git add backend/ && git commit -m "feat: scaffold Go project with Echo"
```

---

### Task 2: 目录初始化 + 静态资源嵌入

**Files:**
- Create: `backend/fs/fs.go`
- Modify: `backend/main.go`

**Step 1: 创建目录初始化逻辑**

```go
// fs/fs.go
package fs

import (
    "embed"
    "os"
    "path/filepath"
)

//go:embed dist/*
var DistFS embed.FS

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
    // 创建初始文件
    writeIfNotExist(filepath.Join(dataDir, "wiki/index.md"), "# Index\n\n")
    writeIfNotExist(filepath.Join(dataDir, "wiki/log.md"), "# Log\n\n")
    writeIfNotExist(filepath.Join(dataDir, "schema.md"), defaultSchema)
    return nil
}
```

**Step 2: 在 main.go 启动时初始化目录**

```go
cfg := config.Load()
fs.InitDirs(cfg.DataDir)
```

**Step 3: 验证**

Run: `go run main.go`
Expected: `~/.llm-knowledge/` 下创建完整目录结构

**Step 4: Commit**

```bash
git add backend/fs/ && git commit -m "feat: directory initialization"
```

---

### Task 3: SQLite 数据模型 + GORM Migration

**Files:**
- Create: `backend/db/db.go`
- Create: `backend/db/models.go`

**Step 1: 定义模型**

```go
// db/models.go
package db

import "time"

type Document struct {
    ID         uint      `gorm:"primaryKey" json:"id"`
    Title      string    `json:"title"`
    SourceType string    `json:"sourceType"` // pdf, rss, web, manual
    RawPath    string    `json:"rawPath"`
    WikiPath   string    `json:"wikiPath"`
    Language   string    `json:"language"`
    Status     string    `gorm:"default:inbox" json:"status"` // inbox, published, archived
    Metadata   string    `json:"metadata"`                     // JSON string
    CreatedAt  time.Time `json:"createdAt"`
    UpdatedAt  time.Time `json:"updatedAt"`
}

type Tag struct {
    ID        uint      `gorm:"primaryKey" json:"id"`
    Name      string    `gorm:"unique" json:"name"`
    Color     string    `json:"color"`
    CreatedAt time.Time `json:"createdAt"`
}

type DocumentTag struct {
    DocumentID uint `gorm:"primaryKey" json:"documentId"`
    TagID      uint `gorm:"primaryKey" json:"tagId"`
}

type Conversation struct {
    ID        uint      `gorm:"primaryKey" json:"id"`
    Title     string    `json:"title"`
    CreatedAt time.Time `json:"createdAt"`
    UpdatedAt time.Time `json:"updatedAt"`
}

type ConversationMessage struct {
    ID             uint      `gorm:"primaryKey" json:"id"`
    ConversationID uint      `json:"conversationId"`
    Role           string    `json:"role"` // user, assistant, system
    Content        string    `json:"content"`
    ContextDocIDs  string    `json:"contextDocIds"` // JSON array
    CreatedAt      time.Time `json:"createdAt"`
}
```

**Step 2: 创建数据库初始化**

```go
// db/db.go
package db

import (
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

var DB *gorm.DB

func Init(path string) error {
    var err error
    DB, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
    if err != nil {
        return err
    }
    return DB.AutoMigrate(&Document{}, &Tag{}, &DocumentTag{}, &Conversation{}, &ConversationMessage{})
}
```

**Step 3: 在 main.go 中初始化数据库**

```go
db.Init(filepath.Join(cfg.DataDir, "data", "knowledge.db"))
```

**Step 4: 验证**

Run: `go run main.go`
Expected: `knowledge.db` 文件创建，5 张表自动建好

**Step 5: Commit**

```bash
git add backend/db/ backend/main.go && git commit -m "feat: SQLite data models and GORM migration"
```

---

### Task 4: PDF 解析模块

**Files:**
- Create: `backend/ingest/pdf.go`
- Create: `backend/ingest/pdf_test.go`

**Step 1: 安装 PDF 解析库**

```bash
go get github.com/ledongthuc/pdf
```

**Step 2: 实现 PDF 文本提取**

```go
// ingest/pdf.go
package ingest

import (
    "os"
    "path/filepath"
    "strings"

    "github.com/ledongthuc/pdf"
)

type ExtractedPDF struct {
    FullText string
    Pages    []string
}

func ExtractPDFText(filePath string) (*ExtractedPDF, error) {
    f, r, err := pdf.Open(filePath)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    var pages []string
    for i := 1; i <= r.NumPage(); i++ {
        p := r.Page(i)
        if p.V.IsNull() {
            continue
        }
        text, _ := p.GetPlainText(nil)
        pages = append(pages, text)
    }

    return &ExtractedPDF{
        FullText: strings.Join(pages, "\n\n"),
        Pages:    pages,
    }, nil
}

func CleanPDFText(text string) string {
    // 去页眉页脚（简单启发式：跳过首尾短行）
    lines := strings.Split(text, "\n")
    var cleaned []string
    for i, line := range lines {
        line = strings.TrimSpace(line)
        if line == "" {
            cleaned = append(cleaned, line)
            continue
        }
        // 跳过页码行
        if len(line) < 5 && isNumeric(line) {
            continue
        }
        // 保留内容
        cleaned = append(cleaned, line)
        _ = i
    }
    return strings.Join(cleaned, "\n")
}
```

**Step 3: 编写测试**

```go
// ingest/pdf_test.go
func TestExtractPDFText(t *testing.T) {
    // 用测试 PDF 验证
    result, err := ExtractPDFText("testdata/sample.pdf")
    assert.NoError(t, err)
    assert.NotEmpty(t, result.FullText)
}
```

**Step 4: 验证测试**

Run: `go test ./ingest/ -v`
Expected: PASS (有 sample.pdf 时)

**Step 5: Commit**

```bash
git add backend/ingest/ && git commit -m "feat: PDF text extraction module"
```

---

### Task 5: Raw 文件存储 API

**Files:**
- Create: `backend/api/raw.go`
- Modify: `backend/main.go` (注册路由)

**Step 1: 实现上传 + 存储接口**

```go
// api/raw.go
package api

import (
    "io"
    "net/http"
    "os"
    "path/filepath"

    "github.com/labstack/echo/v4"
)

type RawHandler struct {
    DataDir string
}

func (h *RawHandler) UploadPDF(c echo.Context) error {
    file, err := c.FormFile("file")
    if err != nil {
        return c.JSON(400, echo.Map{"error": "no file"})
    }

    src, _ := file.Open()
    defer src.Close()

    name := strings.TrimSuffix(file.Filename, ".pdf")
    dir := filepath.Join(h.DataDir, "raw", "papers", name)
    os.MkdirAll(filepath.Join(dir, "assets"), 0755)

    // 保存原始 PDF
    dst, _ := os.Create(filepath.Join(dir, "paper.pdf"))
    io.Copy(dst, src)
    dst.Close()

    // 提取文本
    extracted, _ := ingest.ExtractPDFText(filepath.Join(dir, "paper.pdf"))
    os.WriteFile(filepath.Join(dir, "paper.md"), []byte(extracted.FullText), 0644)

    return c.JSON(200, echo.Map{
        "path":    dir,
        "message": "PDF uploaded and text extracted",
    })
}
```

**Step 2: 在 main.go 注册路由**

```go
rawH := &api.RawHandler{DataDir: cfg.DataDir}
e.POST("/api/raw/pdf", rawH.UploadPDF)
```

**Step 3: 验证**

Run: `curl -F "file=@test.pdf" localhost:3456/api/raw/pdf`
Expected: raw/papers/{name}/ 下生成 paper.pdf + paper.md

**Step 4: Commit**

```bash
git add backend/api/raw.go backend/main.go && git commit -m "feat: PDF upload and raw storage API"
```

---

### Task 6: Claude CLI 调用封装

**Files:**
- Create: `backend/claude/client.go`

**Step 1: 实现 claude 进程调用**

```go
// claude/client.go
package claude

import (
    "bufio"
    "encoding/json"
    "io"
    "os/exec"
)

type Client struct {
    BinPath string
}

type StreamEvent struct {
    Type    string `json:"type"`    // assistant, tool_use, tool_result, result
    Content string `json:"content"`
    Tool    string `json:"tool,omitempty"`
}

func (c *Client) Send(ctx context.Context, prompt string, eventCh chan<- StreamEvent) error {
    cmd := exec.CommandContext(ctx, c.BinPath, "--stream-json")
    cmd.Stdin = strings.NewReader(prompt)

    stdout, _ := cmd.StdoutPipe()
    cmd.Start()

    scanner := bufio.NewScanner(stdout)
    for scanner.Scan() {
        var event StreamEvent
        if json.Unmarshal(scanner.Bytes(), &event) == nil {
            eventCh <- event
        }
    }

    return cmd.Wait()
}

func (c *Client) SendSimple(ctx context.Context, prompt string) (string, error) {
    cmd := exec.CommandContext(ctx, c.BinPath, "-p", prompt)
    out, err := cmd.Output()
    return string(out), err
}
```

**Step 2: 验证**

Run: 单元测试中 mock 或直接跑一次简单调用
Expected: `SendSimple` 返回 claude 响应文本

**Step 3: Commit**

```bash
git add backend/claude/ && git commit -m "feat: Claude CLI client wrapper"
```

---

### Task 7: Ingest Pipeline（Claude 读 raw → 写 wiki）

**Files:**
- Create: `backend/ingest/pipeline.go`

**Step 1: 构建 ingest prompt 模板**

```go
// ingest/pipeline.go
package ingest

const ingestPrompt = `你是一个知识库维护者。请读取以下源文档，并完成：

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
```

**Step 2: 实现 Pipeline 函数**

```go
func (p *Pipeline) Ingest(ctx context.Context, rawPath, name string) error {
    prompt := buildPrompt(ingestPrompt, map[string]string{
        "WikiDir": p.WikiDir,
        "Name":    name,
        "RawPath": rawPath,
    })

    eventCh := make(chan claude.StreamEvent)
    go p.claude.Send(ctx, prompt, eventCh)

    for evt := range eventCh {
        // 记录事件到日志（后续可 SSE 推送到前端展示 ingest 进度）
        log.Printf("[ingest] %s: %s", evt.Type, evt.Content)
    }
    return nil
}
```

**Step 3: 在 UploadPDF 中触发 ingest**

```go
// 上传完成后异步触发
go func() {
    p := ingest.NewPipeline(cfg.DataDir+"/wiki", cfg.ClaudeBin)
    p.Ingest(context.Background(), dir+"/paper.md", name)
}()
```

**Step 4: 验证**

上传 PDF → 检查 wiki/sources/ 和 wiki/index.md 是否被 claude 更新

**Step 5: Commit**

```bash
git add backend/ingest/pipeline.go backend/api/raw.go && git commit -m "feat: ingest pipeline via Claude CLI"
```

---

### Task 8: 收件箱审核 API

**Files:**
- Create: `backend/api/documents.go`

**Step 1: 实现文档 CRUD**

```go
// api/documents.go
func (h *DocHandler) ListInbox(c echo.Context) error {
    var docs []db.Document
    db.DB.Where("status = ?", "inbox").Order("created_at desc").Find(&docs)
    return c.JSON(200, docs)
}

func (h *DocHandler) Publish(c echo.Context) error {
    id := c.Param("id")
    db.DB.Model(&db.Document{}).Where("id = ?", id).Update("status", "published")
    return c.JSON(200, echo.Map{"status": "published"})
}

func (h *DocHandler) GetDoc(c echo.Context) error {
    var doc db.Document
    db.DB.Preload("Tags").First(&doc, c.Param("id"))
    return c.JSON(200, doc)
}

func (h *DocHandler) UpdateDoc(c echo.Context) error {
    // 更新标题、标签、状态等
}

func (h *DocHandler) DeleteDoc(c echo.Context) error {
    // 删除文档 + raw/ 文件 + wiki/ 页面
}
```

**Step 2: 注册路由**

```go
e.GET("/api/documents/inbox", docH.ListInbox)
e.GET("/api/documents/:id", docH.GetDoc)
e.PUT("/api/documents/:id", docH.UpdateDoc)
e.POST("/api/documents/:id/publish", docH.Publish)
e.DELETE("/api/documents/:id", docH.DeleteDoc)
```

**Step 3: 验证**

```bash
curl localhost:3456/api/documents/inbox
```

**Step 4: Commit**

```bash
git add backend/api/documents.go && git commit -m "feat: inbox review and document CRUD API"
```

---

### Task 9: Query 问答 API（SSE 流式）

**Files:**
- Create: `backend/api/query.go`

**Step 1: 实现 SSE 问答**

```go
// api/query.go
func (h *QueryHandler) Ask(c echo.Context) error {
    var req struct {
        ConversationID uint   `json:"conversationId"`
        Question       string `json:"question"`
        DocID          uint   `json:"docId,omitempty"` // 可选：聚焦某文档
    }
    c.Bind(&req)

    // 创建或获取对话
    convID := req.ConversationID
    if convID == 0 {
        conv := db.Conversation{Title: truncate(req.Question, 50)}
        db.DB.Create(&conv)
        convID = conv.ID
    }

    // 保存用户消息
    db.DB.Create(&db.ConversationMessage{
        ConversationID: convID,
        Role:           "user",
        Content:        req.Question,
    })

    // 获取历史上下文
    var history []db.ConversationMessage
    db.DB.Where("conversation_id = ?", convID).
        Order("created_at desc").Limit(10).Find(&history)

    // 构建 prompt
    prompt := buildQueryPrompt(h.DataDir, history, req.Question, req.DocID)

    // SSE headers
    c.Response().Header().Set("Content-Type", "text/event-stream")
    c.Response().Header().Set("Cache-Control", "no-cache")
    c.Response().Header().Set("Connection", "keep-alive")

    flusher, _ := c.Response().Writer.(http.Flusher)

    eventCh := make(chan claude.StreamEvent)
    ctx, cancel := context.WithCancel(c.Request().Context())
    defer cancel()

    go h.claude.Send(ctx, prompt, eventCh)

    var fullContent strings.Builder
    for evt := range eventCh {
        data, _ := json.Marshal(evt)
        fmt.Fprintf(c.Response(), "data: %s\n\n", data)
        flusher.Flush()

        if evt.Type == "assistant" {
            fullContent.WriteString(evt.Content)
        }
    }

    // 保存 assistant 消息
    db.DB.Create(&db.ConversationMessage{
        ConversationID: convID,
        Role:           "assistant",
        Content:        fullContent.String(),
    })

    return nil
}
```

**Step 2: 注册路由**

```go
e.POST("/api/query/ask", queryH.Ask)
```

**Step 3: 验证**

```bash
curl -N -X POST localhost:3456/api/query/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"注意力机制的核心贡献是什么？"}'
```
Expected: SSE 事件流式输出

**Step 4: Commit**

```bash
git add backend/api/query.go && git commit -m "feat: SSE streaming query API"
```

---

### Task 10: 翻译 API

**Files:**
- Create: `backend/api/translate.go`

**Step 1: 实现翻译接口**

```go
// api/translate.go
func (h *TranslateHandler) Translate(c echo.Context) error {
    var req struct {
        DocID      uint   `json:"docId"`
        TargetLang string `json:"targetLang"` // zh, en
    }
    c.Bind(&req)

    var doc db.Document
    db.DB.First(&doc, req.DocID)

    // 读取原文
    content, _ := os.ReadFile(doc.RawPath + "/paper.md")

    // 构建翻译 prompt
    prompt := fmt.Sprintf("请将以下内容翻译为%s，保持学术性：\n\n%s",
        req.TargetLang, string(content))

    // SSE 流式翻译
    c.Response().Header().Set("Content-Type", "text/event-stream")
    // ... 同 query SSE 模式
    go h.claude.Send(ctx, prompt, eventCh)

    // 完成后保存翻译结果到 paper_zh.md
}
```

**Step 2: 注册路由**

```go
e.POST("/api/translate", translateH.Translate)
```

**Step 3: 验证**

```bash
curl -N -X POST localhost:3456/api/translate -d '{"docId":1,"targetLang":"zh"}'
```

**Step 4: Commit**

```bash
git add backend/api/translate.go && git commit -m "feat: streaming translation API"
```

---

### Task 11: React 前端脚手架

**Files:**
- Create: `frontend/` (Vite + React + TypeScript)

**Step 1: 初始化前端项目**

```bash
npm create vite@latest frontend -- --template react-ts
cd frontend && npm install
```

**Step 2: 安装依赖**

```bash
npm install react-router-dom
npm install react-markdown remark-gfm
npm install -D tailwindcss @tailwindcss/vite
```

**Step 3: 配置 Tailwind**

```css
/* index.css */
@import "tailwindcss";
```

**Step 4: 配置 Vite proxy + build output**

```ts
// vite.config.ts
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: { '/api': 'http://localhost:3456' }
  },
  build: {
    outDir: '../backend/fs/dist'
  }
})
```

**Step 5: 创建基础布局**

```tsx
// App.tsx
function App() {
  return (
    <BrowserRouter>
      <div className="flex h-screen bg-white">
        <Sidebar />
        <main className="flex-1 overflow-auto">
          <Routes>
            <Route path="/" element={<Inbox />} />
            <Route path="/documents/:id" element={<DocDetail />} />
            <Route path="/wiki/*" element={<WikiView />} />
            <Route path="/chat/:id?" element={<ChatView />} />
            <Route path="/import" element={<ImportView />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
```

**Step 6: 验证**

Run: `npm run dev` → 打开 `localhost:5173`
Expected: 空布局正常渲染

**Step 7: Commit**

```bash
git add frontend/ && git commit -m "feat: React frontend scaffold with Tailwind"
```

---

### Task 12: 前端页面实现

**Files:**
- Create: `frontend/src/components/Sidebar.tsx`
- Create: `frontend/src/pages/Inbox.tsx`
- Create: `frontend/src/pages/DocDetail.tsx`
- Create: `frontend/src/pages/WikiView.tsx`
- Create: `frontend/src/pages/ChatView.tsx`
- Create: `frontend/src/pages/ImportView.tsx`

逐个实现页面，每个页面完成后 commit：

**Sidebar:**
- 搜索框 + 导航项（收件箱(数量徽标)、所有文档、标签、对话历史）
- Wiki 分区（Index、Entities、Topics、Sources）
- 当前选中项高亮

**Inbox:**
- 待审核文档卡片列表
- 每项显示标题、来源类型图标、日期、状态标签
- 点击进入 DocDetail

**DocDetail:**
- 左侧：markdown 渲染区域（react-markdown）
- 右侧：元数据面板（标题、作者、标签编辑器、语言、状态下拉框）
- Publish / Archive / Delete 按钮

**WikiView:**
- 渲染 wiki/ 下 markdown 文件
- 支持 `[[双向链接]]` 跳转（解析后转为 `<Link>`）
- 面包屑导航

**ChatView:**
- 消息气泡列表（user 右对齐，assistant 左对齐）
- 底部输入框
- SSE EventSource 连接，逐字流式显示
- Tool use 状态条 "正在查阅..."
- 引用链接
- 新建对话 / 历史对话切换

**ImportView:**
- 拖拽上传 PDF 区域
- 粘贴 URL 输入框（网页裁剪）
- RSS 源管理列表

**Step 1: 实现每个页面 → 每个页面写完后验收**

**Step 2: Commit 每个页面**

```bash
git add frontend/src/pages/Xxx.tsx && git commit -m "feat: Xxx page"
```

---

### Task 13: 单二进制编译

**Files:**
- Create: `Makefile`
- Modify: `backend/main.go` (嵌入前端构建产物)

**Step 1: 创建 Makefile**

```makefile
.PHONY: build run dev

build:
    cd frontend && npm run build
    cd backend && CGO_ENABLED=1 go build -o ../llm-knowledge .

run: build
    ./llm-knowledge

dev:
    cd backend && go run . &
    cd frontend && npm run dev
```

**Step 2: 配置 Go embed 前端 dist**

```go
//go:embed dist
var distFS embed.FS

// 在 main.go 中注册静态文件服务
e.Use(middleware.StaticWithConfig(middleware.StaticConfig{
    Filesystem: http.FS(distFS),
    Root:       "dist",
}))
```

**Step 3: 构建验证**

```bash
make build
./llm-knowledge
# 访问 http://localhost:3456 → 前端界面
# 访问 http://localhost:3456/api/health → {"status":"ok"}
```

**Step 4: Commit**

```bash
git add Makefile backend/main.go && git commit -m "feat: single binary build with embedded frontend"
```

---

### Task 14: 对接测试 + Bug 修复

端到端走通 MVP 全流程：

1. 上传 PDF → 检查 raw/ 和 wiki/ 产出
2. 收件箱审核 → published
3. Wiki 浏览 → 页面正常渲染、双向链接可跳转
4. 提问 → SSE 流式回答 → 追问
5. 翻译 → 保存译文
6. 单二进制运行正常

---

## 完成标准

- [ ] Go 后台单二进制可运行
- [ ] PDF 上传 → 自动提取文本 → 存入 raw/
- [ ] Claude 完成 ingest：写 wiki 页面 + 更新 index + log
- [ ] React UI 收件箱审核流程
- [ ] Wiki markdown 浏览 + [[链接]] 跳转
- [ ] SSE 流式多轮对话
- [ ] 翻译功能
- [ ] 包含测试
