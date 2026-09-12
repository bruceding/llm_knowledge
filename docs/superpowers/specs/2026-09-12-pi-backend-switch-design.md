# Agent 后端可切换(claude / pi) — 设计文档

**日期:** 2026-09-12
**范围:** 新增 `backend/agent` 包 + `scripts/pi-path-validator.ts`;改造 `backend/claude`(client/session/stream/query_pool)、`backend/config`、`backend/db/models.go`、`backend/main.go`、`backend/api/admin_settings.go`、`backend/ingest`(sections/summary/pipeline)、`backend/api`(documents/query/raw/sections/translate)、`frontend/src`(types/SettingsPage/i18n)

## 背景

后端目前把 Claude Code CLI 当作唯一的 LLM 执行器,通过 `stream-json` 双向流驱动:

- `backend/claude/session.go` — `InteractiveSession` + `SessionPool`(文档问答)
- `backend/claude/query_pool.go` — `QuerySession` + `QuerySessionPool`(自由问答)
- `backend/claude/client.go` — 一次性调用(摘要/分节/翻译)
- `backend/claude/stream.go` — `StreamProcessor` 把 CLI 事件转成前端 `SSEEvent`
- `backend/claude/security.go` + `scripts/path-validator.py` — `ALLOWED_DIR` 沙箱

`cfg.ClaudeBin` 在 `main.go` 里约 10 处被注入到 handler 与连接池,池是启动时构造的长生命周期单例。

现状有三个已知包袱(记录在 `CLAUDE.md`):必须先发 init message 才能拿到 `session_id`;因此需要 `waitForInit` 5s 超时 + `local-<UnixNano>` fallback ID + `onRealSessionID` 别名回调;`stream.go` 里为 Qwen/GLM 经 Claude CLI 代理而写了三套去重逻辑。

