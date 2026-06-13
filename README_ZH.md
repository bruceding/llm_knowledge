# LLM Knowledge

[English](README.md)

一个自托管的个人知识管理工具，帮助你收集、理解和查询文档 — 单文件部署，开箱即用。

LLM Knowledge 支持导入 PDF、网页剪藏和 RSS，使用 Claude 提取和总结内容，并通过 SSE 流式对话与文档进行智能问答。

## 功能

- **多源导入** — 上传 PDF（拖拽或 URL）、通过 Chrome 插件剪藏网页、订阅 RSS 或博客自动同步、或通过 IMAP 导入 Newsletter
- **LLM 驱动提取** — Claude CLI 提取结构化内容、生成摘要、中英互译
- **文档对话** — 基于 SSE 的流式多轮问答，支持会话管理
- **知识库问答** — 自由对话式 AI，支持中断/停止、会话历史和图片输入
- **PDF 翻译** — 通过 pdf2zh 实现排版保留的 PDF 翻译（兼容 OpenAI API）
- **双 PDF 对比视图** — 原文与译文左右分屏，滚动/缩放同步
- **标签与 Wiki** — 给文档打标签，在 Wiki 视图中浏览整理后的内容
- **多用户账号** — 注册、登录与用户间数据隔离，基于 Token 的认证
- **移动端适配** — 专用的移动端壳层，手机/平板自带底部抽屉式对话
- **双语界面** — 完整的中英文国际化支持
- **单文件部署** — 前端嵌入 Go 二进制文件，下载即用

## 环境要求

- **Go** 1.25+
- **Node.js & npm**（用于构建前端）
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — 需在 PATH 中可用
- **Python 3.12**（可选）— 用于 pdf2zh PDF 翻译（需要 PEP 695 语法支持）
- **qpdf**（可选）— pdf2zh 的 pikepdf 依赖

## 快速开始

```bash
# 克隆并启动（默认端口 9999）
git clone https://github.com/bruceding/llm_knowledge.git
cd llm_knowledge
./start.sh
```

`start.sh` 启动脚本会自动：
- 检查并安装 **pdftotext**（poppler）用于 PDF 文本提取
- 检查 **Python 3.12** 是否可用（缺失时打印警告，PDF 翻译功能禁用）
- 检查并安装 **qpdf** 用于 pdf2zh 依赖
- 构建后端和前端
- 在端口 9999 启动服务

```bash
# 自定义端口
PORT=8080 ./start.sh

# 或手动构建运行
make build
./llm-knowledge -port 8080

# 开发模式（热重载）
make dev                 # 后端 :3456，前端 :5173
```

数据存储在 `~/.llm-knowledge/`（可通过 `DATA_DIR` 环境变量配置）。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `3456` | 服务端口 |
| `DATA_DIR` | `~/.llm-knowledge` | 数据和数据库目录 |
| `PDF2ZH_VENV_DIR` | `$DATA_DIR/.venv` | pdf2zh Python 虚拟环境路径 |

## 键盘快捷键

仅在桌面端浏览器生效，输入框聚焦时自动禁用。

### 文档详情（vim 风格滚动）

- `j` / `k` — 内容向下 / 向上滚动（长按加速）
- `g` / `G` — 跳到文档顶部 / 底部

### Inbox 与 Wiki 文档列表

- `d` — 删除鼠标当前悬停的文档（弹窗确认后执行）

### 对话（文档对话与知识库问答）

- `Enter` — 发送消息
- `Shift` + `Enter` — 插入换行

### 弹窗与搜索

- `Escape` — 关闭当前弹窗（如博客配置、确认对话框）
- `Enter` — PDF 阅读器中提交搜索 / 文档详情中添加标签

## 技术栈

- **后端:** Go + Echo + GORM (SQLite) + Claude CLI
- **前端:** React 19 + TypeScript + Vite + Tailwind CSS v4
- **PDF:** pdfjs-dist（浏览器渲染）+ pdf2zh（翻译）

## Chrome 插件 — Wiki Web Clipper

一键剪藏网页到你的知识库。支持 Chrome、Edge、Brave 等现代 Chromium 内核浏览器。

### 安装

1. 打开 Chrome，访问 `chrome://extensions/`
2. 开启右上角的 **开发者模式**
3. 点击 **加载已解压的扩展程序**，选择本项目中的 `extension/` 目录

### 配置

安装后点击插件图标打开设置页面：

1. 输入 **Wiki 地址**（如 `http://localhost:9999` 或你的部署地址）
2. 输入 **用户名** 和 **密码**
3. 点击 **保存并连接** 完成认证

插件会本地存储你的凭证，并在需要时自动刷新 Token。

