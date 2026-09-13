# Agent 后端可切换(claude / pi) Implementation Plan — Plan 2

> **For agentic workers:** 用 subagent-driven-development 逐任务实现:每个任务派一个全新上下文的实现者,完成后派一个任务审查者,修复轮做定向复审,全部任务完成后做一次整分支审查。步骤用 checkbox(`- [ ]`)语法跟踪。

**Goal:** 在 Plan 1 已就位的 `agent.Protocol` seam 上实现第二个后端 `PiProtocol`,并让后端选择成为管理员 Settings 里的全局开关,覆盖文档问答、自由问答、ingest 三条链路。前端聊天代码零改动,`SSEEvent` 线格式不变,claude 路径作为可回退默认值。

**Architecture:** 沿用规格的方案 A(Protocol 接口注入现有 `InteractiveSession`)。池逻辑、SSE 连接计数、30s 清理循环、订阅扇出全部不动;新增 `agent.PiProtocol` 与 `agent` 包级 resolver(`Init`/`Current`/`Invalidate`),把所有 `agent.NewClaudeProtocol(...)` 硬编码构造点换成 `agent.Current()`。沙箱用 pi extension(`backend/scripts/pi-path-validator.ts`,部署时复制到运行时 `scripts/`)承载,强度对齐 `path-validator.py` 并**额外**堵住 `fetch_content` 的本地文件向量。

**Tech Stack:** Go 1.25+ 标准库(`os/exec`、`encoding/json`);TypeScript(pi extension API);无新增 Go 第三方依赖。运行时新增硬依赖:Node >= 22.19.0、pi >= 0.85.1、`pi-web-access`(pin v0.29.0)。

**规格:** `docs/superpowers/specs/2026-09-12-pi-backend-switch-design.md`。**规格是设计权威,本计划是执行序列**;两者冲突时以规格为准,但本计划「决策点」一节记录的 3 处偏离已经过论证,执行者应遵循本计划。上一阶段计划见 `docs/superpowers/plans/2026-09-12-agent-abstraction-layer.md`(其「交接给 Plan 2」列出 6 项,本计划将其展开为 11 个任务)。