[pi](https://github.com/earendil-works/pi) 提供 `--mode rpc`(双向 JSONL)与 `--mode json`(一次性事件流),原生多 provider,可以直接替换上述执行器。

## 目标

- 后端可在 **claude** 与 **pi** 之间切换,开关存放在管理员 Settings(DB),不走环境变量
- 切换覆盖**全部链路**:文档问答、自由问答、ingest(摘要/分节/讲解/翻译)
- 前端 `SSEEvent` 契约与所有 API 形状**保持不变**,前端聊天代码零改动
- pi 路径下保持与现有 `path-validator.py` **同等**的沙箱强度(fail-closed)
- claude 路径行为**完全不变**,作为可回退的默认值

## 非目标 / YAGNI

- 不删除 `backend/claude`,不做单向迁移
- 不引入 Node sidecar / 内嵌 pi SDK(见「已否决方案 D」)
- 不做每用户后端选择(仅全局管理员开关)
- 不改前端聊天组件、不改 `SSEEvent` 线格式
- 不采用 pi 的 `steer` / `follow_up` / `compact` / `fork` / `executeBash` 等额外能力
- 不做 pi 侧 token / cost 统计(`getSessionStats`)
- 不移植 `path-validator.py` 的 IP/DNS 级 SSRF 校验(改用 `pi-web-access` 自带的更强实现)
- 不给 `source_check` 工具,不开启 `allowBrowserCookies`,不启用 `fetch_content` 的本地视频能力
- 不修复 `backend/dependencies` 的孤儿状态(前端至今未消费 `/api/dependencies/status`),仅复用其探测手法
- 不动 `CLAUDE.md`

## 已锁定的决策

以下 8 项在 Plan Mode 阶段经结构化提问确认,本文档不再重新论证:

| # | 决策点 | 选择 |
|---|---|---|
| 1 | 落地范围 | 双后端可切换,保留 `backend/claude`,新增 pi 实现 |
| 2 | 沙箱落法 | 移植为 pi extension(`pi.on("tool_call") → {block:true}`) |
| 3 | provider/model | 沿用 pi 全局配置(`~/.pi/agent/settings.json` + `auth.json`),后端不传 `--provider`/`--model` |
| 4 | 开关层级 | `db.GlobalSettings`,管理员全局 |
| 5 | 生效时机 | 新建会话时生效(spawn 前读 DB,带缓存) |
| 6 | 覆盖链路 | 全部(两个 chat + ingest 一次性调用) |
| 7 | 旧 session ID | 忽略,依赖 `onResumeFailed` 自动降级为新会话 |
| 8 | 保存校验 | `PUT /api/admin/settings` 时探测 pi 可用性,不可用返回 400 |

## 方案对比

### 方案 A:Protocol 接口注入现有 InteractiveSession(采用)

新建 `backend/agent` 包定义 `Protocol` 接口,把 CLI 专有细节(旗标、env、stdin 编码、stdout 解析)全部收进两个实现。`InteractiveSession` 增加 `proto` 字段并委托调用。

- 池逻辑、SSE 连接计数、30s 清理循环、订阅扇出、`SSEEvent` 前端契约**全部不动**
- `StreamEvent` 提升为共享类型后,`claude.StreamEvent` 变为类型别名,所有 import 与 30+ 现有测试**零改动**
- pi 的 `message_update.assistantMessageEvent`(`text_delta`/`toolcall_start`/`toolcall_delta`/`toolcall_end` + `contentIndex`)与 Claude 的 `content_block_delta`(`text_delta`/`input_json_delta` + `index`)结构同构,归一化成本低
- 代价:需重构 `stream.go` 的 `Extract*` 系列改为消费归一化 `Delta`,并同步 `stream_test.go`

### 方案 B:SSEEvent 层做 seam,每后端自带 StreamProcessor(否决)

`stream.go` 可以完全不动,Qwen/GLM 去重逻辑原地保留。但 `query_pool.go` 的 `routeEvents()` 消费的是 `StreamEvent`,照样要改;且两套 processor 会重复实现 tool 状态机、SSE 重连恢复、pending flush,违反 DRY。

### 方案 C:进程外 adapter 把 pi JSONL 翻译成 Claude wire format(否决)

Go 侧零改动,但要伪造 Claude 的 `stream_event`/`content_block_delta` 嵌套结构,并模拟 `--resume` 语义、`session_id`、interrupt。多一个进程、多一层延迟,调试成本极高,违反 `CLAUDE.md` 第 2 条「简单优先」。

### 方案 D:Node sidecar 内嵌 pi SDK(否决,附重新评估触发条件)

调研 [`jmfederico/pi-web`](https://github.com/jmfederico/pi-web)(v1.202609.0,620 stars)后浮现的选项。pi-web **不 spawn pi 子进程**,而是把 pi 当库用:

```ts
import { createAgentSessionRuntime, createAgentSessionServices,
         createAgentSessionFromServices, SessionManager, ModelRuntime,
         ProjectTrustStore, SettingsManager, defineTool } from "@earendil-works/pi-coding-agent";
```

优势:一个 Node 进程托管 N 个 session,比 N 个 pi 子进程省内存;可拿到 RPC 拿不到的 `state.streamingMessage`(用于给重连客户端播种)与 `getSessionStats()`(token/cost)。

否决理由:

- 需新写 TS 服务 + 设计 Go↔Node IPC 协议,打破 README 卖点「Single binary — just download and run」
- `docs/rpc.md` 开宗明义即为非 Node 宿主而写(「useful for embedding the agent in other applications, IDEs, or custom UIs」),方案 A 走的是官方文档化路径
- 现状已是「每 session 一个 Claude CLI 子进程」,换成 pi 子进程属等价替换,资源模型不变
- 本项目有 30s 空闲清理,并发 session 量级不足以让 D 的内存优势显现

**注意一个常见误判**:「引入 Node 运行时」不构成否决 D 的理由。`pi` 本身就是 `#!/usr/bin/env node` 脚本(`~/.pi/agent/bin/` 仅含 `fd`、`rg`),**采用 pi 后 Node.js 无论如何都是硬运行时依赖**。

**重新评估触发条件**(满足任一则应重新考虑 D):

1. 并发 session 数或常驻内存成为实测瓶颈
2. 产品需要 token / cost 统计
3. 需要 `streamingMessage` 级别的重连体验(而非 `get_messages` 近似)

## 架构

### seam 位置

```
                     ┌─────────────────────────────────────┐
   api / ingest ───► │  claude.SessionPool / QuerySessionPool │  ← 不动
                     │  claude.InteractiveSession             │  ← 仅加 proto 字段并委托
                     │  claude.Client                         │  ← 仅加 Proto 字段并委托
                     │  claude.StreamProcessor                │  ← 改为消费 Delta
                     └───────────────┬─────────────────────┘
                                     │ agent.Protocol
                     ┌───────────────┴────────────────┐
                     │                                │
              agent.ClaudeProtocol              agent.PiProtocol
              (现有行为原样搬移)              (新增)
                     │                                │
              claude CLI 子进程                 pi --mode rpc 子进程
```

**理由:** `StreamEvent` 已经是归一化事件的雏形(`Type`/`Subtype`/`Content`/`Result`/`Error`/`SessionID`/`Message`),唯一的 Claude 专有泄漏点是 `Event json.RawMessage`(`stream_event` 原始载荷,被 `Extract*` 系列深挖)。把它替换为归一化的 `Delta *Delta`,seam 即收干净,且上层全部无感。

### `agent` 包

```go
package agent

type Backend string

const (
    BackendClaude Backend = "claude"
    BackendPi     Backend = "pi"
)

// Delta 是流式增量的归一化表示,替代原先直挖 Claude raw JSON 的做法。
type DeltaKind int

const (
    DeltaNone DeltaKind = iota
    DeltaText          // 文本增量
    DeltaToolStart     // 工具调用开始(带 ToolID + ToolName)
    DeltaToolInput     // 工具入参增量
    DeltaToolEnd       // 工具调用结束
)

type Delta struct {
    Kind      DeltaKind
    Text      string // DeltaText
    Index     int    // 内容块序号:Claude 的 index / pi 的 contentIndex
    ToolID    string
    ToolName  string
    ToolInput string
}

// 以下三个类型从 claude 包迁移到 agent,字段与 JSON tag 全部不变。
// claude 包内保留类型别名,使现有 import 与测试零改动:
//   type ImageData    = agent.ImageData
//   type StreamEvent  = agent.StreamEvent
//   type Message      = agent.Message
//   type ContentBlock = agent.ContentBlock
type ImageData struct {
    MediaType  string
    Base64Data string
}

// Message / ContentBlock 描述一条 assistant 消息的规范化形态。
// Claude 的 `assistant` 事件与 pi 的 `message_end` 事件都映射到它,
// 因此必须与 StreamEvent 同包,否则 agent ↔ claude 形成 import 循环。
type Message struct {
    Role    string         `json:"role"`
    Content []ContentBlock `json:"content"`
}

// 字段与 JSON tag 与现有 client.go:43-49 完全一致(注意 Text 无 omitempty)。
type ContentBlock struct {
    Type  string          `json:"type"`            // text, thinking, tool_use
    Text  string          `json:"text"`
    ID    string          `json:"id,omitempty"`
    Name  string          `json:"name,omitempty"`
    Input json.RawMessage `json:"input,omitempty"`
}

// StreamEvent 从 claude 包迁移为规范定义。字段与现有 client.go:21-34 逐一对应,
// 唯一变动是 Event json.RawMessage → Delta *Delta。
type StreamEvent struct {
    Type      string          // 保留
    Content   string          // 保留
    Subtype   string          // 保留
    SessionID string          // 保留
    Result    string          // 保留
    Error     string          // 保留
    ToolName  string          // 保留(见下方说明)
    ToolInput string          // 保留(见下方说明)
    Message   *Message        // 保留
    Delta     *Delta          // 新增,取代原 Event json.RawMessage

    // 以下两个不是 wire 字段,而是 query_pool.go 的 routeEvents 写入的路由元数据
    // (现 query_pool.go:64-65),用于把 assistant 回复存回 DB。必须原样保留。
    ResultMessageID   uint
    ResultFullContent string
}

**两个注意事项:**

1. `ToolName` / `ToolInput` 在现有代码里**从未被赋值**(`stream.go` 里的同名字段属于 `SSEEvent`,不是 `StreamEvent`)。属既有死字段,按 `CLAUDE.md` 第 3 条「提到但不删」原样搬迁,本次不清理。
2. `ResultMessageID` / `ResultFullContent` 由上层(`query_pool`)而非解析器写入,两个 `Protocol` 实现都**不应**触碰它们。

// Protocol 收拢两个 CLI 的全部差异。
type Protocol interface {
    Backend() Backend
    Bin() string

    // 旗标与环境
    SessionArgs(sysPrompt string, tools []string) ([]string, error)
    ResumeArgs(prevSessionID, sysPrompt string, tools []string) ([]string, error)
    OnceArgs(sysPrompt string, tools []string, print bool) ([]string, error)
    Env(allowedDir string) []string

    // stdin 编码
    EncodeUserMessage(content string, images []ImageData) ([]byte, error)
    EncodeInterrupt() ([]byte, error)

    // stdout 解析:一行 JSONL → 归一化事件。ok=false 表示该行应跳过。
    ParseLine(line []byte) (evt StreamEvent, ok bool)

    // 会话可用性探测(Settings 保存时调用)
    Probe(ctx context.Context) error
}
```

**为何 `Message`/`ContentBlock` 必须一起上移:** `StreamEvent.Message` 是指向它的指针字段。若 `StreamEvent` 在 `agent` 而 `Message` 留在 `claude`,则 `agent` 需 import `claude`;而 `claude` 为了提供 `type StreamEvent = agent.StreamEvent` 别名又需 import `agent` —— 循环。故四个类型(`ImageData`/`StreamEvent`/`Message`/`ContentBlock`)一并上移,`claude` 侧全部改为别名。

两套 CLI 各自的 wire shape(`RawEvent`、pi 的 `message_update` 嵌套结构)**不**上移,分别留在 `ClaudeProtocol` / `PiProtocol` 内部,由各自 `ParseLine` 解析后填充归一化的 `Message` 与 `Delta`。

### 解析器后端差异

| 语义 | `ClaudeProtocol.ParseLine` | `PiProtocol.ParseLine` |
|---|---|---|
| session_id | `type=system` + `subtype=init` → `SessionID` | 启动后主动发 `{"type":"get_state"}`,从 `response.data.sessionId` 取 |
| 文本增量 | `type=stream_event` → `event.content_block_delta.text_delta` | `type=message_update` → `assistantMessageEvent.type=text_delta` |
| 工具开始 | `content_block_start` + `content_block.type=tool_use` | `assistantMessageEvent.type=toolcall_start`(带 `id`/`toolName`) |
| 工具入参 | `content_block_delta.input_json_delta.partial_json` | `assistantMessageEvent.type=toolcall_delta` → `delta` |
| 工具结束 | `content_block_stop` | `assistantMessageEvent.type=toolcall_end`,或 `tool_execution_end` |
| 完整消息 | `type=assistant` → `message.content[]` | `type=message_end` → `message` |
| 轮次结束 | `type=result`(`is_error` → error) | `type=agent_end`;`agent_settled` 作为最终静默信号 |
| 错误 | `result` + `is_error=true` | `type=response` + `success=false` → `error`;`extension_error` |
| 忽略 | `type=system`(除 init) | `type=session`(头行,已用于取 id)、`turn_start`、`queue_update`、`compaction_*`、`auto_retry_*` |

**JSONL framing:** 只按 `\n` 切分,容忍并剥除行尾 `\r`。`docs/rpc.md` 明确警告不要用会按 `U+2028`/`U+2029` 切分的通用行读取器——Go 的 `bufio.Scanner`(`ScanLines`)已合规,`newScanner` 的 1MB buffer 保留(pi 的 `tool_execution_update.partialResult` 是累积快照,大输出同样会撑爆行)。

### pi 进程配方

会话(rpc):

```
pi --mode rpc
   --tools <allowlist>
   --no-skills --no-prompt-templates --no-context-files
   -e <repo>/scripts/pi-path-validator.ts
   -na
   --session-dir <userDir>/.pi-sessions
   [--session <prevPiSessionID>]
   [--system-prompt <prompt>]
```

一次性(json / print):同上硬化旗标,把 `--mode rpc` 换成 `--mode json`(需要事件流)或 `-p`(只需纯文本);prompt 走 stdin(pi 的 `-p` 会读取管道 stdin 并合并进初始 prompt)。

`cwd = userDir`,`env += ALLOWED_DIR=<filepath.EvalSymlinks(userDir)>`。

**每个旗标的理由:**

| 旗标 | 理由 |
|---|---|
| `--tools <list>` | 白名单。四档:文档问答 `read` + web 工具(现 `BuildSecureArgs([]string{"Read"})`);自由问答 `read,find,grep,ls` + web 工具(现 `[]string{"Read","Glob","Grep","LS"}`);ingest 的 `Send`/`SendWithTools` 用 `read,write,edit`(**不给** web 工具,现 `[]string{"Read","Write","Edit"}`),`SendSimpleWithRead` 用 `read` |
| **不用** `--no-extensions` | 联网能力来自 `pi-web-access` 包(见下小节),关掉扩展发现会连带关掉它。而用 `-e npm:pi-web-access` 显式加载也不可行:`docs/packages.md:45` 明写该形式 "installs to a temporary directory for the current run only",在「每 session 一个子进程」模型下等于每次开会话都重装。因此改为依赖全局已装的包 + `--tools` 白名单收口 |
| `--no-skills` / `--no-prompt-templates` | 同上,缩小可被 `/命令` 触发的面 |
| `--no-context-files` | 阻止 `userDir` 内的 `AGENTS.md`/`CLAUDE.md` 注入系统提示(用户上传内容不得影响指令) |
| `-e <validator.ts>` | 本地路径,不触发安装;与 `--tools` 共同构成「只有白名单内的工具能被调用,且每次调用都过沙箱」 |
| `-na` / `--no-approve` | 忽略项目本地 settings 与扩展,避免 `userDir` 内的 `.pi/` 被信任 |
| `--session-dir` | 每用户会话存储隔离,避免跨用户串号 |
| **不需要** `--dangerously-skip-permissions` | pi 内置工具无权限询问,该危险旗标可整体去掉 |
| **不需要** `--verbose` | pi 无此要求 |

工具名映射:`Read→read`、`Glob→find`、`Grep→grep`、`LS→ls`、`Write→write`、`Edit→edit`。pi 内置工具仅 `read/bash/powershell/edit/write/grep/find/ls`,不存在 `Task`/`NotebookEdit`/`KillShell`/`BashOutput`/`SlashCommand` 的对应物;`bash` 由白名单直接排除(等价于现在的 `--disallowedTools` 硬阻断)。

`PI_CODING_AGENT_DIR`(auth/models 配置目录)**保持全局共享**,不做每用户隔离——服务端 provider 凭据不应按用户拆分。

### 联网能力:`pi-web-access`

doc chat 需要联网查证。联网能力**不是 pi 内置**(pi 内置工具仅 `read/bash/powershell/edit/write/grep/find/ls`,其 `dist` 里搜不到 `web_search`),而是来自 npm 包 **`pi-web-access`**(当前 v0.29.0),通过 `~/.pi/agent/settings.json` 的 `packages: ["npm:pi-web-access"]` 安装。

它注册 4 个工具,各自可单独开关(`isToolEnabled(initConfig, "webSearch"|"sourceCheck"|"fetchContent"|"getSearchContent")`):

| 工具 | 默认名 | 本设计 |
|---|---|---|
| webSearch | `web_search` | ✅ 给 |
| fetchContent | `fetch_content` | ✅ 给 |
| getSearchContent | `get_search_content` | ✅ 给——前两者的配套(分页取回已抓内容),不给则大结果无法阅读 |
| sourceCheck | `source_check` | ❌ 不给——研究场景专用,doc chat 用不上;少一个需校验 URL 的入口 |

**工具名可被 `web-search.json` 的 `toolNames` 改写**,所以不能在各处硬编码字面量。单一事实来源:Go 侧从同一份配置解析出名称,一路用于 `--tools` 白名单,另一路经 env(`PI_WEB_TOOLS=web_search,fetch_content,get_search_content`)传给沙箱 extension。两边同源,避免漂移。

**残留风险(必须写入部署文档):** `--tools` 只能限制工具**调用**,拦不住扩展**加载期**的任意代码。`docs/packages.md:20` 原文警告:*"Pi packages run with full system access. Extensions execute arbitrary code, and skills can instruct the model to perform any action including running executables."* 缓解手段:pin `pi-web-access` 版本、用 `pi config` 关掉其他包扩展、运维侧管控 `~/.pi/agent/settings.json` 的 `packages` 列表。

### 沙箱 extension:`scripts/pi-path-validator.ts`

职责比 `path-validator.py` **多一项**:除路径沙箱外,还必须校验 `fetch_content` 的 URL。

```ts
pi.on("tool_call", async (event, ctx) => {
  // 1. 白名单外的工具一律 block(等价 ALWAYS_DENIED_TOOLS 兜底)
  //    白名单 = 文件工具 ∪ env PI_WEB_TOOLS 传来的 web 工具名
  // 2. 文件工具(read/grep/find/ls/write/edit):提取路径 → realpath →
  //    必须落在 ALLOWED_DIR 内(分隔符边界比较,防 /u/1 匹配 /u/10);
  //    命中敏感路径正则则 block(/etc/shadow、~/.ssh、~/.aws、Keychains 等,含 macOS /private 前缀)
  // 3. fetch_content:校验 url 与 urls[] 全部元素——仅允许 http:/https:,
  //    拒绝本地路径(绝对/相对)与 file:/data:/gopher:/ftp: 等一切其他 scheme
  // 4. ALLOWED_DIR 未设置 → 文件工具全部 block(fail-closed)
  return { block: true, reason: "Access denied: ..." };
});
```

#### 为何必须管 `fetch_content` 的 URL

这是本设计**新增的关键控制**,`path-validator.py` 里没有对应物。该工具描述明写 *"Supports YouTube transcripts, GitHub repositories, PDFs, and **local videos**"*,而实现里:

- `video-extract.ts:337` — `readFile(info.absolutePath)`,**任意绝对路径读取**
- `video-extract.ts:213` — `execFileSync("ffmpeg", [...])`,**带该路径 spawn 进程**

这是一条**绕过 `ALLOWED_DIR` 沙箱**的本地文件读取 + 进程执行向量。`--tools` 白名单拦不住它——白名单只决定工具能不能被调,不校验参数。且 pi-web-access **没有「只关本地视频」的开关**(`isToolEnabled` 是整工具粒度,`video.maxSizeMB` 仅是大小上限),所以只能在我们自己的 hook 里堵。拒绝本地路径后,`timestamp`/`frames` 等视频参数也就够不着本地文件了。

#### SSRF:不移植,改为依赖并配置 pi-web-access 自带的防护

理由**不是**「无联网工具所以不需要」——联网工具确实存在。真实理由是 `pi-web-access/ssrf-protection.ts`(530 行)**比 `path-validator.py` 更严**:

| 能力 | pi-web-access | path-validator.py |
|---|---|---|
| 默认策略 | `assertPublicAddress` fail-closed;`loadSsrfConfig()` 无配置时返回 `{allowRanges:[], trustEnvProxy:false}` | fail-closed |
| 重定向跟随 | ✅ 重定向目标**永不继承** `allowLoopback`(源码注释明确) | ❌ 无 |
| DNS rebinding | 每次 fetch 前校验 | ❌ 自己承认 "can still be raced" |
| 域名策略 | `DomainPolicy{allow,deny}`,deny 先判 | ❌ 无 |

再叠一层我们自己的 IP/DNS 校验只会重复且互相遮蔽。因此**分工明确**:我们的 hook 只管「是不是远端 URL」,pi-web-access 管「远端 URL 是不是指向内网」。

#### 部署侧必须固化的 `web-search.json` 键

| 键 | 值 | 理由 |
|---|---|---|
| `allowBrowserCookies` | `false` | **必须钉死**。`chrome-cookies.ts:179-181` 会将浏览器 cookie SQLite 库 `copyFileSync` 到临时目录再读(含 `-wal`/`-shm` sidecar),配合 `rookie-cookies-darwin-arm64` 原生依赖解密。默认已关(`chrome-cookies.ts:116`),但多租户服务器上一旦开启,任何用户的 doc chat 都能外泄运维者本人的浏览器 cookie |
| `ssrf.allowRanges` | `[]` | 虽是默认值,显式写出防误配 |
| `ssrf.trustEnvProxy` | `false` | 同上 |
| `sourceCheck` 工具开关 | 关 | 与 `--tools` 白名单保持一致 |
| `fetchContent.deny` / `.allow` | 按运维需求 | 可选的域名级收紧 |

#### 其他要点

- `ALLOWED_DIR` 从 `process.env` 读取,由 Go 侧 `Env()` 注入(与现有 `BuildSecureEnv` 的 realpath 解析行为一致,否则 macOS `/tmp` → `/private/tmp` 会全量误拒)
- web 工具名从 `PI_WEB_TOOLS` env 读取,**不硬编码**(可被 `toolNames` 配置改写)
- 敏感路径正则表与 `path-validator.py` 保持**逐条同步**,由跨语言同步测试守护(见「测试」)

### Settings 与生效时机

`db.GlobalSettings` 新增字段:

```go
LLMBackend string `gorm:"default:claude" json:"llmBackend"` // "claude" | "pi"
```

`AutoMigrate` 自动加列,无需数据迁移(既有 `MigrateTranslationToGlobal` 是一次性数据搬迁,本字段不涉及)。

新增 `agent` 包级解析器,满足「新建会话时生效」:

```go
func Init(claudeBin, piBin string)          // main.go 启动时调用一次
func Current() (Protocol, error)            // 读缓存的 GlobalSettings.LLMBackend
func Invalidate()                           // PUT 保存成功后调用
```

`Current()` 用 5s TTL 缓存 + `Invalidate()` 主动失效,避免每次 spawn 都打一次 DB。未知/空值一律回退 `claude`(fail-safe 到既有行为)。

调用点改造:`SessionPool.StartSession`、`QuerySessionPool.GetOrCreate`/`GetOrResume`/`ResumeSession`、`StartSession`/`StartResumedSession`,以及 `ingest` 的一次性调用,全部在 **spawn 前**调 `agent.Current()`。已运行的会话继续用旧后端,直到 SSE 断开 30s 后被既有清理循环回收——**不主动踢会话**。

`main.go` 随之停止注入 `cfg.ClaudeBin`(现存 11 处:214、225、234、258、261、276、313、315、335、348、363 行);各 handler 的 `ClaudeBin string` 字段删除,改为调用时 `agent.Current()`。`config.go` 保留 `ClaudeBin` 并新增 `PiBin`(env `PI_BIN`,默认 `"pi"`),二者只在 `agent.Init` 时使用。

### Admin API

`GET /api/admin/settings` 响应增加 `llmBackend`。

`PUT /api/admin/settings` 输入结构增加 `LLMBackend string \`json:"llmBackend"\``,沿用既有部分更新风格(空字符串表示不改):

- 取值必须是 `"claude"` 或 `"pi"`,否则 400
- 取值为 `"pi"` 时执行探测:`exec.LookPath(piBin)` + `pi --version`(5s 超时)。失败返回 400,消息含安装指引(`npm install -g @earendil-works/pi-coding-agent`)
- 保存成功后调用 `agent.Invalidate()`

探测手法参照 `backend/dependencies/checker.go` 的 `checkClaudeCLI`,但**不**把 pi 加进 checker、**不**改前端对 `/api/dependencies/status` 的消费(该接口至今无人调用,属既有孤儿功能,本次不扩大范围)。

### 前端

- `types.ts` 的 `GlobalSettings` 增加 `llmBackend: string`
- `SettingsPage.tsx` 在既有 `{isAdmin && (...)}` 区块内,按「Global Translation Section (Admin Only)」的现有范式新增一个 `<select>`(claude / pi)+ 保存按钮,复用 `handleGlobalTranslationSave` 的错误提示模式
- i18n 补 en / zh 文案(标签、说明、探测失败的错误提示)
- **聊天相关组件零改动**:`SSEEvent` 线格式不变

### 旧 session ID 处理

DB 中有两个**后端无关的裸 string 字段**承载会话 ID:

| 位置 | 字段 | 用途 |
|---|---|---|
| `db/models.go:22` | `Document.ChatSessionID` | doc chat resume |
| `db/models.go:48` | `Conversation.SessionID` | query chat resume |

回调链同样后端无关:`session.go:242-250` 的 `onSessionID` → `onRealSessionID(oldID, newID)` → API 层写 DB(`api/docchat.go:76`、`api/query.go:132`);`query_pool.go:384-385` 同理。这条链只认「真实 session ID 到了」,不认它是哪个后端发的。

**因此 pi 产生的 session ID 会沿同一条链正常存入 DB,并在后续 resume 时复用:**

```
spawn pi --mode rpc
  → {"type":"get_state"} → response.data.sessionId
  → onSessionID(oldID, newID) → 写入同一个 DB 字段
  → 下次 prevSessionID 取到它 → --session <piSessionId> → resume 成功
```

这也正是决策 7「不加 `pi_session_id` 列」成立的原因:单个 string 字段存的是「当前后端的会话 ID」,语义天然随开关走。

**唯一失效的是切换那一刻的存量 ID。** 按决策 7:

- claude → pi 时,DB 里的 Claude UUID 对 pi 无意义,`--session <claude-uuid>` 会导致进程启动即失败(拿不到 session 头行 / `get_state` 响应)
- `PiProtocol` 检测到「进程退出且从未产出 session_id」时,触发既有 `onResumeFailed` 回调 → 清掉缓存 ID → 下次开新会话
- 不做 schema 变更,不加列,不批量清空

**边界:作废是双向的。** 因为只有一个字段:

- `pi → claude` 时,存量 pi ID 对 Claude 同样无意义,走同一条 `onResumeFailed` 降级路径
- 且降级后新后端产生的 ID 会**覆盖**旧值,所以再切回去时旧 ID 已不存在——**「切回原后端可恢复原对话」不成立**

运维需知:切换后端会使所有进行中的对话丢失上下文续接能力(历史消息仍在 DB,只是 agent 侧的会话上下文断开)。切换是管理员显式操作,该代价可接受。

**连带修改:** `db/models.go:22` 与 `:48` 的注释目前写着 "Claude session ID for ... --resume",改造后成为误导。既然本次在语义上复用了这两个字段,注释应改为后端中立:`agent session ID (claude or pi) for resume`。这属本次改动直接产生的陈旧注释,按 `CLAUDE.md` 第 3 条「清理自己造成的 mess」应一并改掉——**仅改注释,不改字段名与 JSON tag**,避免波及前端。

## 资源与运维影响

- **Node.js 成为硬依赖**:`pi` 是 `#!/usr/bin/env node` 脚本。版本下限取 pi-web 的实测约束:**Node >= 22.19.0**,**pi >= 0.85.1**(pi-web 的 peerDependency 为 `>=0.84.0 <0.85.0 || >=0.85.1`,精确排除 0.85.0)
- **provider 凭据**:服务器需有 `~/.pi/agent/auth.json` 或对应 API key 环境变量,替代原先的 `claude login` 态
- **进程模型不变**:仍是每 session 一个子进程,内存特征与现状等价
- **`pi-web-access` 成为 pi 路径的必需包**:doc chat 的联网能力来自它,不是 pi 内置。版本应 pin(当前 v0.29.0)。依赖树含 `undici`、`unpdf`、`linkedom`、`turndown`、`defuddle`、`@mozilla/readability`;全局 npm 目录里还有 `playwright-core`/`patchright-core`/`betterwright`(curator 浏览器用)。启动内存与磁盘占用比「纯 pi」重不少
- **`start.sh`**:补 `command -v pi` 检查与 `/opt/homebrew/bin` 到 PATH(Apple Silicon 上 npm 全局 bin 在此,而脚本目前只补了 `/usr/local/bin`);并检查 `pi-web-access` 是否已装
- **README**:Prerequisites 增加 pi 与 Node 版本要求、`pi-web-access` 包,以及 `web-search.json` 必须固化的安全键(尤其 `allowBrowserCookies: false`);说明 claude / pi 按 Settings 开关择一生效

### 管理员权限模型与锁死路径

`llmBackend` 挂在 `GlobalSettings` + `/api/admin/settings`(属 `main.go:286` 的 `adminGroup`),前端 `SettingsPage.tsx` 的 `{isAdmin && (...)}` 区块对普通用户隐藏。权限判定**不看用户名,只看 role**(`api/middleware.go:79-82`:`role == "admin"`;role 由 `AuthMiddleware` 从 DB 查出后缓存进 context)。

但现有权限模型有两个既存特征,直接影响本开关的可运维性:

1. **全仓无任何提权途径。** 写 `User.Role` 的地方只有两处:`db/db.go:38` 与 `db/db.go:59`(bootstrap)。`api/auth.go` 里完全没有 `admin` 字样——注册接口不能指定 role,也没有改 role 的 API。(`api/query.go:104` 的 `Role:"user"` 是 `Message.Role`,聊天消息角色,与权限无关。)

2. **`admin` 这个用户名字面量拥有硬编码特权。** `db/db.go:38` 每次启动**无条件**执行:

   ```go
   DB.Model(&User{}).Where("username = ? AND (role IS NULL OR role = '' OR role = 'user')", "admin").Update("role", "admin")
   ```

   它**没有「只跑一次」的守卫**——对比紧邻的 `MigrateTranslationToGlobal()` 就有 `Count(&count)` 早退。因此手动把 `admin` 降级为普通用户,重启后会被自动升回。

**锁死路径(需在部署文档中警示):** 切换后端是全局操作、影响所有用户,而能执行的只有单个 `admin` 账号;该账号的 admin 身份又由上述「用户名字面量 == `admin`」这条无守卫迁移维持。若部署时把默认 `admin` 改名或删除,就**再也无法通过 UI 切换后端**,只能直接改 DB 的 `users.role` 字段,或把用户名改回 `admin` 后重启。

**本设计不修复上述权限模型**(超出范围,且属既有行为),仅要求:

- README / 部署文档写明「后端切换仅 `admin` 账号可操作,且不要重命名或删除该账号」
- `PUT /api/admin/settings` 的 403 与 400 错误消息需可区分(未登录/非管理员 vs 取值非法或 pi 不可用),便于定位到底是权限问题还是探测失败

附:bootstrap 时随机密码会明文进日志(`db/db.go:73`,`log.Printf("Created default user 'admin', password: %s", ...)`)。属既有行为,本次**只提不改**。

## 测试

### 回归闸门(必须先绿)

`stream_test.go` 现有 30+ 测试覆盖 Claude / GLM / Qwen 三条路径与 SSE 重连,是 `Delta` 归一化重构的安全网。重构前先跑通并记录基线:

```bash
cd backend && go build ./... && go vet ./... && go test ./...
```

### 新增单元测试

- `agent/claude_protocol_test.go` — 现有 `stream_event`/`assistant`/`result`/`system.init` 解析行为迁移后逐条等价(用例从 `stream_test.go` 的 `TestExtract*` 平移)
- `agent/pi_protocol_test.go` — 表驱动:`session` 头行、`get_state` 响应取 sessionId、`message_update` 四种 `assistantMessageEvent`、`message_end`、`agent_end`、`agent_settled`、`response`+`success:false`、`extension_error`、畸形行跳过、行尾 `\r` 剥除
- `agent/pi_args_test.go` — 硬化旗标齐全(`--no-skills`/`--no-prompt-templates`/`--no-context-files`/`-na`/`-e`/`--session-dir`);**断言不含** `--no-extensions`(否则 `pi-web-access` 加载不了)、`--dangerously-skip-permissions`、`--verbose`;工具名映射正确;四档白名单各自正确(两个 chat 档**含** web 工具、两个 ingest 档**不含**);web 工具名取自配置而非硬编码字面量;resume 分支
- `agent/pi_message_test.go` — `EncodeUserMessage` 纯文本与带图(`images:[{type:"image",data,mimeType}]`,注意与 Claude 的 `content:[{type:image,source:{media_type,data}}]` 不同)、`EncodeInterrupt` 产出 `{"type":"abort"}`
- `agent/resolver_test.go` — TTL 缓存命中、`Invalidate()` 后立即重读、未知值/空值回退 `claude`
- `agent/probe_test.go` — `Probe` 对不存在的二进制返回错误(参照既有 `TestSendSimple_NonExistentBinary`)

### 跨语言同步测试(扩展既有)

`security_test.go` 已有 `TestDangerousToolsCrossLanguageSync`,校验 Go 的 `DangerousDisallowedTools` 与 Python 的 `ALWAYS_DENIED_TOOLS` 一致。加入 TS extension 后变为**三种语言**,该测试需扩展为同时解析 `scripts/pi-path-validator.ts` 的拒绝集合与敏感路径正则表,任一漂移即失败。

同文件既有 `TestPathValidator_WebFetchSSRF` 以 shell 方式驱动 Python 校验器;为 TS extension 补两组用例:

**路径边界**(与 Python 版等价):允许 `ALLOWED_DIR` 内、拒绝目录外、拒绝 `/data/users/1` 前缀碰撞 `/data/users/10`、拒绝敏感路径、`ALLOWED_DIR` 未设置时全拒。

**`fetch_content` URL 校验**(Python 版无对应物,为新增向量新增):接受 `http:`/`https:`;拒绝绝对本地路径(`/etc/passwd`)、相对路径(`../../x.mp4`)、`file:`、`data:`、`gopher:`、`ftp:`;`urls[]` 中**任一**元素非法即整体 block;空 `url` 与空 `urls` 的处理与 Python 版 `validate_webfetch_url` 一致(拒绝)。

注意:**不**为 TS extension 写 IP/DNS 级 SSRF 用例——那是 `pi-web-access/ssrf-protection.ts` 的职责(理由见沙箱小节)。但要加一条**配置一致性测试**:断言部署用的 `web-search.json` 样例里 `allowBrowserCookies` 为 `false`、`ssrf.allowRanges` 为空、`sourceCheck` 已关,防止运维模板静默漂移。

### API 测试

- `PUT /api/admin/settings` 带 `llmBackend:"pi"` 且 pi 不可用 → 400
- 带非法值(如 `"gemini"`)→ 400
- 带 `"claude"` → 200,且不触发 pi 探测
- `GET` 响应含 `llmBackend`

### 集成 / e2e

- Go 集成测试:spawn 真实 `pi --mode rpc`,发 prompt 要求读取 `ALLOWED_DIR` 外文件,断言工具调用被 block;`pi` 不在 PATH 时 `t.Skip`
- **本地文件向量集成测试**(本次新增控制的核心验证;`pi-web-access` 未装时 `t.Skip`):发 prompt 诱导 `fetch_content` 取 `ALLOWED_DIR` 外的本地路径(绝对路径视频 / `/etc/passwd`),断言被 hook block,且 `video-extract.ts` 的 `readFile(absolutePath)` 与 `execFileSync("ffmpeg", ...)` 未被触达
- 既有 Playwright e2e 作为端到端回归:`tests/e2e/test_chat_streaming.py`、`test_document_chat_panel.py`、`test_mobile_doc_translate.py`
- 手工验收(两种后端各跑一遍):文档问答多轮 + SSE 断线重连、自由问答带图片、中途 interrupt、ingest 摘要与分节
- **resume 往返验收**(两种后端各跑一遍,验证本节声称的 DB 复用):第一轮对话后查 DB 确认 `chat_session_id` / `session_id` 已写入**该后端自己的** ID 格式;重启进程或等会话被 30s 清理后再次提问,确认走 `--resume` / `--session` 且上下文续接成功(而非静默开新会话)

### 完成定义

1. `LLMBackend=claude` 时,`go test ./...` 全绿且行为与改造前逐条等价
2. `LLMBackend=pi` 时,上述手工验收全部通过
3. pi 路径下沙箱强度不低于 Python 版(fail-closed + 边界 + 敏感路径三项用例全过),**且 `fetch_content` 的本地文件向量被堵住**(URL 校验用例 + 集成测试全过)
4. 前端聊天代码 diff 为空
5. Settings 切换到不可用的 pi 时被 400 拦住

## 涉及文件

**新增**

- `backend/agent/agent.go` — `Backend`、`Delta`、`StreamEvent`、`ImageData`、`Protocol`
- `backend/agent/claude.go` — `ClaudeProtocol`(现有旗标/编码/解析逻辑搬移)
- `backend/agent/pi.go` — `PiProtocol`;`Env()` 除 `ALLOWED_DIR` 外还注入 `PI_WEB_TOOLS`(与 `--tools` 同源)
- `backend/agent/resolver.go` — `Init` / `Current` / `Invalidate` + TTL 缓存
- `scripts/pi-path-validator.ts` — 沙箱 extension(路径校验 + `fetch_content` URL 校验)
- `scripts/web-search.json.sample` — 部署模板,固化 `allowBrowserCookies:false`、`ssrf.allowRanges:[]`、`ssrf.trustEnvProxy:false`、`sourceCheck` 关;由配置一致性测试断言
- `backend/agent/*_test.go` — 见「测试」

**修改**

- `backend/claude/client.go` — `StreamEvent`/`Message`/`ContentBlock`/`ImageData` 改类型别名;`Client` 增 `Proto` 字段并委托;`RawEvent` 保留为 Claude 专有解析中间类型
- `backend/claude/session.go` — `InteractiveSession` 增 `proto`;`readEvents`/`SendUserMessage`/`SendUserMessageWithImages`/`SendInterrupt`/`buildCmdWithEnv` 委托;`StartSession`/`StartResumedSession` 改用 `agent.Current()`;pi 路径下 `waitForInit` 改为 `get_state` 同步取 id
- `backend/claude/stream.go` — `Extract*` 系列改为消费 `Delta`;`SSEEvent` 与前端契约不变
- `backend/claude/query_pool.go` — 两个 Start 函数改用 `agent.Current()` 与 `Protocol`
- `backend/claude/security.go` — 保留 claude 专用;`DangerousDisallowedTools` 不动
- `backend/config/config.go` — 新增 `PiBin`;新增 web 工具名解析(读 `web-search.json` 的 `toolNames`,缺省用默认名),作为 `--tools` 与 `PI_WEB_TOOLS` 的单一事实来源
- `backend/db/models.go` — `GlobalSettings` 新增 `LLMBackend`;`Document.ChatSessionID`(:22) 与 `Conversation.SessionID`(:48) 的注释改为后端中立(仅注释,不动字段名与 JSON tag)
- `backend/api/admin_settings.go` — 响应/输入/校验/探测/`Invalidate()`
- `backend/main.go` — 调 `agent.Init(cfg.ClaudeBin, cfg.PiBin)`;移除 11 处 `ClaudeBin` 注入
- `backend/ingest/{sections,summary,pipeline}.go` — `claudeBin string` 形参改为调用 `agent.Current()`
- `backend/api/{documents,query,raw,sections,translate}.go` — 删除 `ClaudeBin` 字段
- `frontend/src/types.ts`、`frontend/src/components/SettingsPage.tsx`、i18n en/zh 资源
- `start.sh`、`README.md` / `README_ZH.md` Prerequisites

## 开放项

1. **pi-web 的会话常驻模型是否值得借鉴**:pi-web 拆 `sessiond` + `web` 两个服务,浏览器断开与 UI 重启都不停 agent;我们是 SSE 断开 30s 即清理。本设计**不采用**常驻模型(改动过大且与现有资源回收策略冲突),但如果后续出现「长任务被 30s 清理误杀」的实际反馈,应重新评估。
2. **SSE 重连恢复的简化**:pi-web 用 `state.streamingMessage` 给新客户端播种。RPC 的 `get_state` 不暴露该字段,但 `get_messages` 返回全部消息,重连时取最后一条 assistant 消息即可替代现有 `streamingContent` 累积 + `sseReconnectContent` 去重。**本次不做**,留作 pi 路径稳定后的独立简化项,以免与归一化重构叠加风险。
3. **协议演进风险**:`docs/rpc.md` 明确记载 `message_update` 曾移除累积 `message` 字段与 `assistantMessageEvent.partial`,说明存在破坏性变更历史。需在部署文档中 pin pi 版本,并让 `ParseLine` 对未知事件类型静默跳过(而非报错),以降低升级冲击。

## 附录:pi-web 调研要点

| 项 | 值 |
|---|---|
| 包名 / 版本 | `@jmfederico/pi-web` v1.202609.0,MIT,620 stars |
| 集成方式 | **进程内嵌 SDK**,不 spawn `pi` 子进程(全仓 `spawn`/`execFile` 仅用于 service probe、server plugin、fzf/git、subsession) |
| Node 要求 | `>= 22.19.0` |
| pi 要求 | peerDep `>=0.84.0 <0.85.0 \|\| >=0.85.1`(**精确排除 0.85.0**);README 写 `>=0.84.0` |
| 核心模型 | Machine → Project → Workspace → Session |
| 服务拆分 | `pi-web-sessiond`(长生命周期,持有 agent runtime)+ `pi-web-ui-dev`(可 autoreload) |
| 信任模型 | 「not a sandbox, permission system, or multi-tenant platform」;Docker 模式挂 `/var/run/docker.sock`(root 等价)、`/home`+`/srv`+`/opt` 可读写 |
| 对我们的复用价值 | **安全架构零复用**(信任模型相反),但印证了自建沙箱 extension 是必选项 |
| 可吸收 | `PiAgentSession` 接口(`piSessionService.ts:434-520`)是 pi 会话能力的权威清单,与 RPC 命令集几乎 1:1;`PI_CODING_AGENT_SESSION_DIR` 是控制会话存储的官方 env |
| 有意分歧 | pi-web 暴露 `executeBash()`;我们的 `Bash` 属 `DangerousDisallowedTools`,不提供 |
