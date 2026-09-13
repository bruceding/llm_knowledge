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
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — 需在 PATH 中可用（默认 LLM 后端）
- **[pi](https://github.com/earendil-works/pi-coding-agent)**（可选）— 备选 LLM 后端，`npm install -g @earendil-works/pi-coding-agent`；另需 `pi-web-access` 扩展（pin `0.29.0`）。两者择一生效，由管理员在 Settings 里切换，详见[「LLM 后端切换」](#llm-后端切换claude--pi)
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
- 检查 **pi** 与 **pi-web-access** 是否就位（可选后端，缺失只告警不阻塞）
- 构建后端和前端
- 把 `path-validator.py` 与 `pi-path-validator.ts` 部署到运行时 `scripts/`（即 `LLM_SCRIPTS_DIR`）
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

## LLM 后端切换（claude / pi）

服务支持两种 LLM 后端，**同一时刻只有一个生效**，由管理员在 Settings →「LLM 后端」里切换（对应 `GET/PUT /api/admin/settings` 的 `llmBackend` 字段）。文档问答、自由问答、ingest 摘要与分节、PDF 逐页转换全部跟随同一个开关，不需要改代码或重启进程。

- 默认 `claude`；取值只有 `claude` 与 `pi`，其余一律 400
- 切到 `pi` 时服务端**先探测再保存**，不可用则返回 400 并保持原值
- 保存成功立即生效（resolver 缓存 5s，保存时主动失效）
- 切回 `claude` **不做探测**——它是 pi 坏掉时的逃生门，不能被探测堵住

### pi 后端的部署前提

1. `npm install -g @earendil-works/pi-coding-agent`（实测 0.85.1；其 `engines` 要求 **Node.js >= 22.19.0**）
2. 安装 `pi-web-access` 扩展并 **pin 版本**（实测 0.29.0）
3. 固化 `web-search.json`：把仓库里的 `backend/scripts/web-search.json.sample` 复制到
   `$PI_CODING_AGENT_DIR/web-search.json`；未设该环境变量时是
   `<服务账号 HOME>/.pi/agent/web-search.json`。
   **不是** `$XDG_CONFIG_HOME/pi/` —— pi 不读那里。
4. `pi-path-validator.ts` 由 `make build` 与 `start.sh` 自动复制到运行时 `scripts/`
   （即 `LLM_SCRIPTS_DIR`），无需手工部署。缺这个文件时 pi 后端是 **fail-closed** 的：
   切换时报 400、spawn 时拒绝启动，不会静默退化成"无沙箱执行"。

`start.sh` 会检查 pi 与 pi-web-access 是否就位并打印结果；缺失只告警不阻塞启动
（claude 后端不需要 pi）。

### 运维警示

**① 切换后端会作废进行中对话的上下文续接能力，而且是双向的。**
`chat_session_id` / `session_id` 存的是**该后端自己的** session ID，两种格式不通用。切换后旧对话无法 resume —— 历史消息仍在数据库里、仍可查看，只是接不上上下文。**「切回原后端就能恢复原对话」不成立**：切换期间新产生的消息是在另一个后端下进行的，两边的会话已经分叉。有进行中对话时不要切换。

**② 后端切换仅 `admin` 账号可操作，不要重命名或删除该账号。**
开关渲染在 `{isAdmin && ...}` 里，而 admin 判定来自 `users.role`。`db/db.go:38` 有一条**无守卫**的迁移：

```sql
UPDATE users SET role='admin'
 WHERE username='admin' AND (role IS NULL OR role='' OR role='user')
```

它只会把**用户名字面为 `admin`** 的行升回 admin。所以一旦把 admin 改名，这条迁移救不回来，UI 里就再没有人能看到后端开关（只能直接改数据库）。

**③ `--tools` 拦不住扩展的加载期代码。**
pi 的 `--tools` 只约束**工具调用**，而扩展被加载时就会执行其顶层代码。因此必须 pin `pi-web-access` 的版本，并管控 `settings.json` 的 `packages` 列表 —— 任何被加载的包都等于在服务进程里执行了它的代码。

**④ `web-search.json` 必须显式关掉扩展命令，否则任何用户都能执行扩展代码。**
rpc 模式会把**以 `/` 开头的用户消息当作扩展命令派发**。所以一条普通的文档问答消息（比如 `/search foo`）就能直接触发扩展命令。部署模板已经关掉了这四个：

```json
"commands": {
  "websearch":      { "enabled": false },
  "curator":        { "enabled": false },
  "search":         { "enabled": false },
  "google-account": { "enabled": false }
}
```

**默认值是全部启用**，所以「没有这个配置文件」不等于安全，恰恰是最危险的状态。

四道既有防线为何都拦不住：

| 防线 | 为什么无效 |
|---|---|
| `--tools` | 只管**工具调用**，命令派发不经过它 |
| `--no-skills` / `--no-prompt-templates` | 只关 skill 与 prompt 模板，与扩展命令无关 |
| 沙箱 extension 的 `input` hook | 在命令**派发之后**才触发，此时命令已在执行 |
| 服务端过滤消息内容 | 不能做——那会破坏正常以 `/` 开头的提问 |

其他已加载的包若也注册命令，只能靠 ③ 的运维隔离（pin 版本 + 管控 `packages`）覆盖。Go 侧启动时会校验这份配置并告警，Settings 里切到 pi 时也会拒绝有致命问题的配置。

**⑤ `toolNames` 写错会让整个 pi 后端 `exit 1`，不只是联网功能失效。**
pi-web-access 会在 `resolveToolNames` 上抛错 → 扩展加载失败 → pi 以退出码 1 退出。于是**连不用 web 工具的 ingest 链路（摘要、分节、PDF 转换）也一起死**。

关于探测能否发现它：`pi --version` **发现不了** —— 它在 pi 的 `main.js:483-486` 提前 `exit 0`，根本不加载扩展。所以服务端的切换探测除了 `pi --version`，还用 Go 侧同一份解析逻辑做**静态预检**（Go 与 pi-web-access 读的是同一个文件，见上面第 3 条），把 `toolNames` 的形状、取值、跨键重名问题在切换时就以 400 拦下。

静态预检**覆盖不到**：`pi-web-access` 根本没装、或**其他**包在加载期抛错。前者由 `start.sh` 提示，后者只能靠 ③ 的运维隔离。

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