**分支:** `feature/pi-backend-switch`,基于 `feature/agent-abstraction-layer`(`64ea15b`,即 PR #92 的 head)。Plan 2 的 PR base 应指向 `feature/agent-abstraction-layer`,直到 PR #92 合入 main 后再改指 main。

## 环境前提(2026-09-13 实测,执行前应复核)

| 项 | 实测值 | 规格要求 | 结论 |
|---|---|---|---|
| `pi` | `/opt/homebrew/bin/pi` **0.85.1** | >= 0.85.1 | ✅ 正是规格实测所用版本,事件序列假设可直接沿用 |
| `node` | `/opt/homebrew/bin/node` **v24.20.0** | >= 22.19.0 | ✅ |
| `pi-web-access` | `~/.pi/agent/npm/node_modules/pi-web-access` **v0.29.0** | pin v0.29.0 | ✅ 版本与规格一致 |
| `claude` | `/opt/homebrew/bin/claude` 可用 | — | ✅ 双后端都能本地验收 |
| `PI_CODING_AGENT_DIR` | **未设置** | 规格要求 Go 侧 `Env()` 显式传 | ⚠️ Task 2 必须显式注入,不能依赖默认值 |
| `~/.pi/agent/web-search.json` | **不存在** | 安全键须钉死在此 | ⚠️ Task 1 产出 sample + 部署说明;缺失时 `loadSsrfConfig()` 返回 fail-closed 默认值,`allowBrowserCookies` 默认 false,故开发期不阻塞 |
| `~/.pi/agent/settings.json` 的 `packages` | `pi-web-access`、`@narumitw/pi-plan-mode`、`@narumitw/pi-btw`、`betterwright`、`pi-subagents` | 规格要求运维侧管控 | ⚠️ 见「风险登记」R1:除 `pi-web-access` 外的包都会在 spawn 时加载并执行任意代码 |

**注意 PATH:** `pi` 与 `node` 都在 `/opt/homebrew/bin`。`start.sh` 目前只补 `/usr/local/bin`,Task 10 必须补 `/opt/homebrew/bin`,否则服务端进程 `exec.LookPath("pi")` 会失败而 UI 报「pi 不可用」。

## 全局约束

- **claude 路径行为不变**(除「决策点 D3」记录的 once-call prompt 传递方式):所有既有 API/SSE 形状、既有 30+ `stream_test.go` 用例、既有 e2e 全绿
- **前端聊天组件 diff 必须为空**:`SSEEvent` 的 JSON tag 一字不改。前端只允许改 `types.ts`、`SettingsPage.tsx`、i18n 资源
- **不在 `claude`/`api`/`ingest` 包里出现后端分支**:禁止 `if proto.Backend() == agent.BackendPi` 这类判断。后端差异只能落在 `agent` 包的两个 Protocol 实现内。这是 Plan 1 建立的核心不变式,Plan 2 最容易破坏它
- **不动** `CLAUDE.md`、`path-validator.py`(tracked 源 `backend/scripts/path-validator.py` 与运行时副本 `scripts/path-validator.py` 都不动)、`backend/dependencies/`(规格明确:不把 pi 加进 checker,不改前端对 `/api/dependencies/status` 的消费)
- **不新增 Go 第三方依赖**,不改 `go.mod`
- **每个任务的收尾闸门**:`cd backend && go build ./... && go vet ./... && go test ./...` 全绿才允许 commit。已知环境性失败可容忍且必须逐个指名:`api` 包的**出网类**用例(`TestWebClippingXArticle` 与 `TestFetchHTML`,同在 `backend/api/web_test.go`;本机 TLS 握手超时时具体哪个失败随网络状况变化,两者都算同一类)、`browser.TestFetchRenderedHTML_TimeoutOnMissingSelector`(浏览器)、`ingest.TestExtractPDFText`(缺 `pdftotext`)。**出现上述之外的失败即视为闸门未过**;若怀疑是既有环境问题,须像 Task 1 那样 `git stash` 后在基线上复跑同一用例并贴出逐字相同的输出,不得仅凭断言
- 提交信息用中文,前缀按任务指定(`feat(agent):` / `feat(security):` / `feat(admin):` / `feat(settings):` / `docs:`)
- 涉及真实 `pi` 子进程的测试,`pi` 不在 PATH 时 `t.Skip`,不得让 CI/他人环境红

## Plan 1 遗留的接缝缺口(本计划必须收口)

Plan 1 抽象了 args/env/encode/parse,但**二进制路径没有走 Protocol**。实测:

- `Protocol.Bin()` 已定义,但 `grep -rn "\.Bin()" backend/` 在非测试代码里**零调用**
- 6 处 spawn 直接用字段:`claude/session.go:107`、`:115`(用形参 `claudeBin`)、`claude/client.go:55`、`:145`、`:167`、`:217`(用 `c.BinPath`)
- `claude/client.go:145`(`SendSimple`)与 `:217`(`SendWithOutput`)**硬编码 `-p` 且把 prompt 放进 argv**,完全绕过 `OnceArgs`
- `api/documents.go:467` 是最严重的泄漏:`exec.Command("claude", cmdArgs...)`,字面量二进制名 + `cmdArgs` 里硬编码 `--model sonnet` + `-p <长 prompt>`(PDF 逐页转 Markdown)

另有一处**规格与代码的计数漂移**需知悉:规格写「`main.go` 现存 11 处 `ClaudeBin` 注入(…313、315…)」,实测 `grep -c ClaudeBin backend/main.go` = **10**,第 315 行是 `Pool: sessionPool` 而非注入点。执行者不必去找第 11 处。

不先收口这些,`agent.Current()` 返回 `PiProtocol` 时仍会 spawn claude。Task 5 与 Task 8 分别处理。

`backend/dependencies/checker.go:95,115,176` 的 3 处 `exec.Command("claude", ...)` **按规格保持不动**(范围外)。

## `scripts/` 目录约定(2026-09-13 核实,规格未提及)

规格把两个新产物写作 `scripts/pi-path-validator.ts` 与 `scripts/web-search.json.sample`,但**仓库根 `scripts/` 整体被 git 忽略**(`.gitignore:6` = `/scripts/`,注释「Local deployment scripts」)。实测:

- `git ls-files scripts/ backend/scripts/` 只返回 `backend/scripts/SECURITY_DEPLOYMENT.md` 与 `backend/scripts/path-validator.py`
- `git check-ignore -v scripts/pi-path-validator.ts` → `.gitignore:6:/scripts/`
- 运行时 `scripts/path-validator.py` 是**未跟踪的本地副本**,与 `backend/scripts/path-validator.py` 内容相同

既有范式(必须沿用):**tracked 源放 `backend/scripts/`,部署时复制到运行时 `scripts/`**。`SECURITY_DEPLOYMENT.md:90` 是 `cp backend/scripts/path-validator.py /opt/llm-knowledge/scripts/`,`:129` 是 Dockerfile 的 `COPY`;运行时目录由 `start.sh:137` 的 `LLM_SCRIPTS_DIR="${SCRIPT_DIR}/scripts"` 指定。

Go 侧定位脚本的既有机制:`main.go:78` 读 `LLM_SCRIPTS_DIR` → `security.go:125` 的 `generateSettingsFile(scriptsDir)` → `filepath.Join(scriptsDir, "path-validator.py")` + `os.Stat`,**文件不存在就返回错误**(`security.go:131-133`),绝不启动一个没有沙箱的进程。

**因此本计划的产物落点一律改为 `backend/scripts/`**,且 `PiProtocol` 必须照抄上面这条 fail-closed 先例(见 Task 2)。若把 extension 放在被忽略的目录,新克隆/新部署里它根本不存在,而 pi 的 `-e` 指向缺失文件时的行为未经证实 —— 那就可能整条 pi 路径**没有任何工具调用拦截**(安全 fail-open),而沙箱 extension 正是本设计新增的关键控制。

## 决策点(规格未定或本计划有意偏离,已论证)

### D1:pi 的 `get_state` 握手如何嵌入 seam —— 新增 `InitCommands()`,并把响应归一化成 `system/init`

规格说 pi 的 session_id「**不走 `ParseLine`**:spawn 后同步发 `get_state`」,并说 pi 路径「跳过 `waitForInit` 5s 超时、`local-<UnixNano>` fallback ID 与 `onSessionID` 别名注册」。

**照字面实现会在 `claude/session.go` 里引入后端分支**,违反本计划的全局约束。改为:

1. `Protocol` 接口**新增第 11 个方法**:
   ```go
   // InitCommands 返回 spawn 之后、任何用户消息之前要写入 stdin 的 JSONL 行。
   // Claude 返回 nil(它的 system.init 由首条用户消息触发)。
   // pi 返回 get_state 命令,用于在发 prompt 前取得真实 sessionId。
   InitCommands() [][]byte
   ```
   `ClaudeProtocol.InitCommands()` 返回 `nil`;`PiProtocol` 返回 `[]byte(`{"id":"init-1","type":"get_state"}`)`。
2. `InteractiveSession` 在 `cmd.Start()` 之后、`go readEvents()` 之前,无条件把 `proto.InitCommands()` 的每一行写入 stdin(Claude 是 no-op)。
3. **`PiProtocol.ParseLine` 把 `get_state` 的 `response` 归一化为 `StreamEvent{Type:"system", Subtype:"init", SessionID:<data.sessionId>}`**。

于是既有的 `waitForInit`、`initDone`、`onSessionID(oldID,newID)` 别名注册、`local-<UnixNano>` fallback、`onResumeFailed` 降级链**一行都不用改**,两个后端走完全相同的路径。pi 的 `get_state` 响应在毫秒级到达,5s 超时不会触发,fallback ID 只是安全网。

*为何不用可选接口(type assertion):* 会丢掉编译期检查,新增第三个后端时不会被强制实现。接口只有两个实现,显式方法成本更低。

**偏离规格的代价:** pi 路径仍会经过 `waitForInit` 与 fallback ID 赋值。这是有意的:换来的是 `claude` 包零后端知识。规格里「跳过」的意图(不必依赖 init-message hack、能同步拿到真实 id)由 `InitCommands` + 归一化完整达成。

### D2:once-call 的 prompt 一律走 stdin,不进 argv

`client.go:145`/`:217` 与 `api/documents.go:467` 现在把 prompt 放在 argv(`-p <prompt>`)。pi 的 `-p` 语义是「读管道 stdin 并合并进初始 prompt」,argv 传 prompt 在 pi 下不成立。

统一为:`OnceArgs(...)` 只产出旗标,**prompt 由调用方写入子进程 stdin**(两个 CLI 在 print 模式下都把管道 stdin 当作 prompt)。

**这是对「claude 路径完全不变」的一处有意偏离**,理由三条:
1. 不这么做就得在后端之间分叉 prompt 传递方式,重新引入泄漏
2. argv 对 `ps` 可见 —— prompt 里含用户上传的文档内容,属信息泄露面
3. argv 受 `ARG_MAX` 限制,PDF 逐页转换的长 prompt 有溢出风险;stdin 无此限制

闸门:`SendSimple`/`SendWithOutput`/`SendSimpleWithRead` 的既有单测必须全绿(它们断言的是返回内容与错误行为,不断言 argv);另加一条断言 `OnceArgs` 产出里**不含 prompt 文本**。

### D3:`--model sonnet` 的处置 —— 引入 model hint,而不是下沉进 `OnceArgs`

`api/documents.go:465` 的 `cmdArgs := append([]string{"--model","sonnet"}, secureArgs...)` 是 Claude 专有旗标,pi 无对应物(规格决策 3:沿用 pi 全局配置,不传 `--provider`/`--model`)。

**实测确认:`ClaudeProtocol.OnceArgs` 目前不含 `--model`**(只产出 `-p` 或 `--print --output-format stream-json --verbose`,加 `SecureArgs` 与 `--system-prompt`)。而 `--model sonnet` **只存在于 documents.go 的 PDF 逐页 OCR 这一处**;`SendSimple`/`SendWithTools`/`summary`/`sections` 都不带模型旗标,走 claude 默认模型。

因此**不能**把 `--model sonnet` 下沉进 `OnceArgs` —— 那会让摘要/分节/翻译一并改用 sonnet,是实打实的行为与成本变化。

处置:`OnceArgs` 增一个 model hint 参数,由各 Protocol 自行解释:

```go
OnceArgs(sysPrompt string, tools []string, print bool, model string) ([]string, error)
```

- `ClaudeProtocol`:`model != ""` 时追加 `--model <model>`;空串时保持现状(不加旗标)
- `PiProtocol`:**忽略该 hint**(规格决策 3),不加任何模型/provider 旗标

documents.go 传 `"sonnet"`,其余 once-call 传 `""`。claude 侧 argv 的**旗标集合与语义不变**,只有顺序变化(`--model` 从 secureArgs 之前移到之后)—— claude CLI 不依赖旗标顺序,但执行者须在提交信息里点明这处顺序变化。

**代价:** 这是 `Protocol` 接口继 D1 之后的第二处签名变更,Plan 1 冻结的 `OnceArgs(sysPrompt, tools, print)` 需同步改;两个实现与相关单测一并更新。

## 文件结构

**创建**

| 文件 | 职责 |
|---|---|
| `backend/agent/pi_args.go` | `PiProtocol` 的旗标/env/工具名映射/`Probe`/`InitCommands` |
| `backend/agent/pi_encode.go` | `PiProtocol` 的 stdin 编码(user message / images / `{"type":"abort"}`) |
| `backend/agent/pi_parse.go` | `PiProtocol.ParseLine`:pi JSONL → 归一化 `StreamEvent`/`Delta` |
| `backend/agent/resolver.go` | `Init` / `Current`(5s TTL 缓存,fail-safe 回退 claude)/ `Invalidate` |
| `backend/agent/pi_args_test.go` | 硬化旗标齐全 + 禁含旗标 + 四档白名单 + 工具名映射 + resume 分支 |
| `backend/agent/pi_encode_test.go` | 文本/带图编码、interrupt(规格里叫 `pi_message_test.go`,统一用 `pi_*` 前缀) |
| `backend/agent/pi_parse_test.go` | 表驱动,用例取自规格「解析器后端差异」的实测事件序列 |
| `backend/agent/pi_constraints_test.go` | 专测规格列出的 pi 专有约束(5 条) |
| `backend/agent/resolver_test.go` | TTL 命中、`Invalidate` 后立即重读、未知/空值回退 claude |
| `backend/agent/probe_test.go` | `Probe` 对不存在的二进制返回错误 |
| `backend/scripts/pi-path-validator.ts` | 沙箱 extension:路径校验 + `fetch_content` URL 校验(tracked 源,部署时复制到运行时 `scripts/`) |
| `backend/scripts/web-search.json.sample` | 部署模板,固化 `allowBrowserCookies:false`、`ssrf.allowRanges:[]`、`ssrf.trustEnvProxy:false`、`sourceCheck` 关(部署到 `$PI_CODING_AGENT_DIR/web-search.json`) |

**修改**

| 文件 | 改动 |
|---|---|
| `backend/agent/agent.go` | `Protocol` 增 `InitCommands() [][]byte`(D1);`OnceArgs` 增 model hint 形参(D3) |
| `backend/agent/claude_args.go` | 实现 `InitCommands()` 返回 nil;`OnceArgs` 按 D3 处理 model hint |
| `backend/config/config.go` | 新增 `PiBin`(env `PI_BIN`,默认 `"pi"`);新增 web 工具名解析(读 `web-search.json` 的 `toolNames`,缺省用默认名),作为 `--tools` 与 `PI_WEB_TOOLS` 的单一事实来源 |
| `backend/claude/session.go` | `buildCmd`/`buildCmdWithEnv` 改用 `proto.Bin()`;`StartSession` 用 `agent.Current()`;`cmd.Start()` 后写 `InitCommands()` |
| `backend/claude/query_pool.go` | 两个 `Start*` 函数改用 `agent.Current()` 与 `proto.Bin()`;删 `claudeBin` 形参与池字段 |
| `backend/claude/client.go` | 4 处 spawn 改用 `protocol().Bin()`;`SendSimple`/`SendWithOutput` 改走 `OnceArgs` + prompt 入 stdin(D2) |
| `backend/db/models.go` | `GlobalSettings` 增 `LLMBackend string \`gorm:"default:claude" json:"llmBackend"\``;`:22` `Document.ChatSessionID` 与 `:48` `Conversation.SessionID` 的注释改为后端中立(仅注释) |
| `backend/api/admin_settings.go` | 响应增 `llmBackend`;输入增 `LLMBackend`;校验取值;`"pi"` 时探测;保存成功后 `agent.Invalidate()` |
| `backend/main.go` | 启动时 `agent.Init(cfg.ClaudeBin, cfg.PiBin)`;移除 10 处 `ClaudeBin` 注入(214/225/234/258/261/276/313/335/348/363) |
| `backend/api/documents.go` | `:467` 改走 `agent.Current()` + `OnceArgs` + `proto.Bin()` + prompt 入 stdin(D2/D3) |
| `backend/api/{query,raw,sections,translate}.go` | 删除 `ClaudeBin` 字段 |
| `backend/ingest/{pipeline,sections,summary}.go` | `claudeBin string` 形参删除,改调 `agent.Current()` |
| `backend/claude/security_test.go` | `TestDangerousToolsCrossLanguageSync` 扩为三语言(解析 TS 的拒绝集合与敏感路径正则);新增 TS extension 的路径边界与 URL 校验用例;新增 `web-search.json.sample` 配置一致性测试 |
| `frontend/src/types.ts` | `GlobalSettings` 增 `llmBackend: string` |
| `frontend/src/components/SettingsPage.tsx` | `{isAdmin && (...)}` 区块内按「Global Translation Section」范式增 `<select>` + 保存 |
| `frontend/src/i18n/*`(en/zh) | 标签、说明、探测失败提示 |
| `start.sh` | 补 `command -v pi` 检查、`/opt/homebrew/bin` 入 PATH、`pi-web-access` 是否已装 |
| `README.md` / `README_ZH.md` | Prerequisites(pi/Node 版本、`pi-web-access`、`web-search.json` 安全键)、后端开关说明、管理员锁死路径警示 |

## Task 1: config 的 `PiBin` 与 web 工具名单一事实来源

**为什么先做:** `PiProtocol` 的 `--tools` 白名单与 `PI_WEB_TOOLS` env 必须同源,否则工具名被 `web-search.json` 的 `toolNames` 改写时两边漂移(规格明写「不能在各处硬编码字面量」)。

- [ ] `config.go` 增 `PiBin`(env `PI_BIN`,默认 `"pi"`),与 `ClaudeBin` 并列;二者只在 `agent.Init` 时用
- [ ] 新增 web 工具名解析:读 `$PI_CODING_AGENT_DIR/web-search.json`(未设则 `~/.pi/agent/web-search.json`)的 `toolNames`,缺省 `web_search`/`fetch_content`/`get_search_content`;文件缺失或解析失败**不报错**,用默认名(与 `loadSsrfConfig()` 的宽容行为一致)
- [ ] 明确「给哪几个」由调用方决定:本任务只提供名称解析;`source_check` **不给**(规格决策)
- [ ] 产出 `backend/scripts/web-search.json.sample`,固化规格表格里那 5 个键(**不是** `scripts/` —— 该目录被 `.gitignore:6` 忽略,见「`scripts/` 目录约定」)
- [ ] 单测:默认名、`toolNames` 改写生效、文件缺失回退、malformed JSON 回退
- [ ] 配置一致性测试(计划原列在 Task 6,因产物在本任务产生,提前到此处):断言 sample 里 `allowBrowserCookies` 为 `false`、`ssrf.allowRanges` 为空、`ssrf.trustEnvProxy` 为 `false`、`sourceCheck` 已关
- [ ] `toolNames` 的键名与嵌套形状**必须从 `~/.pi/agent/npm/node_modules/pi-web-access/` 源码核实**(建议 `utils.ts` 与配置类型定义),不得凭规格描述推测;核实依据(`文件:行号` + 原文)写进提交信息

**闸门:** `go build ./... && go vet ./... && go test ./config/... ./agent/...`
**提交:** `feat(agent): config 增 PiBin 与 web 工具名单一事实来源`

### ✅ Task 1 已完成(`ef6500b`,2026-09-13)

交付:`backend/config/config.go`(+114:`PiBin`/`PI_BIN`、`PiWebToolNames`、`PiWebSearchConfigPath()`、`LoadPiWebToolNames()`、`Names()`)、`backend/config/config_test.go`(新增 258,该包此前无任何测试)、`backend/scripts/web-search.json.sample`(18)。闸门由控制方独立复跑通过。

**已核实的 `pi-web-access` v0.29.0 schema(后续任务直接引用,不必重新推导):**

| 事实 | 依据 |
|---|---|
| `toolNames` 是**根级**键,类型为 `Partial<ToolNames>`,`ToolNames` 四键为**驼峰** `webSearch`/`sourceCheck`/`fetchContent`/`getSearchContent` | `index.ts:146`(`interface WebSearchConfig`)、`:173`、`:227-232` |
| 默认名 `web_search`/`source_check`/`fetch_content`/`get_search_content` | `index.ts:234-239` 的 `DEFAULT_TOOL_NAMES` |
| 合法值判据 `/^[A-Za-z][A-Za-z0-9_-]{0,63}$/`,逐键合并 + 先 trim 再校验 | `index.ts:240`、`:288-301` 的 `resolveToolNames` |
| 配置目录:`PI_CODING_AGENT_DIR` 最高优先,否则 `~/.pi/agent`;文件不存在时 `loadConfig()` 返回 `{}` | `utils.ts:10-26`、`index.ts:100`、`:210-213` |
| **`sourceCheck` 默认是开的**,故「不给 `source_check`」需要两道:白名单不授予 + 配置里 `tools.sourceCheck.enabled=false` | `index.ts:271-274` 的 `isToolEnabled` |
| 域名策略真实路径是 `fetchContent.domainPolicy.{allow,deny}`;`allow` 为空 = 不限制 | `ssrf-protection.ts:67-85`、`:65`、`:265-273` |

规格已同步勘误两处(`sourceCheck` 默认值、`fetchContent.domainPolicy` 少一层)。

**接受的覆盖缺口:** `Load()` 里 `PiBin` 的 env 解析**没有单测** —— `Load()` 内部执行 `flag.String("port", ...)` + `flag.Parse()`,同一测试进程内二次调用会 panic(`flag redefined: port`),且它读真实 env/HOME。`PiBin` 逻辑仅 4 行且与紧邻的 `ClaudeBin` 逐字对称,故本计划接受该缺口;正确修法是先把 `Load()` 拆成可注入的纯函数(见「开放项」4)。

## Task 2: `PiProtocol` 旗标、env 与 `Probe`

- [ ] `pi_args.go`:`PiProtocol{bin, webTools []string, sessionDirRoot string}` + `NewPiProtocol(...)`;`Backend()` 返回 `BackendPi`;`Bin()` 返回 `bin`
- [ ] `SessionArgs`/`ResumeArgs`/`OnceArgs` 按规格「pi 进程配方」产出:`--mode rpc`(会话)/`--mode json` 或 `-p`(一次性)、`--tools <allowlist>`、`--no-skills`、`--no-prompt-templates`、`--no-context-files`、`-e $LLM_SCRIPTS_DIR/pi-path-validator.ts`、`-na`、`--session-dir <userDir>/.pi-sessions`、`[--session <id>]`、`[--system-prompt <p>]`
- [ ] `OnceArgs` 按 D3 带 model hint 形参,`PiProtocol` **忽略**它(断言产出里不含 `--model`/`--provider`)
- [ ] 工具名映射 `Read→read`、`Glob→find`、`Grep→grep`、`LS→ls`、`Write→write`、`Edit→edit`;四档白名单:doc chat `read`+web、自由问答 `read,find,grep,ls`+web、ingest `Send`/`SendWithTools` `read,write,edit`(**不含** web)、`SendSimpleWithRead` `read`
- [ ] web 工具名与 `PI_WEB_TOOLS` 一律取自 Task 1 的 `config.LoadPiWebToolNames()`,**不得在 `agent` 包里再写任何工具名字面量**(单一事实来源的意义就在于此)
- [ ] `source_check` 的两道防线都要在位:白名单不含它(Task 2)+ 部署模板 `tools.sourceCheck.enabled=false`(Task 1 已固化)。实测它默认是**开**的,只靠白名单一旦写错就是敞口
- [ ] **断言不含**:`--no-extensions`(否则 `pi-web-access` 加载不了)、`--dangerously-skip-permissions`、`--verbose`、`--model`、`--provider`
- [ ] `Env(allowedDir)`:继承 `BuildSecureEnv` 的 realpath 行为(macOS `/tmp`→`/private/tmp`),注入 `ALLOWED_DIR`、`PI_WEB_TOOLS`(与 `--tools` 同源)、**`PI_CODING_AGENT_DIR`(显式钉死,不依赖默认值)**
- [ ] `Probe(ctx)`:`exec.LookPath(bin)` + `pi --version`(5s 超时)
- [ ] **fail-closed 前置校验**(照抄 `security.go:131-133` 的先例):`NewPiProtocol` 接收 `scriptsDir`,三个 `*Args` 在拼 `-e` 之前 `os.Stat(filepath.Join(scriptsDir, "pi-path-validator.ts"))`,文件缺失就**返回错误**,绝不产出缺沙箱的 argv。配套单测:文件不存在时 `SessionArgs`/`ResumeArgs`/`OnceArgs` 都报错
- [ ] `scriptsDir` 的传递方式与 `ClaudeProtocol` 的 `settingsPath` 对称:由 `agent.Init` 从 `main.go:78` 的 `LLM_SCRIPTS_DIR` 传入,**不在 `agent` 包里自己读 env**
- [ ] `InitCommands()` 返回 `get_state` 那一行(D1);同时给 `ClaudeProtocol` 补 `InitCommands() nil` 与接口断言,保证本任务结束时 `go build ./...` 仍绿
- [ ] 单测 `pi_args_test.go` + `probe_test.go`,覆盖规格列出的每一条

**闸门:** 全局闸门
**提交:** `feat(agent): PiProtocol 承接旗标、env 与可用性探测`

## Task 3: `PiProtocol` 的 stdin 编码

- [ ] `EncodeUserMessage(content, images)`:pi 的 `prompt` 命令形状;带图时是 `images:[{type:"image",data,mimeType}]`,**注意与 Claude 的 `content:[{type:image,source:{media_type,data}}]` 不同**
- [ ] `EncodeInterrupt()` → `{"type":"abort"}`
- [ ] 单测 `pi_encode_test.go`:纯文本、带图、interrupt;断言每条都以 `\n` 结尾且是合法 JSON

**闸门:** 全局闸门
**提交:** `feat(agent): PiProtocol 的 stdin 编码(prompt/images/abort)`

## Task 4: `PiProtocol.ParseLine`

**这是 Plan 2 风险最高的任务。** 规格的「解析器后端差异」表与「实测确认的三条 pi 专有约束」是唯一权威,实现者必须逐行对照。

- [ ] 事件映射:`message_update`→`assistantMessageEvent` 的 `text_delta`(→`DeltaText`)/`toolcall_start`(→`DeltaToolStart`,带 `id`/`toolName`)/`toolcall_delta`(→`DeltaToolInput`)/`toolcall_end` 或 `tool_execution_end`(→`DeltaToolEnd`);`contentIndex` 填 `Delta.Index`
- [ ] **`text_end.content` 必须忽略**(否则与 delta 重复)
- [ ] `message_end` → 完整消息,**且必须 `message.role == "assistant"`** 才产出(否则用户提问会被回推前端)
- [ ] `agent_settled` → 轮次结束(映射到既有 `result` 语义,驱动 SSE `done`);**不是 `agent_end`**
- [ ] `type=response` + `success=false` → error;`extension_error` → error
- [ ] `get_state` 的 `response` → 归一化为 `StreamEvent{Type:"system",Subtype:"init",SessionID:...}`(D1)
- [ ] `prompt` 自身的 `response{success:true}` **不可当作完成信号**(实测它比首个 `message_update` 早 1.15s 到达)
- [ ] 忽略清单:`agent_start`、`turn_start`、`turn_end`、`message_start`、`thinking_start/delta/end`、`queue_update`、`compaction_*`、`auto_retry_*`、`summarization_retry_*`、`bash_execution_update`
- [ ] 未知事件类型**静默跳过**而非报错(规格开放项 3:pi 有破坏性变更历史)
- [ ] JSONL framing:只按 `\n` 切分,容忍并剥除行尾 `\r`;**不得写 `session` 头行用例**(rpc 模式不发该头行)
- [ ] 单测 `pi_parse_test.go`(表驱动)+ `pi_constraints_test.go`(规格的 5 条约束逐条一个用例,其中第 3 条要穿 `StreamProcessor` 验证前端只收到一份文本)

**闸门:** 全局闸门 + `go test ./agent/... ./claude/...` 逐条绿
**提交:** `feat(agent): PiProtocol.ParseLine 归一化 pi 事件流`

## Task 5: resolver + 会话层接入(收口 `Bin()` 与握手)

- [ ] `resolver.go`:`Init(claudeBin, piBin string)`、`Current() (Protocol, error)`(读缓存的 `GlobalSettings.LLMBackend`,5s TTL)、`Invalidate()`;未知/空值一律回退 `claude`;未 `Init` 时 `Current()` 返回明确错误
- [ ] 替换全部硬编码构造点:`claude/session.go:175`、`claude/query_pool.go` 的两个 `Start*`、`claude/client.go:23`(`protocol()` 的惰性分支)
- [ ] **6 处 spawn 改用 `proto.Bin()`**:`session.go:107`、`:115`、`client.go:55`、`:145`、`:167`、`:217`;删除 `SessionPool.claudeBin`、`QuerySessionPool.claudeBin`、`buildCmd*` 的 `claudeBin` 形参、`StartSession`/`StartResumedSession` 的 `claudeBin` 形参
- [ ] `cmd.Start()` 之后、`go readEvents()` 之前写入 `proto.InitCommands()` 的每一行(D1);**不得**为 pi 加任何 `if backend == pi` 分支
- [ ] `SendSimple`/`SendWithOutput` 改走 `OnceArgs` + prompt 入 stdin(D2);`OnceArgs` 增 model hint 形参,`ClaudeProtocol` 按 D3 实现,本任务涉及的既有调用点一律传 `""`
- [ ] `agent.Init` 的调用点在 Task 8 才加进 `main.go`;本任务用测试内的 `Init` 驱动,保证包可独立验证
- [ ] 单测 `resolver_test.go`;并补一条**不变式测试**:遍历 `backend/` 非测试代码,断言不存在 `exec.Command*("claude"` 字面量与 `BackendPi` 的等值比较(`dependencies/checker.go` 除外,按规格豁免)

**闸门:** 全局闸门 + `pytest tests/e2e/test_chat_streaming.py` 12 passed(claude 路径回归)
**提交:** `feat(agent): resolver 与会话层接入,spawn 统一走 Protocol.Bin()`

## Task 6: 沙箱 extension `backend/scripts/pi-path-validator.ts`

- [ ] `pi.on("tool_call", ...)` 四步:白名单外一律 block(文件工具 ∪ env `PI_WEB_TOOLS`);文件工具提路径→realpath→必须落在 `ALLOWED_DIR` 内(**分隔符边界比较**,防 `/u/1` 匹配 `/u/10`)+ 敏感路径正则 block;`fetch_content` 校验 `url` 与 `urls[]` **全部元素**(仅 `http:`/`https:`,拒绝绝对/相对本地路径与 `file:`/`data:`/`gopher:`/`ftp:` 等);`ALLOWED_DIR` 未设置→文件工具全拒(fail-closed)
- [ ] 敏感路径正则表与 `path-validator.py` **逐条同步**(`/etc/shadow`、`~/.ssh`、`~/.aws`、Keychains 等,含 macOS `/private` 前缀)
- [ ] `ALLOWED_DIR`/`PI_WEB_TOOLS` 都从 `process.env` 读,不硬编码工具名
- [ ] 产物落 `backend/scripts/`(tracked);在 `SECURITY_DEPLOYMENT.md` 的 cp 步骤与 Dockerfile `COPY` 步骤里补上这个文件,并在本地复制一份到运行时 `scripts/`,否则 Task 11 的集成测试无从跑起
- [ ] **不**实现 IP/DNS 级 SSRF 校验(那是 `pi-web-access/ssrf-protection.ts` 的职责,规格已论证它更严)
- [ ] `security_test.go`:把 `TestDangerousToolsCrossLanguageSync` 扩为三语言(同时解析 TS 的拒绝集合与敏感路径正则,任一漂移即失败)
- [ ] 新增 TS 用例(以 shell 驱动,参照既有 `TestPathValidator_WebFetchSSRF` 的手法):路径边界 5 条 + `fetch_content` URL 校验 8 条(含 `urls[]` 任一非法即整体 block、空 `url`/空 `urls` 拒绝)
- [ ] 配置一致性测试已在 Task 1 随产物落地(`backend/scripts/web-search.json.sample`),本任务只需复核其仍然通过

**闸门:** 全局闸门 + `npx tsc --noEmit scripts/pi-path-validator.ts`(或等价的类型检查;若引入 TS 工具链需先确认不污染前端 `package.json`)
**提交:** `feat(security): pi 沙箱 extension 与三语言同步测试`

## Task 7: DB 字段 + Admin API 开关与探测

- [ ] `db/models.go`:`GlobalSettings` 增 `LLMBackend string \`gorm:"default:claude" json:"llmBackend"\``(`AutoMigrate` 自动加列,无数据迁移);`:22`/`:48` 注释改为后端中立(`agent session ID (claude or pi) for resume`),**仅注释,不动字段名与 JSON tag**
- [ ] `admin_settings.go`:`globalSettingsResponse` 增 `llmBackend`;`UpdateGlobalSettings` 输入增 `LLMBackend`,沿用既有部分更新风格(空串表示不改);取值非 `"claude"`/`"pi"` → 400;取值为 `"pi"` 时探测(`LookPath` + `pi --version`,5s),失败 → 400 且消息含安装指引 `npm install -g @earendil-works/pi-coding-agent`;成功后 `agent.Invalidate()`
- [ ] **403 与 400 的消息必须可区分**(规格「管理员权限模型」小节的要求)
- [ ] API 测试:`"pi"` 且不可用→400;`"gemini"`→400;`"claude"`→200 且**不触发探测**;`GET` 含 `llmBackend`

**闸门:** 全局闸门
**提交:** `feat(admin): Settings 增 llmBackend 开关与 pi 可用性探测`

## Task 8: `main.go` 去注入 + `ingest`/`api` 形参改造

- [ ] `main.go`:启动时 `agent.Init(cfg.ClaudeBin, cfg.PiBin)`;移除 10 处 `ClaudeBin` 注入;`NewSessionPool`/`NewQuerySessionPool` 去掉 `claudeBin` 实参
- [ ] `api/{documents,query,raw,sections,translate}.go`:删除 `ClaudeBin string` 字段及其构造处
- [ ] `api/documents.go:440-475` 的 PDF 逐页转换改走 `agent.Current()` + `OnceArgs(..., "sonnet")` + `proto.Bin()` + prompt 入 stdin(D2/D3);`claude.BuildSecureEnv(tempDir)` 改为 `proto.Env(tempDir)`
- [ ] `ingest/{pipeline,sections,summary}.go`:`claudeBin string` 形参删除,`claude.NewClientWithPath(claudeBin)` 改为用 `agent.Current()` 构造的 Client(设 `Proto` 字段)
- [ ] 连带修正所有调用点签名

**闸门:** 全局闸门 + `pytest tests/e2e/test_chat_streaming.py` 12 passed
**提交:** `refactor(agent): main 与 ingest/api 去除 ClaudeBin 注入`

## Task 9: 前端 Settings 开关 + i18n

- [ ] `types.ts` 的 `GlobalSettings` 增 `llmBackend: string`
- [ ] `SettingsPage.tsx`:在既有 `{isAdmin && (...)}` 区块内,按「Global Translation Section (Admin Only)」的现有范式加 `<select>`(claude/pi)+ 保存按钮,复用 `handleGlobalTranslationSave` 的错误提示模式
- [ ] i18n en/zh:标签、说明、探测失败的错误提示
- [ ] **聊天相关组件 diff 必须为空**:`git diff --stat main -- frontend/src/components/ChatView.tsx frontend/src/hooks` 应为空

**闸门:** `cd frontend && npm run build` 成功 + `make build` + 手工点开 Settings 确认开关可见/普通用户不可见
**提交:** `feat(settings): 管理员可切换 LLM 后端(claude/pi)`

## Task 10: `start.sh`、README 与部署文档

- [ ] `start.sh`:补 `command -v pi` 检查、把 `/opt/homebrew/bin` 加进 PATH(Apple Silicon 的 npm 全局 bin 在此,脚本目前只补 `/usr/local/bin`)、检查 `pi-web-access` 是否已装;顺带修 `:28` 的 `brew install poppler` 缺 `|| true`(它会阻塞启动);并在导出 `LLM_SCRIPTS_DIR`(`:137`)之后,把 `backend/scripts/` 下的 `path-validator.py` 与 `pi-path-validator.ts` 复制到运行时 `scripts/`(缺则复制)——消除部署文档里的手工步骤,也堵住「extension 缺失导致 pi 路径 fail-open」
- [ ] README / README_ZH:Prerequisites 增 pi 与 Node 版本要求、`pi-web-access`(pin 版本)、`web-search.json` 必须固化的安全键(尤其 `allowBrowserCookies:false`)及其部署路径 `$PI_CODING_AGENT_DIR/web-search.json`;说明 claude/pi 按 Settings 开关择一生效
- [ ] 写清规格的三条运维警示:①切换后端会使进行中的对话丢失上下文续接能力(历史消息仍在 DB),且**作废是双向的**、「切回原后端可恢复原对话」不成立;②后端切换仅 `admin` 账号可操作,**不要重命名或删除该账号**(`db/db.go:38` 那条无守卫迁移会把它升回,改名即锁死 UI 开关);③`--tools` 拦不住扩展**加载期**的任意代码,须 pin `pi-web-access` 版本并管控 `settings.json` 的 `packages` 列表

**闸门:** `bash -n start.sh`;`./start.sh` 能起来(不实际消耗配额)
**提交:** `docs: pi 后端的前提、部署安全键与运维警示`

## Task 11: 集成测试、e2e 回归与验收

- [ ] Go 集成测试:spawn 真实 `pi --mode rpc`,发 prompt 要求读 `ALLOWED_DIR` 外文件,断言工具调用被 block;`pi` 不在 PATH 时 `t.Skip`
- [ ] **本地文件向量集成测试**(本次新增控制的核心验证;`pi-web-access` 未装时 `t.Skip`):诱导 `fetch_content` 取 `ALLOWED_DIR` 外的本地路径(绝对路径视频 / `/etc/passwd`),断言被 hook block,且 `video-extract.ts` 的 `readFile(absolutePath)` 与 `execFileSync("ffmpeg", ...)` 未被触达
- [ ] e2e 回归:`pytest tests/e2e/test_chat_streaming.py`(12)、`test_chat_view.py`、`test_mobile_chat_view.py`、`test_desktop_no_mobile_dom.py`
- [ ] 手工验收(**两种后端各跑一遍**,由人执行,不消耗配额的自动化不得替代):文档问答多轮 + SSE 断线重连、自由问答带图片、中途 interrupt、ingest 摘要与分节
- [ ] resume 往返验收(两种后端各一遍):第一轮后查 DB 确认 `chat_session_id`/`session_id` 写入的是**该后端自己的** ID 格式;重启进程或等 30s 清理后再提问,确认走 `--resume`/`--session` 且上下文续接成功
- [ ] `LLMBackend=pi` 时把 Settings 切到 pi,重跑 e2e 的聊天用例,确认前端零改动即可工作

**完成定义(规格原文,逐条勾)**

- [ ] `LLMBackend=claude` 时 `go test ./...` 全绿且行为与改造前逐条等价
- [ ] `LLMBackend=pi` 时手工验收全部通过
- [ ] pi 路径沙箱强度不低于 Python 版(fail-closed + 边界 + 敏感路径三项全过),**且 `fetch_content` 本地文件向量被堵住**
- [ ] 前端聊天代码 diff 为空
- [ ] Settings 切到不可用的 pi 时被 400 拦住

## 风险登记

| # | 风险 | 处置 |
|---|---|---|
| R1 | `~/.pi/agent/settings.json` 里除 `pi-web-access` 外还装了 `betterwright`、`pi-subagents`、`@narumitw/pi-plan-mode`、`@narumitw/pi-btw`。我们**不能**用 `--no-extensions`(会连带关掉 `pi-web-access`),因此这些包在每次 spawn 时都会加载并执行任意代码 | 规格已列为残留风险。Task 10 写入部署文档:生产环境应用独立的 `PI_CODING_AGENT_DIR`,其 `settings.json` 的 `packages` 只含 pin 过的 `pi-web-access`。**本计划不改开发者本机的 settings.json** |
| R2 | pi 协议有破坏性变更历史(`docs/rpc.md` 记载 `message_update` 曾移除累积 `message` 与 `assistantMessageEvent.partial`) | `ParseLine` 对未知事件静默跳过;部署文档 pin 版本;Task 4 的用例全部取自 0.85.1 实测序列 |
| R3 | D2 把 prompt 从 argv 移到 stdin,是 claude 路径的一处可观测变化 | 既有单测 + 12 个 e2e 用例作为闸门;若某处依赖 argv prompt(如日志),在该任务内一并修正 |
| R4 | Task 5 删除 `claudeBin` 形参会波及大量调用点,易与 Task 8 重叠 | Task 5 只改 `backend/claude` 内部与其直接调用方;`main.go`/`api`/`ingest` 的形参删除留给 Task 8。两任务不得同时修改同一文件 |
| R5 | 会话池在浏览器断开后不回收 claude 子进程(既有问题,每轮全量 e2e 留约 8 个孤儿),pi 路径下同理会积累 node 进程 | 属既有缺陷,**不在本计划范围**;但 Task 11 的集成测试必须在 teardown 里显式 kill 自己 spawn 的进程,不得加剧 |
| R6 | `pi-path-validator.ts` 若不在运行时 `scripts/` 里,pi 路径将没有任何工具调用拦截(安全 fail-open);而仓库根 `scripts/` 被 git 忽略,新克隆里天然没有该文件 | 三重防护:产物 tracked 在 `backend/scripts/`;Task 2 的 `os.Stat` 前置校验(缺失即拒绝产出 argv);Task 10 让 `start.sh` 自动复制 |
| R7 | `source_check` 在 pi-web-access 里**默认注册且启用**(`index.ts:271-274`),`--tools` 白名单只决定「不授予调用」 | 两道防线:白名单不含它 + `web-search.json` 里 `tools.sourceCheck.enabled=false`(Task 1 的 sample 已固化,并由 `config_test.go` 的一致性测试守护) |

## 开放项(承规格,本计划不做)

1. pi-web 的会话常驻模型(`sessiond` + `web` 拆分)——与现有 30s 清理策略冲突,暂不采用
2. SSE 重连恢复简化(用 `get_messages` 取代 `streamingContent` 累积 + `sseReconnectContent` 去重)——留作 pi 路径稳定后的独立简化项
3. `backend/dependencies` 的孤儿状态(前端至今未消费 `/api/dependencies/status`)——仅复用其探测手法,不扩大范围
4. `config.Load()` 不可测:它内部 `flag.String` + `flag.Parse()`,二次调用即 panic,导致 `PiBin`/`ClaudeBin` 的 env 解析无法单测。应拆成「纯函数解析(可注入 env/args)」+「薄壳 `Load()`」。**本计划不做**(属既有结构问题,与后端切换无关)