### 使用

- 打开想要收藏的网页
- 点击浏览器工具栏中的插件图标
- 页面内容会被剪藏并发送到知识库的「原始文档」区域
- 成功：绿色 ✓ 标记 | 失败：红色 ✗ 标记 | 进行中：灰色 "..."
- 页面会弹出 Toast 提示显示操作结果

### 功能特性

- **全页面抓取** — 提取完整 HTML 内容，保留页面结构
- **自动标题识别** — 使用页面标题作为文档名称
- **微信公众号支持** — 特殊处理微信文章格式
- **认证管理** — 基于 Token 的安全认证，自动处理过期
- **状态反馈** — 通过 Badge 和 Toast 通知显示操作状态

### 支持的网站

适用于大多数公开网站。部分重度依赖 JavaScript 渲染的页面可能需要等待页面完全加载后再剪藏。

## Blog 订阅 — 索引页同步

通过博客的索引页/归档页 URL 订阅。服务端会抓取该页面（必要时使用无头浏览器渲染 JavaScript），自动识别常见平台、提取文章链接，并把最近的文章拉到收件箱。

### 配置

1. 进入界面中的 **导入 → Blog** 标签页
2. 填写博客的 **索引页 URL**（如 `https://example.com/blog`）以及可选的 **名称**
3. 可选开启 **自动同步**，定期后台拉取
4. 点击 **添加订阅**

如果博客属于已知平台（WordPress、Medium 风格等），直接生效。对于未识别的站点，会弹出配置对话框要求填写 CSS 选择器：

- **链接选择器** — 索引页上文章链接的 CSS 选择器（如 `article h2 a`）
- **内容选择器** — 文章正文的 CSS 选择器（如 `article .post-content`）
- **链接排除**（可选）— 需要忽略的链接选择器（如分页、标签）

### 使用

- **首次同步** 最多扫描 20 篇候选文章，按发布日期排序，导入最近 5 篇
- **后续同步** 仅抓取上次同步之后的新文章
- 点击每个订阅上的 **立即同步** 可手动拉取
- 通过共享的无头浏览器池支持 JavaScript 渲染的 SPA

### 功能特性

- **平台自动识别** — 常见博客平台开箱即用，无需配置选择器
- **SPA 支持** — 无头浏览器先渲染 JavaScript 再提取
- **智能去重** — 每个订阅独立记录已抓取的 URL，重复同步不会产生重复
- **后台自动同步** — 按订阅独立调度，与 RSS 同步并行运行

## Newsletter 导入 — IMAP 邮件同步

通过 IMAP 自动从邮箱导入 Newsletter，适合订阅技术周刊、行业更新和精选内容。

### 配置

1. 进入界面中的 **导入 → Newsletter** 标签页
2. 配置 IMAP 设置：
   - **Host**: IMAP 服务器地址（如 Gmail 使用 `imap.gmail.com`）
   - **Port**: `993`（IMAPS，推荐）或 `143`（IMAP）
   - **Username**: 邮箱地址
   - **Password**: 邮箱密码或应用专用密码
   - **Folder**: 邮箱文件夹名称（默认 `Newsletter`）
3. 开启 **自动同步** 可每小时自动拉取
4. 点击 **保存并连接**

### Gmail 设置

Gmail 需要使用 **应用专用密码** 而非常规密码：

1. 访问 [Google 账户安全设置](https://myaccount.google.com/security)
2. 开启 **两步验证**（应用密码的前提条件）
3. 进入 **应用密码** → 生成新密码
4. 选择「邮件」和「其他（自定义名称）」→ 命名为「LLM Knowledge」
5. 使用生成的 16 位密码进行配置

### 使用

- 点击 **立即同步** 手动拉取新 Newsletter
- 首次同步最多导入 10 条（避免内容过多）
- 后续同步仅拉取上次同步后的新邮件
- 开启自动同步后每小时自动执行

### 功能特性

- **HTML 提取** — 从 multipart 邮件中提取干净的 HTML 内容
- **图片处理** — 下载嵌入图片，过滤追踪像素
- **智能清理** — 移除重复标题、页脚噪音、退订链接
- **发送者标签** — 自动根据 Newsletter 来源创建标签
- **Claude 摘要** — 后台为每条 Newsletter 生成摘要
- **浏览器查看链接** — 提取并保留原始 Newsletter 链接

### 文件组织

Newsletter 存储在 `~/.llm-knowledge/raw/newsletter/<发送者>/`：
- `<slug>.md` — 带元数据头的 Markdown 版本
- `<slug>.html` — 原始 HTML，用于富文本渲染
- `assets/` — 下载的图片
