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
- **`StreamEvent.Delta` 是 `json:"-"`**(自 main 的 `89c3862` 起):它是内部归一化增量,**绝不**序列化进 SSE/JSON。Plan 2 新增的任何路径都不得让它变成 wire 字段 —— 注意 `api/translate.go` 是「直接 marshal `StreamEvent`」的既有先例,这条 tag 正是为了防御未来某条路径泄露内部字段
- **不在 `claude`/`api`/`ingest` 包里出现后端分支**:禁止 `if proto.Backend() == agent.BackendPi` 这类判断。后端差异只能落在 `agent` 包的两个 Protocol 实现内。这是 Plan 1 建立的核心不变式,Plan 2 最容易破坏它
- **不动** `CLAUDE.md`、`path-validator.py`(tracked 源 `backend/scripts/path-validator.py` 与运行时副本 `scripts/path-validator.py` 都不动)、`backend/dependencies/`(规格明确:不把 pi 加进 checker,不改前端对 `/api/dependencies/status` 的消费)
- **不新增 Go 第三方依赖**,不改 `go.mod`
- **每个任务的收尾闸门**:`cd backend && go build ./... && go vet ./... && go test ./...` 全绿才允许 commit。已知环境性失败可容忍且必须逐个指名:`api` 包的**出网类**用例(`TestWebClippingXArticle` 与 `TestFetchHTML`,同在 `backend/api/web_test.go`;本机 TLS 握手超时时具体哪个失败随网络状况变化,两者都算同一类)、`browser.TestFetchRenderedHTML_TimeoutOnMissingSelector`(浏览器)、`ingest.TestExtractPDFText`(缺 `pdftotext`)。**出现上述之外的失败即视为闸门未过**;若怀疑是既有环境问题,须像 Task 1 那样 `git stash` 后在基线上复跑同一用例并贴出逐字相同的输出,不得仅凭断言
- 提交信息用中文,前缀按任务指定(`feat(agent):` / `feat(security):` / `feat(admin):` / `feat(settings):` / `docs:`)
- 涉及真实 `pi` 子进程的测试,`pi` 不在 PATH 时 `t.Skip`,不得让 CI/他人环境红
- **不变式 I1(Task 1 ↔ Task 2,审查者提出、已实测支撑)**:每一次 pi spawn,`PiProtocol.Env()` 注入的 `PI_CODING_AGENT_DIR` 必须**逐字等于** `filepath.Dir(config.PiWebSearchConfigPath())` 在 Go 进程内解析出的目录。实现方式必须是**派生,不是重算**:`"PI_CODING_AGENT_DIR=" + filepath.Dir(config.PiWebSearchConfigPath())`;禁止重新读 env、禁止硬编码 `~/.pi/agent`、禁止用 Settings/DB 里的另一个路径。该值还必须**非空、绝对、不含 `~`**(`pi-web-access/utils.ts:13-14` 不做 tilde 展开,而 pi 本体 `getAgentDir()` 会做 `expandTildePath`,`config.js:408-409`/`:422-425`;`~`-前缀会让 auth 目录与 web-search 目录分家),且**不得指向一个新建的空目录**(它同时是 pi 的 auth/settings 目录)。
  I1 成立即可彻底消除「Go 读的配置文件 ≠ pi 子进程读的配置文件」这条漂移:`utils.ts:13-14` 的第一分支对任何非空值无条件返回,注入后子进程内 XDG/legacy 两级不可达。Go 侧只实现四级回退的第 1、4 级是**有意**的 —— XDG/legacy 那两级是 `pi-web-access` 独有的怪癖,pi 本体的 `getAgentDir()`(`config.js:421-427`)没有 XDG 回退,故两级实现与 pi 本体语义一致

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

### D4:`--session-dir` 改由 env 注入 —— 因为 `SessionArgs` 签名里没有 userDir(Task 2 实现时发现)

规格写的是 `--session-dir <userDir>/.pi-sessions`,但 Plan 1 冻结的 `SessionArgs(sysPrompt, tools)` 签名里**没有 userDir**(它只经 `Env(allowedDir)` 与 `cmd.Dir` 传递)。照字面实现只有两条路,都更差:

1. **传相对值 `.pi-sessions`** —— 它能工作,但把正确性挂在「子进程 cwd 恰好等于 userDir」这个隐式耦合上。已核实可行:`cli/args.js:88-89` 逐字取值不做解析,`core/session-manager.js:600` 只做 `normalizePath`,而 `utils/paths.js:58-80` 的 `normalizePath` 只 trim / 展开 `~` / Windows 规范化,**不绝对化**;且 `grep -rn "process.chdir" dist/` 零命中(pi 从不改 cwd)。于是相对值最终由 fs 按 `process.cwd()` 解析。
2. **给三个 `*Args` 加 userDir 形参** —— 波及 `ClaudeProtocol` 与全部调用点,直接违反「claude 路径行为不变」。

改为走 env:pi 的优先级是 **旗标 > `PI_CODING_AGENT_SESSION_DIR` > settings**(`main.js:531-534`;env 名由 `config.js:407` 的 `${APP_NAME.toUpperCase()}_CODING_AGENT_SESSION_DIR` 得出)。不传旗标即由本 env 生效,而且它**压过** `settings.json` 里可能被运维设过的 `sessionDir` —— 这一点比旗标方案更强,因为会话目录不允许被运维配置改写。

`Env(allowedDir)` 本就拿到 userDir 且已做 `filepath.EvalSymlinks`,于是会话目录与 `ALLOWED_DIR` **同源同 realpath**,不可能漂移;而方案 1 的相对值不经 realpath,macOS 上 `/tmp` 与 `/private/tmp` 会让两者分家。

**代价:** `PiProtocol` 不再持有计划草图里的 `sessionDirRoot` 字段(改用常量 `piSessionDirName = ".pi-sessions"` 与 `Env()` 的 realpath 结果拼接);`--session-dir` 进入 Task 2 的「禁含旗标」断言清单,由 `TestPiArgs_NeverContainForbiddenFlags` 钉住。

### D5:`tool_execution_end` **不**映射为 `DeltaToolEnd`(Task 4 实现时发现)

本计划 Task 4 写的是「`toolcall_end` **或** `tool_execution_end`(→`DeltaToolEnd`.」。只实现前者,理由两条:

1. `tool_execution_end` 是**顶层**事件,带的是 `toolCallId` 而**没有 `contentIndex`**(`rpc.md:1042-1050`)。映射成 `DeltaToolEnd` 会让 `Delta.Index` 落到零值 `0`,**可能误关掉另一个正在进行的工具** —— 而 `index 0` 是真实存在的槽位(规格实测:thinking 占 0、text 占 1)。
2. `Process` 的 `activeTools` 是 `map[int]*activeTool`,**只按 Index 关联**;`Delta` 虽然有 `ToolID` 字段,但 `DeltaToolEnd` 分支不读它。要按 `toolCallId` 关联就得改 `claude` 包的 `Process` —— 那是 Task 4 的范围外,且会动到 claude 路径。

**不丢功能:** `toolcall_end` 是 assistant 消息流的一部分,工具调用完成时必然到达;即便某轮缺席,`agent_settled` → `result` 会让 `Process` 重置 `activeTools`,不会泄漏。`tool_execution_start`/`update`/`end` 三者一并加入忽略清单(`update` 的 `partialResult` 是**累积快照**而非增量,`rpc.md:1052-1054`,放行会让前端重复显示)。

### D6:R8 的切换探测用**静态预检**而不是「追加一次带扩展的最小 spawn」(Task 7 决定)

R8 的处置列写着「Task 7 的探测**不能**只靠 `pi --version`(可考虑追加一次带扩展的最小 spawn,代价是探测变慢——留给 Task 7 决定)」。`pi --version` 在 pi 的 `main.js:483-486` 提前 `exit(0)`、**根本不加载扩展**,所以它在结构上就探测不到 `web-search.json` 的致命配置 —— 这个判断成立。但「追加一次 spawn」被否决,三条理由:

1. **慢**:扩展加载 + npm 包解析是秒级到数十秒,放在 `PUT /api/admin/settings` 的 handler 里容易撞 HTTP 超时
2. **有副作用**:会在 pi 的 agent 目录里留下会话文件,并触发已加载包的**加载期代码**(即 R1)
3. **收尾不可靠**:实测 pi 在 rpc 模式下 stdin EOF 后**并不退出**(Task 4 前的探针等了 25s 仍需外部 kill),要可靠收尾就得在 HTTP handler 里自己管进程生命周期

改用静态预检:**Go 与 pi-web-access 读的是同一个文件**(不变式 I1 保证),所以 Go 完全能算出 pi 会不会在 `resolveToolNames` 上抛错。零成本、确定性、可单测。

为此把 Task 1 的判定逻辑抽成 `config.ValidatePiWebConfig() (names, problem)`,`LoadPiWebToolNames` 变成「调用它 + 打日志」的薄壳 —— **共用同一份实现,两条路径不可能漂移**。`problem` 不带 `[config] ` 前缀,因为它还要直接进 400 响应体。

**代价与边界:** 静态预检只能覆盖 Go 能从配置算出来的失败(`toolNames` 的形状/取值/重名),覆盖不了「pi-web-access 根本没装」或「其他包加载期抛错」—— 前者由 Task 10 的 `start.sh` 检查兼 Task 11 的集成测试覆盖。「pi 能否真的带着扩展启动」留给 Task 11 的集成测试 —— 那才是它该被验证的地方(在 CI 里跑,不占用户的一次 Settings 保存)。

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

**审查与修复轮(0 Critical / 3 Important / 7 Minor → 已修 `9a873b7`)**:审查者抓出两条控制方与实现者都没看见的真问题,均已修复:

- **I-1(运维炸点,已由源码推证升级为实测)**:`web-search.json` 的 `toolNames` 写错,后果**不是**「联网不可用」,而是**整个 pi 后端 `exit 1`**(连带不用 web 工具的 ingest 链路)。实测:临时 `PI_CODING_AGENT_DIR` + 只读 symlink 真实 `auth.json`/`settings.json`/`npm`,对照组 `{"allowBrowserCookies":false}` → 退出码 0、stderr 空;实验组 `{"toolNames":{"webSearch":42}}` → 退出码 1、stderr `Failed to load extension ".../pi-web-access/index.ts": ... toolNames.webSearch ... must be a string`。链路:`resolveToolNames`(`index.ts:288-313`)抛错 → 位于 `loadConfigForExtensionInit` 的 catch **之外**(`:1065` vs `:1068`)→ `loader.js:483-486` → `main.js:631-634` → **`main.js:723-731 process.exit(1)`,在所有 mode 的公共路径上(含 `--mode rpc`)**。而 `pi --version` 在 `main.js:483-486` 提前 `exit(0)`、不加载扩展 → **Task 2 的 `Probe` 与 Task 7 的切换探测都抓不到它**:管理员切到 pi 时探测通过,随后所有请求失败。
  处置:注释按「pi 吞掉(文件缺失 / JSON 非法 / 根非对象)vs pi 抛错(`toolNames` 形状、取值、重名,含 `null`)」两栏写准确;仅在后者那一支 `log.Printf` 告警(带配置路径与后果);并由 `TestLoadPiWebToolNames_WarnsOnlyWhenPiWouldRejectConfig`(11 个子用例:6 静默 + 5 必须告警)把这条承诺变成断言。规格禁止的是**返回 error**,不禁止日志。
- **I-3(本 diff 新引入的唯一 fail-open 通路)**:`os.UserHomeDir()` 失败时原实现回退 `"."`,使一条安全相关的**读**路径变成 CWD 相对路径(`$HOME` 未设 + CWD 可写时可植入 `{"toolNames":{"webSearch":"source_check"}}`,一次击穿两道 `source_check` 防线)。已改为返回 `""` 且调用方显式判空(`config.go:145-147`、`:195-199`),并补诱饵配置用例(`t.Chdir` + 一份把三个工具名全改成 `source_check` 的配置,断言不被读取)。
- **I-2**:见上文不变式 I1(已写进「全局约束」并落进 `config.go:108-140` 的条件句注释)。
- Minor 已修:M-1(注释同源)、M-3(trim 字符集差异 U+0085/U+FEFF,仅注释)、M-4(原断言恒真的子测试升级为「任何输入下 `Names()` 都不得含 `source_check`」)、M-5(正则 `{0,63}` 边界 64/65、改名后顺序、`toolNames:null`、根是数组/标量)、M-6(改消息不收紧断言)。
- 一处**有据的越界**:`resolveToolNames` 遍历的是 `Object.keys(DEFAULT_TOOL_NAMES)` 全四键(`index.ts:293`),故 `{"toolNames":{"sourceCheck":42}}` 同样会让 pi `exit 1`;Go 若只校验自己采纳的三个键就会保持沉默 —— 那正是 I-1 要消灭的「零日志的整体不可用」。因此对 `sourceCheck` 也做校验(**仅告警,不采纳其名字**),未知键仍不校验(pi 也不校验,否则会产生假告警)。控制方已复核该依据成立。
- 解析结构由「单次 struct unmarshal」改为「两级 map 解析」,这是 I-1 的**必要条件**而非重构偏好:原实现无法区分「`toolNames` 不是对象(pi 抛错)」与「根 JSON 非法(pi 吞掉)」,也就无法只对前者告警。既有用例期望值一字未改。

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
- [ ] **落实不变式 I1**(见「全局约束」):`Env()` 里的 `PI_CODING_AGENT_DIR` 必须由 `filepath.Dir(config.PiWebSearchConfigPath())` **派生**。守护测试:在「env 已设」与「env 未设」两种情形下,各断言一次 `Env(dir)` 返回中该键的值 `==` 派生值
- [ ] **拒绝相对路径的 `PI_CODING_AGENT_DIR`**(Task 1 修复轮新发现):相对值会让 Go 按服务进程 CWD 解析、而 pi 子进程 cwd 是 `userDir`,两边必然不一致 —— 与 I-3 堵的是同一类问题、只是入口不同。在注入前把关(`filepath.Abs` 或直接拒绝),并加用例
- [ ] web 工具名**在 `NewPiProtocol` 里解析一次并持有**(M-7):若三个 `*Args` 各调一次 `LoadPiWebToolNames()`,等于每次请求 3 次磁盘读 + 3 次可能的告警刷屏;解析一次也顺带关掉 I1 的时间窗(解析名字与注入 env 之间配置被改)
- [ ] 决定 M-2:`Names()` 目前不去重(`{"toolNames":{"webSearch":"x","fetchContent":"x"}}` → `["x","x",...]`)。对 `--tools` 无害(allowlist 语义),但 `PI_WEB_TOOLS` 与 Task 6 的 hook 白名单会带重复项。要么按首次出现去重,要么加用例把当前行为钉住并注明「重名在 pi 侧是致命的(会让 pi `exit 1`),见 Task 1 的 I-1」
- [ ] `InitCommands()` 返回 `get_state` 那一行(D1);同时给 `ClaudeProtocol` 补 `InitCommands() nil` 与接口断言,保证本任务结束时 `go build ./...` 仍绿
- [ ] 单测 `pi_args_test.go` + `probe_test.go`,覆盖规格列出的每一条

**闸门:** 全局闸门
**提交:** `feat(agent): PiProtocol 承接旗标、env 与可用性探测`

### ✅ Task 2 已完成(`b54a735`,2026-09-13)

交付:`backend/agent/pi_args.go`(新)、`pi_args_test.go`(新,21 个用例)、`probe_test.go`(新,4 个用例);并按 D1/D3 改 `Protocol` 接口(`InitCommands()` 新增、`OnceArgs` 增 model hint),连带更新 `claude_args.go`、`claude_args_test.go`(补 model hint 用例)与 `client.go:51`/`:161` 两个调用点(都传 `""`)。

`PiProtocol` 此时**尚未**满足 `Protocol`(缺 `Encode*`/`ParseLine`,属 Task 3/4),故有意不写 `var _ Protocol = (*PiProtocol)(nil)` —— 该断言由 Task 4 补上。

**D4 已在本任务落地**(见「决策点」):会话目录改由 `Env()` 注入 `PI_CODING_AGENT_SESSION_DIR`,`--session-dir` 进入禁含旗标清单。

**已核实的 pi 0.85.1 事实(Task 3/4/5 可直接引用,不必重新推导):**

| 事实 | 依据 |
|---|---|
| `--mode` 只认 text/json/rpc,**非法值被静默忽略**(mode 变 undefined) | `cli/args.js` parseArgs 的 `--mode` 分支 |
| rpc 模式**不读**管道 stdin(留作 JSON-RPC);json/print 会读并**前置**拼进初始 prompt | `main.js:701-708`、`cli/initial-message.js:6-18` |
| `-p` 会**贪婪吞掉**紧随其后那个不以 `-` 开头的实参当 message | `cli/args.js:172-176` |
| 未知 `--xxx` 旗标被收进 `unknownFlags` **静默忽略**;未知单横线选项才报 error | `cli/args.js` parseArgs 尾部 |
| 工具名重名 → `resolveToolNames` 抛错(仅限**已启用**的键)→ pi `exit 1` | `pi-web-access/index.ts:303-311` |

由第 2、3 条得出两条实现约束,都有用例钉住:once-call 的 prompt **绝不能进 argv**(否则与 stdin 内容被拼接成一段),且 `-p` 必须放**末尾**(让误吞在结构上不可能)。

**M-2 处置:去重 + 告警。** `config` 的 `Names()` 不去重,而重名在 pi 侧致命(`exit 1`,且 `pi --version` 探测不到)。Task 1 的 `warnInvalid` 只覆盖单键非法、**不覆盖跨键重名**,这条路径此前是静默的。`PiProtocol` 在构造时(M-7:解析一次并持有)按首次出现去重并补上告警。

**三个安全把关:** ①fail-closed 沙箱前置校验(照抄 `security.go:131-133`,三个 `*Args` 在拼 `-e` 之前 `os.Stat`);②`-e` 的值**必须绝对** —— Go 的 Stat 以服务进程 CWD 解析相对路径而 pi 子进程以 cwd(= userDir)解析,基准不同就会出现「Stat 通过但 pi 加载不到」即 R6 的 fail-open,有用例专门堵这条;③I1 落实为**派生**而非重算,并新增两条把关(含 `~` 时不注入、相对路径按服务进程 CWD 绝对化),三种「不注入」情形都先剔除继承来的坏值,不靠「后写覆盖先写」这种实现细节。

**变异检验 5 处全部被抓:** 去掉 `-na`;`OnceArgs` 误授 web 工具;I1 改成重读 env(报出「子进程会读 `backend/agent/web-search.json` 而 Go 读 `~/.pi/agent/web-search.json`」);去掉 `requireSandbox`;去掉重名去重。

**测试密封:** 每个用例都用 `t.Setenv` 把 `PI_CODING_AGENT_DIR` 钉到空临时目录,否则会读开发机/CI 上真实的 `~/.pi/agent/web-search.json`,一旦运维改过 `toolNames`,所有关于工具名的断言都随环境漂移。

**接受的缺口:** `Probe` 的 5s 超时**没有用例**。要验证它得造一个在 `--version` 上挂住的假二进制并真等 5s,代价(每次全量测试多 5s)与收益不匹配;超时本身是 `context.WithTimeout` 两行,由审查覆盖。同理未测 ctx 已取消的分支 —— 我最初写了该用例,但发现选的二进制(`sh --version`)会立刻退出、证明不了取消语义,遂删除而不是留一个没有牙的假护栏。

## Task 3: `PiProtocol` 的 stdin 编码

- [ ] `EncodeUserMessage(content, images)`:pi 的 `prompt` 命令形状;带图时是 `images:[{type:"image",data,mimeType}]`,**注意与 Claude 的 `content:[{type:image,source:{media_type,data}}]` 不同**
- [ ] `EncodeInterrupt()` → `{"type":"abort"}`
- [ ] 单测 `pi_encode_test.go`:纯文本、带图、interrupt;断言每条都以 `\n` 结尾且是合法 JSON

**闸门:** 全局闸门
**提交:** `feat(agent): PiProtocol 的 stdin 编码(prompt/images/abort)`

### ✅ Task 3 已完成(`c8afadd`,2026-09-13)

交付:`backend/agent/pi_encode.go`、`pi_encode_test.go`(6 个用例)。wire 形状对 `docs/rpc.md` 逐字核实(`:43-58` prompt、`:78` images、`:124-134` abort)。

**与 Claude 的三处结构差异**(故不能照搬 `ClaudeProtocol.EncodeUserMessage`):pi 的 `message` 恒为**字符串**、图片走**兄弟字段** `images`;键是 `mimeType`(驼峰)且无 `source` 包装;`abort` 只有一个键、无 `request_id`。三条都有用例钉住(断言 `images[i]` 恰好三键,且 `source`/`media_type` 出现即报错)。

`prompt` 的 id 用包级 atomic 计数器而不是 `time.Now().UnixNano()` —— 后者在紧循环下会撞,正是本分支 `061e7dd` 修的那个坑。用例还钉住「prompt 的 id 绝不与 `InitCommands` 的 `get_state` id 撞上」:混淆两者会直接坏掉会话 ID 捕获,或把 prompt 的 `response` 误当完成信号而提前发 `done`(Task 4 的关键约束)。

**变异检验:4 处被抓**(图片改用 Claude 的 `source`/`media_type` 形状;prompt 丢掉结尾换行;`abort` 改成 `control_request`;id 变常量或与 `init-1` 撞名)。

**一处没抓到、且已如实写进注释而不是留假论证:** id 改回 `UnixNano` 后连跑 5 次全绿。我最初按「2000 次必撞(生日悖论)」把迭代数提到 2000,**实测推翻** —— 本用例循环体(含多字节文本的 `json.Marshal`)每次 >1µs,在本机约 1µs 的时钟粒度下反而不撞;而 claude 侧那个轻得多的 `EncodeInterrupt` 在 50 次循环里就有约四成概率撞。已改回 200 并重写注释:唯一性由构造(计数器)保证,不依赖该用例;它钉住的是「id 非空、彼此不同、且不与 `get_state` 撞名」。

### ⚠️ Task 3 期间发现的新缺口 R9(已修 `fbe6e45` + 登记 `2100e31`)

为核实 `prompt` 形状而读 `docs/rpc.md` 时发现:**rpc 模式下以 `/` 开头的用户消息会被 pi 当扩展命令派发执行**(`rpc-mode.js:301-304` 未传 `expandPromptTemplates` → `agent-session.js:822` 默认 `true` → `:828-834` 派发 → `:954-961` 执行)。规格与本计划都未覆盖,四道既有防线(`--tools`、`--no-skills`/`--no-prompt-templates`、沙箱 `input` hook、`-na`)全拦不住它。详见风险登记 R9。

已修:部署模板显式关掉 `pi-web-access` 的 4 个命令(`commands.*.enabled=false`;`isCommandEnabled` 在 `index.ts:277-279` 是 `!== false`,**默认开**),守护测试扩进既有的 `TestWebSearchSample_PinsSecurityKeys`(4 处变异全抓,含「模板里多出未知的 `enabled:true` 命令」)。

**定性:这不是额外收紧,而是追平两个后端的安全强度** —— Claude 侧的对应物 `SlashCommand` 早就在 `ClaudeDangerousDisallowedTools` 里被硬阻断,pi 路径此前有一个敞开的等价物。

**留给后续任务的两件事:** ①残留部分(其他已加载包的命令,如本机的 `pi-subagents`)只能靠 R1 的运维隔离,Task 10 须写第四条运维警示;②Task 11 的验证**必须带对照组**(临时把 `enabled` 改成 `true`,断言命令确实会执行),否则无法区分「被关掉了」与「本来就没触发」—— 这与 Task 6 对沙箱 extension 提的是同一条要求。**一个尚未决定的开放项**:是否在 Go 侧启动时对「部署的 `web-search.json` 仍开着 commands」`log.Printf` 告警(与 Task 1 的 `warnInvalid` 同手法)。本次未做,因为它属 Task 1 已冻结的 `config.go`,且需要重新解析 `commands` 段;不做则运维漏配时是静默 fail-open。

## Task 4: `PiProtocol.ParseLine`

**这是 Plan 2 风险最高的任务。** 规格的「解析器后端差异」表与「实测确认的三条 pi 专有约束」是唯一权威,实现者必须逐行对照。

- [ ] 事件映射:`message_update`→`assistantMessageEvent` 的 `text_delta`(→`DeltaText`)/`toolcall_start`(→`DeltaToolStart`,带 `id`/`toolName`)/`toolcall_delta`(→`DeltaToolInput`)/`toolcall_end` 或 `tool_execution_end`(→`DeltaToolEnd`);`contentIndex` 填 `Delta.Index`
- [ ] **空增量守卫(承 main 的 `89c3862`,pi 侧不可重犯 claude 侧曾有的疏漏)**:`toolcall_delta` 的 `delta` 为空串时**不得**产出 `DeltaToolInput`;`text_delta` 同理。依据:`Process`(`claude/stream.go:159-168`)对空 `ToolInput` **不再二次守卫**,放行就会发出携带上一条累积输入的重复 `tool_input` 事件。claude 侧的空 `partial_json` 守卫(`claude_parse.go:106`)是 Plan 1 重构孤立出来的唯一防线,且此前无测试覆盖,`89c3862` 才补上 `TestParseLine_EmptyInputJSONDeltaProducesNoDelta`(并用变异验证过判别力)。pi 侧必须有对应用例:喂空 `delta` 断言 `ok=false` 或 `Delta.Kind == DeltaNone`
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

### ✅ Task 4 已完成(`731c0e4`,2026-09-13)

交付:`pi_parse.go`(309)、`pi_parse_test.go`(60 个表驱动子用例)、`pi_constraints_test.go`(9 个约束用例)。`PiProtocol` 至此满足 `Protocol`,已补上 `var _ Protocol = (*PiProtocol)(nil)`。

**权威源的选择:rpc.md 而不是 pi-ai 的 `.d.ts`。** 两者不一致,而 ParseLine 吃的是 rpc 线格式:

| 事实 | 依据 |
|---|---|
| `.d.ts:411-455` 的每个 `assistantMessageEvent` 都带 `partial: AssistantMessage`(累积快照),但 **rpc 线格式已移除它** —— 照 `.d.ts` 写会去读一个线上根本不存在的字段 | `rpc.md:992-993` "intentionally omits the former cumulative `message` field and `assistantMessageEvent.partial"`;即风险登记 R2 记载的那次破坏性变更 |
| `.d.ts:442` 的 `toolcall_start` 只有 `contentIndex`,而线上带 `id` 与 `toolName`(rpc 层补的) | `rpc.md:990` 的示例 + `:994` "`toolcall_start` provides the call `id` and `toolName`",与规格实测结论一致 |
| `tool_execution_update.partialResult` 是**累积快照**而非增量 | `rpc.md:1052-1054` |
| `agent_end` 之后仍可能跟 retry/compaction/queued,`agent_settled` 才是完全落定 | `rpc.md:893` vs `:905-911` |

按计划要求,**没有**写 `{"type":"session",...}` 头行的用例(rpc 模式不发该头行,它只属于 `--mode json`)。

**规格未覆盖、实现时从源码查出的坑:块类型必须翻译。** pi 的内容块叫 **`"toolCall"`**、入参字段叫 **`arguments` 且是个对象**(`pi-ai/types.d.ts:256-264`、`:307-312`),而 `claude` 包的 `ExtractToolUseFromAssistantMsg` 硬编码判 `block.Type == "tool_use"`。直接把 `message_end.message` 塑进归一化 `Message` 会让 pi 的工具块被**静默丢弃**。正常流式路径下看不出来(前端已从 `toolcall_start` 拿到 `tool_start`),这个洞只在 **SSE 重连**时暴露:重连走 assistant 完整消息 + `sentToolIDs` 去重那条路,拿不到工具块就永远补不回 `tool_start`。故 `convertPiMessage` 做三步翻译(`text`→`text`、`thinking`→`thinking` 且内容字段 `thinking`→`Text`、`toolCall`→`tool_use` 且 `arguments` 原样透传为 `RawMessage`),约束 8 穿 `StreamProcessor` 钉住。

**D5 已落地**(见「决策点」):`tool_execution_end` 不映射为 `DeltaToolEnd`,因为它没有 `contentIndex`、而 `Process` 只按 Index 关联。

**变异检验 8 处全部被抓**,且每条报错都直指用户可见后果:去掉 role 过滤 → 用户自己的提问被当助手回复推回;放行 `text_end.content` → 前端收到 3 段 delta(文本重复);`agent_end` 也驱动 done → 自动重试期间发出 3 个 done;`prompt` response 当完成信号 → 模型还没开口就 done;去掉空 `toolcall_delta` 守卫 → 多出一个 ToolInput 完全相同的 tool_input;不翻译 `toolCall` → 工具块被静默丢弃;`Delta.Index` 恒为 0 → 工具入参被丢弃;`get_state` 不归一化 → 上层拿不到真实 sessionId。

**两个约束用例带对照组**(否则无法区分「被正确过滤」与「本来就不产生事件」):约束 2 用同样形状换成 `role=assistant` 断言必须下发 `full`;约束 7 用 index 错位的 delta 断言确实被丢弃(证明 `Index` 是被真实使用的键)。

**为何用外部测试包:** `pi_constraints_test.go` 要把 `ParseLine` 的输出穿过 `claude` 包的 `StreamProcessor`,而 `claude` 已 import `agent`,包内测试无法反向 import;`agent_test` 与 `agent` 是两个不同的包,`agent_test → claude → agent` 不成环。

**计数漂移(同 `ClaudeBin`「11 处 vs 实测 10 处」同类):** 本计划写「规格的 5 条约束逐条一个用例」,但规格的「实测确认的 pi 专有约束」只有 **3 条**编号约束(另有一节「其他实测细节」5 个要点)。实际写了 9 个:3 条编号约束 + `prompt` response 时序 + `agent_settled` vs `agent_end` + 空增量守卫 + `contentIndex` 一致性 + `toolCall` 翻译 + 错误可达前端。

**接受的缺口:** 没有真跑一次 pi 重新采集事件序列。规格明写它那一列「以 2026-09-12 实测为准(pi 0.85.1 + qwen3.8-max,`--mode rpc`,单轮 prompt)」,而本机正是 pi 0.85.1 + 同一模型(Task 2 的探针已确认 `model.id=qwen3.8-max`),故沿用「规格实测 + rpc.md」双重来源,不为此消耗配额。真实 spawn 下的端到端行为属 Task 11 范围。

**计划外收获:** 实测抓到 `extension_ui_request`(pi-subagents 在无任何请求时主动推 `setWidget`),它不在本任务的忽略清单里,靠「未知类型静默跳过」落地;已把真实样本写成用例钉住,免得后人给它加分支。

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

### ✅ Task 5 已完成(`c317065` + `b9e99d3`,2026-09-13)—— e2e 闸门已补跑至 12 passed,但过程中查出两处是非,见末尾

交付:`agent/resolver.go`、`agent/resolver_test.go`(7 个用例)、`agent/invariant_test.go`(2 个用例);改造 `claude/{session,query_pool,client}.go`、`db/models.go`、`main.go`(2 行)与 4 个测试文件的注入方式。

**三处 spawn 全部收口。** `buildCmdWithEnv` 改收 `agent.Protocol` 而不是 bin 字符串 —— 这是故意的:收 Protocol 让「二进制路径必走 `proto.Bin()`」在**类型上**成立,传字符串的话调用方完全可能再把一个硬编码的 `"claude"` 递进来。D1 抽成 `writeInitCommands(proto, stdin)`,**没有任何 `if backend == pi` 分支**;两个 `Start*` 是先 `readEvents` 再 `waitForInit(5s)`,而 `waitForInit` 等的正是这个响应,所以握手必须写在 `readEvents` **之前**(写晚了 pi 的响应就没人读,白等一轮 5s 超时)。

**两个我自己写出、又被自己的测试抓住的问题:**

1. **TTL 缓存顺序写反**。原先 `backendNameProvider()` 在缓存判断**之前**调用,于是每次 `Current()` 都读一次 DB,缓存只剩「不重建 Protocol」这一点收益,完全违背「避免每次 spawn 都打一次 DB」的目的。`TestResolver_TTLCacheAvoidsPerSpawnDBRead` 报出「TTL 内 5 次只该读 1 次,实际 5 次」。修正后 `cachedName` 变成只写不读的死状态,已一并删除。
2. **`client.go:23` 的惰性分支漏改**。本任务清单明写要替换 `agent.NewClaudeProtocol(c.BinPath, ...)`,第一遍漏了,是 `invariant_test` 判红才补上 —— 这正是把不变式写成可执行断言而不是写在文档里的价值。

**一处假绿(已修,并加了护栏):** `protocol()` 改走 `Current()` 后 `BinPath` 不再决定 spawn 哪个二进制,而 `client_test.go` 那 4 个用例仍在用 `NewClientWithPath` 注入。它们**依旧通过** —— 因为 `Current()` 未 Init 时返回的错误同样满足 `err != nil`。实测错误文本是 `agent.Init was never called`,即「二进制不存在」「上下文已取消」这两个行为一次都没被执行到。已改为 `initTestBackend` + `NewClient()`,并新增 `assertNotResolverError`(错误里出现 `agent.Init` 即判红),让这种假绿无法再次静默发生。变异验证:去掉 `initTestBackend` 后该断言确实报错。

**invariant_test 的两个设计点:**

- 豁免清单带**反 stale 机制**:豁免项若已不再命中就判红。否则清单只增不减,Task 8 做完也没人记得回来删,留着就等于给未来的泄漏开一张空白通行证。当前豁免:`dependencies/checker.go`(规格豁免)、`api/documents.go`(**Task 8 必须删**)、`claude/security.go` 的两个兼容垫片(Task 8 迁走 documents.go 后它们只剩测试调用点,应一并删除)。
- 带一条 `scanned < 20` 的下限断言:第一版把 `WalkDir` 的根节点(`".."`,其 `d.Name()` 也是 `".."`)当隐藏目录整个跳掉了,扫到 0 个文件。**没有这条下限断言,该测试会静默变成永真。**

**两处有意的越界(已论证):**

1. `main.go` 改了 2 行(两个池构造函数的实参)。R4 允许 Task 5 改「`backend/claude` 内部与其直接调用方」,而这两处正是直接调用点;不改则本任务的「`go build ./...` 全绿」闸门不可能成立 —— 计划把「去掉 `claudeBin` 实参」列在 Task 8,但 Task 5 删了形参,两者必须有一个先动。其余 8 处 `ClaudeBin` 字段注入未触碰。
2. `db/models.go` 的 `LLMBackend` 字段从 Task 7 提前到此处:`Current()` 要读它,否则 Task 5 无法编译。Task 7 仍负责 API 层的校验、探测与 `Invalidate` 接线,以及 `:22`/`:48` 两处注释的后端中立化(本次未动,保持任务边界可追溯)。

**D2 的连带影响(需知悉):** `SendSimple` / `SendWithOutput` 的旗标集变了(原本只有裸 `-p`,现在带 secure 旗标)。这是必需的:不走 `OnceArgs` 就拿不到 pi 的硬化旗标,pi 会以**全部内置工具(含 bash)**启动。两者在生产代码里**零调用点**(`SendWithOutput` 完全无人调用,`SendSimple` 只有两个错误路径单测),故不影响现有行为;若将来要启用 `SendSimple`,应先重新评估工具面(claude 侧从「无权限绕过」变成「绕过权限但 Bash/Task 硬阻断、文件工具过 hook」,而 WebFetch/WebSearch 故意不在黑名单里)。顺带修掉一个既有怪癖:`SendWithOutput` 原先把 prompt 同时放进 argv **和** stdin,两遗都送。

**遗留:** `Client.BinPath` 自此不再决定 spawn 哪个二进制,只是 `NewClientWithPath` 的遗留入参(尚有 5 个生产调用点:`ingest/{pipeline,sections×2,summary}.go`、`api/translate.go`)。Task 8 删掉这 5 处后,该字段与 `NewClientWithPath` 应一并移除。

**闸门状态:**

- ✅ `go build ./... && go vet ./... && go test ./...` 全绿(10 个包全 ok)
- ✅ 计划点名的 D1 回归护栏 `TestDocChat_PersistsChatSessionIDOnInit` 通过(0.17s,预算放宽到 8s 后不再贴近边界),api 包 7 个 docchat 用例全过
- ✅ `pytest tests/e2e/test_chat_streaming.py` —— **12 passed(198s)**,但这一项是分两步才拿到的,过程里查出两个真问题,见下。

#### ⚠️ Task 5 后发现:计划的任务排序会把应用留在坏掉的状态(已修 `b9e99d3`)

第一跑 e2e 是 **9 passed / 3 failed**。排查后确认不是 flaky,而是计划自身的矛盾:

- Task 5 把所有 spawn 点改成经 `agent.Current()` 取 Protocol,而计划把 `agent.Init` 的调用点排在 **Task 8**。两者之间应用是坏的,实测:
  `POST /api/query/message` → **HTTP 500 `{"error":"failed to create session"}`**。
  链路:`resolverReady` 为 false → `Current()` 报 "agent.Init was never called" →
  `StartSession`/`StartResumedSession` 失败 → handler **吞掉细节**只回一句笼统消息,
  日志里既无 `[session]` 行也无 resolve 错误 —— 这是它难发现的原因。
- 而 Task 5 的闸门写的正是「pytest ... 12 passed(claude 路径回归)」。**那道闸门在 Task 5 不可能满足。**

处置:把 `agent.Init` 提前到 Task 5(与本任务已提前的 `LLMBackend` 字段同理),Task 8 仍负责其余 8 处 `ClaudeBin` 字段注入的删除与 ingest/api 形参改造。`ClaudeSettingsPath` 传的是**函数**而不是值,故与 `InitSecurityConfig` 的先后顺序无关(传值时顺序写错会把路径固定成空串 → 不加 `--settings` → path-validator hook 整个不生效 → 静默 fail-open)。

修复前后对比(同一套 12 个用例):

| | 修复前 | 修复后 |
|---|---|---|
| `POST /api/query/message` | **0 次** | 11 次(全 200) |
| `POST /api/query/interrupt` | **0 次** | 3 次(全 200) |
| `[session] Sent message` | **0 条** | 11 条 |
| e2e | 9 passed / 3 failed | **12 passed** |

三个先前失败的用例(`test_thinking_indicator_shown`、`test_send_blocked_while_switch_in_flight`、`test_stop_preserves_partial_content`)修复后全部通过。另用直接 API 探测做了端到端验证:建会话 → 发消息 → 200 → 助手回复落库。

#### ⚠️ 顺带查出:e2e 套件对「后端坏掉」很不敏感,不能单独当验收依据

修复前那 9 个「通过」是**假绿**:它们的断言是输入框 disabled/enabled
(`wait_streaming_start`/`wait_streaming_complete`)与用户气泡可见(**乐观 UI**,与后端无关);
`test_no_*` 那类更是负向断言,在登录页上也照样通过(已实测:`test_desktop_no_mobile_dom.py`
在登录态失效时 2 passed / 1 failed,前两个就是假绿)。

即:**后端完全不可用时,这道闸门仍能拿到 9 passed。** 本次不改测试(超出范围),但记下给 Task 11:
拿 e2e 当验收依据时,必须同时核对服务端日志里确有 `[session] Sent message` 与
`POST /api/query/message` 200,否则可能验收了一个空转的前端。

#### e2e 跑起来的实际前提(供后人参考,计划未记)

1. `./start.sh` 即可起栈(它会 `make build` —— 前端 `npm run build` 产物 embed 进
   `backend/fs/dist`,后端单二进制同时服务前后端,故 9090 一个端口就够)。
2. **首次登录必须人在场**:`pytest.ini` 的 `addopts = --headed --browser chromium` 是有头的,
   `conftest.py:74-75` 把用户名/密码 `fill("")` 是**故意留空**,然后等 90 让人工
   填账号 + 认 4 位验证码(`api/auth.go` 用 `base64Captcha`)+ 点 Login。
   默认管理员密码也不是已知值(`db/db.go:49` 是 `generateRandomPassword(12)`,
   虽然 `:73` 会打进日志,但只在 `userCount == 0` 的首次 bootstrap)。
3. 登录成功后 `conftest.py:105` 把状态写进 `tests/e2e/.auth/state.json`,之后非交互。
   **坑:** 该文件过期后,`conftest.py:43-52` 的有效性检查会**误判为仍登录** ——
   它 `goto("/")` 后立刻看 `page.url.endswith("/login")`,而 SPA 的重定向是异步的
   (要先打一次 API 才知道 token 失效),于是那一刻 URL 还是 `/`。后果是跳过人工登录、
   用例在登录页上空转。实测踩过:**删掉 `tests/e2e/.auth/state.json` 再跑**即可。

## Task 6: 沙箱 extension `backend/scripts/pi-path-validator.ts`

- [ ] `pi.on("tool_call", ...)` 四步:白名单外一律 block(文件工具 ∪ env `PI_WEB_TOOLS`);文件工具提路径→realpath→必须落在 `ALLOWED_DIR` 内(**分隔符边界比较**,防 `/u/1` 匹配 `/u/10`)+ 敏感路径正则 block;`fetch_content` 校验 `url` 与 `urls[]` **全部元素**(仅 `http:`/`https:`,拒绝绝对/相对本地路径与 `file:`/`data:`/`gopher:`/`ftp:` 等);`ALLOWED_DIR` 未设置→文件工具全拒(fail-closed)
- [ ] 敏感路径正则表与 `path-validator.py` **逐条同步**(`/etc/shadow`、`~/.ssh`、`~/.aws`、Keychains 等,含 macOS `/private` 前缀)
- [ ] `ALLOWED_DIR`/`PI_WEB_TOOLS` 都从 `process.env` 读,不硬编码工具名
- [ ] 产物落 `backend/scripts/`(tracked);在 `SECURITY_DEPLOYMENT.md` 的 cp 步骤与 Dockerfile `COPY` 步骤里补上这个文件,并在本地复制一份到运行时 `scripts/`,否则 Task 11 的集成测试无从跑起
- [ ] **不**实现 IP/DNS 级 SSRF 校验(那是 `pi-web-access/ssrf-protection.ts` 的职责,规格已论证它更严)
- [ ] `security_test.go`:把 `TestDangerousToolsCrossLanguageSync` 扩为三语言(同时解析 TS 的拒绝集合与敏感路径正则,任一漂移即失败)
- [ ] 新增 TS 用例(以 shell 驱动,参照既有 `TestPathValidator_WebFetchSSRF` 的手法):路径边界 5 条 + `fetch_content` URL 校验 8 条(含 `urls[]` 任一非法即整体 block、空 `url`/空 `urls` 拒绝)
- [ ] 配置一致性测试已在 Task 1 随产物落地(`backend/scripts/web-search.json.sample`),本任务只需复核其仍然通过

**闸门:** 全局闸门 + 类型检查。**实测可用的命令**(原计划的 `npx tsc --noEmit scripts/pi-path-validator.ts` 有四个问题:路径已改为 `backend/scripts/`;`npx` 会联网下载而本机 `frontend` 已有 tsc;**TS 6.0 起在 `frontend/` 下跑会报 `TS5112`** —— cwd 有 `tsconfig.json` 时命令行不能指定文件,故必须从仓库根跑;**必须显式 `--types node`**,否则 `@types/node` 不加载、出 15 个 TS2591 并级联一个假的 TS2534):

```
frontend/node_modules/.bin/tsc --noEmit --strict --target es2022 --module esnext \
  --moduleResolution bundler --typeRoots frontend/node_modules/@types --types node \
  backend/scripts/pi-path-validator.ts
```

不引入任何需要 `npm install` 的依赖,`frontend/package.json` 不动
**提交:** `feat(security): pi 沙箱 extension 与三语言同步测试`

### ✅ Task 6 已完成(`acadd70`,2026-09-13)

交付:`backend/scripts/pi-path-validator.ts`(463 行,导出纯函数 `validateToolCall`,hook 与 CLI 入口共用)、`backend/claude/security_test.go`(+375:三语言同步 + 路径边界 17 例 + `fetch_content` URL 校验 19 例)、`backend/scripts/SECURITY_DEPLOYMENT.md`(+18/-6:目录树、cp 步骤、env 表补 `PI_WEB_TOOLS`、Dockerfile COPY)、运行时副本 `scripts/pi-path-validator.ts`(未跟踪,与 tracked 源 `diff -q` 一致)。

**控制方独立复核:** 44 条敏感路径正则 Python ↔ TS **逐条且同序相同**(程序化提取比对,不是手抄;TS 侧用 `String.raw` 而非普通字符串,因为 JS 普通字符串会吃掉未知转义的反斜杠、`'\.' === '.'`,正则语义就不再与 Python 的 `r'...'` 一致);pi hook API 引用属实(`docs/extensions.md:798-806` 的 `event.input` 可读可改、`types.d.ts:818-828` 的 `{block?,reason?,terminate?}`,与规格的 `{block:true,reason}` 一致);tsc 闸门退出码 0;`ok claude/config/agent`;全量 `go test ./...` 仅 3 个已知环境性失败。测试**有牙**(变异检验:删一条 TS 正则 → `count drift: Python=44 TS=43`;往拒绝集合注入 `read` → 两条断言同时报错;改一条 Python 正则 → 双向报错)。

**已核实的 pi 侧事实(后续任务可直接引用):**

| 事实 | 依据 |
|---|---|
| 六个文件工具的路径字段都叫 `path`:`read`/`write`/`edit` 必填,`grep`/`find`/`ls` 可选(缺省即 cwd) | `core/tools/*.d.ts` 的 schema;`types.d.ts:678-724`(自定义工具走 `CustomToolCallEvent.input: Record<string, unknown>`) |
| `find`/`grep` 的 `pattern`/`glob` **不需要**校验:只作为已校验根目录**内部**的匹配模式传给 fd/rg | `core/tools/find.js:78`、`grep.js:100` |
| `tool_call` hook 抛错即 block(fail-safe) | `docs/extensions.md:2925` |
| `fetch_content` 入参是 `url` + `urls[]`,两者都要校验 | `pi-web-access/index.ts:2492-2494` |
| `ctx.cwd` 可用于解析相对路径 | `types.d.ts:217`;`find.js:65`、`grep.js:57` 的 `resolveToCwd(..., ctx?.cwd \|\| cwd)` |

**比规格描述更严重的一条:** `video-extract.ts:337` 的 `readFile(info.absolutePath)` 紧接 `:338-341` 会把内容 **PUT 上传到 Gemini** —— 不只是「任意绝对路径读取」,是读取 + 外泄。`:211-214` 的 `execFileSync("ffmpeg", ...)` 与规格一致(规格写 `:213`,实际调用跨 211-214)。这正是本设计新增 URL 校验的理由。

**4 处偏离(均为更强或必要,已复核):**
1. **URL 校验按入参键名而非工具名** —— 任何被放行的工具凡带 `url`/`urls` 就校验。理由:工具名可被 `toolNames` 改写,硬编码名字会在运维改名后**静默失效**;顺带覆盖 `get_search_content`(它也有 `url` 参数,`index.ts:2840`,但其 `execute` 只从缓存取、不发起抓取,故不构成第二条文件向量)。有用例 `改名后的工具_仍被校验` 钉住
2. **`ALLOWED_DIR` 未设置时拒绝一切**(含 web 工具),比任务书的「文件工具全部 block」更强,与 Python 版 `main()` 在分派到具体工具之前就 deny 一致
3. **相对路径以 `ctx.cwd` 为基准**(Python 版以 `ALLOWED_DIR` 为基准)。生产布局下等价(`cmd.Dir = userDir` 且 `ALLOWED_DIR = realpath(userDir)`);不一致时以 cwd 为准才不会放行 pi 真会访问的路径。有用例 `cwd在目录外_省略path_拒绝` 验证 fail-closed
4. **不用 `import type { ExtensionAPI }`** —— 该包在 `/opt/homebrew/lib/node_modules`,不在仓库解析链上,`tsc` 会 TS2307;加 tsconfig paths 或装依赖都会污染前端工程。改为最小结构化声明 + 引用权威行号;运行时类型全被擦除,不影响行为

**本任务最大的未验证面(留给 Task 11):** 未验证「pi 真的会加载 `-e` 指定的 extension 并调用该 hook」。CLI 入口与 hook 共用同一纯函数,校验逻辑已被 36 个用例覆盖,但真实 spawn 下的拦截行为属集成测试范围。

**已知非缺陷:** TOCTOU(hook 校验 realpath 与工具实际执行之间有时间窗,Python 版同样存在,未加剧);不做 IP/DNS 级 SSRF(规格分工),故 `http://127.0.0.1:6379/` 在本层**放行**、由 `pi-web-access/ssrf-protection.ts` 拦,测试里有显式用例与注释以免后人误判为漏洞。

## Task 7: DB 字段 + Admin API 开关与探测

- [ ] `db/models.go`:`GlobalSettings` 增 `LLMBackend string \`gorm:"default:claude" json:"llmBackend"\``(`AutoMigrate` 自动加列,无数据迁移);`:22`/`:48` 注释改为后端中立(`agent session ID (claude or pi) for resume`),**仅注释,不动字段名与 JSON tag**
- [ ] `admin_settings.go`:`globalSettingsResponse` 增 `llmBackend`;`UpdateGlobalSettings` 输入增 `LLMBackend`,沿用既有部分更新风格(空串表示不改);取值非 `"claude"`/`"pi"` → 400;取值为 `"pi"` 时探测(`LookPath` + `pi --version`,5s),失败 → 400 且消息含安装指引 `npm install -g @earendil-works/pi-coding-agent`;成功后 `agent.Invalidate()`
- [ ] **403 与 400 的消息必须可区分**(规格「管理员权限模型」小节的要求)
- [ ] API 测试:`"pi"` 且不可用→400;`"gemini"`→400;`"claude"`→200 且**不触发探测**;`GET` 含 `llmBackend`

**闸门:** 全局闸门
**提交:** `feat(admin): Settings 增 llmBackend 开关与 pi 可用性探测`

### ✅ Task 7 已完成(`0320e7a`,2026-09-13)

交付:`api/admin_settings.go` 的开关与校验、`api/admin_settings_test.go`(13 个用例,该接口此前**无任何测试**)、`agent.ProbePi`、`config.ValidatePiWebConfig`;并补上 `db/models.go:22`/`:48` 两处注释的后端中立化(仅注释)。

`db.GlobalSettings.LLMBackend` 字段本身已随 Task 5 提前落地(`agent.Current` 要读它),本任务只做 API 层。

**R8 已按 D6 处置**(静态预检,不在 handler 里 spawn)。`agent.ProbePi(ctx)` 三段由浅入深:①`LookPath` + `pi --version`;②`SessionArgs`(沙箱 extension 缺失即报错 —— 把 R6 从「首次聊天才炸」提前到「切换时就 400」);③`ValidatePiWebConfig` 的 problem 为空。

**顺带补上 Task 1 未覆盖的跨键重名检测。** 它必须按 `tools.*.enabled` 过滤 —— pi 的 `resolveToolNames`(`index.ts:303-311`)只对**已启用**的键查重名,不过滤就会对 pi 其实接受的重名误报,把一次合法的后端切换拦成 400。有用例带对照组钉住(`sourceCheck` 关掉时重名合法、开着时必须 400)。同时删掉 Task 2 在 `resolvePiWebTools` 里那份重复的重名告警(检测已下沉到 config,两处各告一次只会让同一条问题在日志里出现两遍),**去重本身保留**。Task 1 的告警测试也补了两个子用例(11 → 13),否则「运行时告警」这条路径无人守。

**API 行为的四个决定:**

- 非法取值 → 400 并回显收到的值,**不静默忽略**:前端下拉框只有两个选项,能走到这里说明请求是手造的或前后端版本不一致,静默忽略会让调用方以为切换成功了
- `"claude"` → **不探测**。claude 是默认后端与回退值,给它加探测会堵住「pi 已经坏了、切回 claude 自救」这条路,而那正是运维最需要的逃生门。用例用 **marker 文件**证明假 pi 确实没被调用(而不是靠推断)
- 400 与中间件的 403(`"admin access required"`)消息可区分(规格硬要求)
- 只在**确实改了后端**时才调 `agent.Invalidate()`:改翻译配置不该把会话层的 Protocol 缓存也丢掉

**变异检验 5 处被抓:** 切到 pi 时不探测(200 而非 400,且 llmBackend 被写进 DB);保存后不调 Invalidate(`Current()` 仍返回 claude);切到 claude 也探测(marker 出现);ProbePi 去掉 R8 预检(3 个子用例全部变成 200);重名检测不按 enabled 过滤。另:`invariant_test` 的规则 3 单独验过有牙(在 api 里注入一处 `pr.Backend() == agent.BackendPi` → 判红)。

**修掉自己写的一个误报:** `invariant_test` 规则 3 原先用裸子串 `BackendPi` 匹配,结果把 api 层自己定义的常量 `llmBackendPi` 误判成「后端种类判断」—— 那是 API 契约里的取值字面量,不是后端分支。改为匹配限定名 `agent.BackendPi`/`agent.BackendClaude` 与 `.Backend() ==`/`!=`。**修的是测试的匹配精度,而不是为了让糙测试通过去改一个合理的命名。**

**两次「变异没生效却以为通过」的教训(已改进做法):** 本轮有两处我最初以为变异被抓/通过,实际是变异根本没落地:一次 perl 模式没匹配上,一次替换让 `fmt` 变成未使用而编译失败 —— 而我的 grep 只过滤 `^(ok|--- FAIL)`,把 `FAIL ... [build failed]` 漏掉了。现在每次变异都先 grep 确认标记文本存在再跑测试,且 grep 模式包含 build failed。

**一个没写成代码的发现:** 我原本担心 GORM 对带 `default` 标签的零值字段会从 INSERT 里省略、导致刚 `FirstOrCreate` 出来的内存对象里 `LLMBackend` 是空串(那会让前端下拉框显示为空白),准备加一层归一化。先用用例实测:**它确实返回 `"claude"`**,所以没加那段防御代码(避免为不存在的场景写兜底)。用例本身留下,以防 GORM 行为变化。

**遗留:** 前端开关是 Task 9。在那之前 `llmBackend` 只能靠 API 改,且 GET 已经会返回它。

## Task 8: `main.go` 去注入 + `ingest`/`api` 形参改造

- [x] `main.go`:~~启动时 `agent.Init(cfg.ClaudeBin, cfg.PiBin)`~~(**已由 `b9e99d3` 提前完成**,否则 Task 5 的 e2e 闸门不可能满足);移除 8 处剩下的 `ClaudeBin` 字段注入;`NewSessionPool`/`NewQuerySessionPool` 的 `claudeBin` 实参**已删**
- [x] 连带处理 Task 5 留下的遗留:`Client.BinPath` 与 `NewClientWithPath` **已删除**;`claude/security.go` 的 `BuildSecureArgs`/`BuildSecureEnv`/`DangerousDisallowedTools` 三个垫片**已删除**,`invariant_test` 的两条豁免已摘除且仍全绿
- [x] `api/{documents,query,raw,rss,sections,translate,web,newsletter,blog}.go`:删除 `ClaudeBin string` 字段及其构造处(**实际是 7 个 struct,比计划列的 5 个多**;`sections.go` 与 `documents.go` 共用 `DocHandler`)
- [x] `api/documents.go` 的 PDF 逐页转换改走 `agent.Current()` + `OnceArgs("", []string{"Read"}, false, "sonnet")` + `proto.Bin()` + prompt 入 stdin(D2/D3);`claude.BuildSecureEnv(tempDir)` 改为 `proto.Env(tempDir)`
- [x] `ingest/{pipeline,sections,summary}.go`:`claudeBin string` 形参删除。~~改为用 `agent.Current()` 构造的 Client~~ → **改为 `claude.NewClient()`**,原因见下方完成记录
- [x] 连带修正所有调用点签名(含 5 个测试文件里的 handler 字面量)

**闸门:** 全局闸门 + `pytest tests/e2e/test_chat_streaming.py` 12 passed —— **均已满足**(e2e 12 passed / 117s,重建二进制并重启服务后跑)
**提交:** `refactor: 收口 Plan 1 遗留接缝,once 调用链路全部改走 resolver`(`8cf8aa7`;计划原文的提交消息是 `refactor(agent): main 与 ingest/api 去除 ClaudeBin 注入`,实际改动范围比它大,改用更准确的描述)

### ✅ Task 8 已完成(`8cf8aa7`,2026-09-13)

**一处比计划描述更简单的发现:** 计划 Step 3 要求把 ingest/api 从 `NewClientWithPath` 迁到 `agent.Current()`,Step 1 还给了一个 `agent.NewClient()` 的草案。实际不需要:Task 5 已经让 `Client.protocol()` 在 `Proto` 为 nil 时惰性走 `agent.Current()`,所以 **`claude.NewClient()`(无参)本身就是后端中立的**。改动于是退化成「`NewClientWithPath(x)` → `NewClient()`」+ 摘掉形参,不必新增任何 API。

计划草案里那个 `agent.NewClient()` 出错时返回 `nil`,是个地雷(调用方会 nil-deref)。既然不需要它,就没有引入 —— 也就没把这个地雷带进代码库。

**比计划更大的一块:`ClaudeBin` 同时是功能开关。** `h.ClaudeBin != ""` 在 10 处被当作「LLM 是否可用」的开关(异步摘要生成的入口),`api/sections.go` 另有 2 处 `== ""` 的 503 早退。**只删 main.go 的注入而保留这些 gate,会让条件恒假、异步摘要生成静默停摆** —— 这是本任务最容易踩的坑。

先确认再动手:`config.go:46-48` 把 `ClaudeBin` 兜底成 `"claude"`、永不为空,所以 `!= ""` 恒真、`== ""` 恒假,它们全是死条件。删字段与删 gate 必须同批完成。「LLM 到底可不可用」现在由 resolver 在实际调用时判定并返回错误,比在入口处靠一个字符串是否为空来猜更准确(切到 pi 之后,那个字符串检查压根不反映真实后端)。

**垫片删除时保住了测试覆盖。** `claude/security_test.go` 里有 12 个测试,其中 5 个与垫片无关且必须留(`TestCleanupStaleSettings_AgeGated`、`TestPathValidator_WebFetchSSRF`、`TestDangerousToolsCrossLanguageSync`,以及 Task 6 的两个 `TestPiPathValidator_*`),所以不能整文件删。剩下 7 个分两类:4 个在 agent 包已有等价覆盖(底层都是同一个 `SecureArgs`/`Env`)直接删;**3 个没有 agent 对应物**(`EmptyAllowedToolsOmitsFlag`、`BypassFlagPresent`、`DangerousDisallowedTools_CoversKnownAttackVectors`)**搬进** `agent/claude_args_test.go`。先逐个核对覆盖再删,否则「删垫片」会顺手删掉唯一的安全断言。

另:`DangerousDisallowedTools` 别名不只为兼容外部,`security.go:146` 内部也在用,且 Task 6/10 的跳语言漂移测试依赖它 —— 全部改为直连 `agent.ClaudeDangerousDisallowedTools`。

**测试。** 新增 `TestClientProtocol_FollowsResolver`:同一个 Client 实例、只换 resolver 指向,断言 `protocol()` 分别返回 claude 与 pi。这是 Task 8 的核心性质。配套的 `TestNewClient_DoesNotPinABackend` 只证明「构造时没钉死」,证明不了「真的会跟随」(一个恒返回 claude 的 `protocol()` 同样能通过它),所以两个都要。变异验证:把 `protocol()` 改成恒返回 `ClaudeProtocol` → 两个后端分支都判红。原 `TestNewClient` 断言的是 `BinPath == "claude"`(即「默认钉死 claude」),与后端开关直接矛盾,随字段一并删除。

**额外闸门:** `go test ./api/ -race` 无数据竞争。这是针对本任务特有风险的:测试里原先用 `ClaudeBin: ""` 让 gate 恒假、从而跳过异步 ingest goroutine;gate 删掉后这些测试可能开始真的起 goroutine。

**我自己犯的一个错,已回退。** 图省事跑了 `gofmt -w ingest/` 和 `gofmt -w claude/`(整目录),波及 5 个我本不想改的文件,其中 `claude/stream.go` 里正是 **SSEEvent 结构体** —— 那是「前端聊天代码零 diff」的关键文件。虽然 JSON tag 没变、只是注释对齐,但这种噪音必须避免:已全部 `git checkout` 回退。

同理,`api/{newsletter,raw,rss,web}.go` 在 HEAD 本来就不合规(`gofmt -l` 命中),所以对它们跑整文件 gofmt 会混进无关重排(实测确实混进了 `childrenCount-1`、结构体字段对齐、整块重缩进)。已回退重做,改成只让**我碰的区域**合规,并用 `gofmt -d <file> | grep <我改的标识符>` 逐个确认为 0。**教训:在这个仓库里不能用 `gofmt -w <目录>`,因为部分文件在 HEAD 就不合规。**

顺带发现 `api/newsletter.go` 那个 gate 在 HEAD 就多缩进了一层(3 tab,而外层作用域是 2 tab)—— 这正是它不合规的原因。这些行已在本次 diff 内,顺手对齐。

**遗留(记入 Task 11):** PDF 逐页转换这条路径没有任何自动化覆盖(e2e 与 go test 都碰不到,它需要真实 LLM + PDF + 逐页 PNG)。本次只做了逐行复核,确认与 `SendSimpleWithRead` 的既有模式一致(`-p` + stdin + text 输出),且 `TestClaudeOnceArgs_TextModeMatchesSendSimpleWithRead` 已钉住 arg 形状。Task 11 应补一个用假二进制的测试:断言 prompt 确实从 stdin 进去、且用的是 `proto.Bin()`。

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
- [ ] **必须写进运维警示(Task 1 已实测)**:`web-search.json` 的 `toolNames` 写错会让**整个 pi 后端 `exit 1`**(不只是联网不可用,连不用 web 工具的 ingest 链路一起死),而 Settings 里切换到 pi 时的探测跑的是 `pi --version`、**不加载扩展因而探测不到**。有效配置路径 = `$PI_CODING_AGENT_DIR/web-search.json`,未设该 env 时为 `<服务账号 HOME>/.pi/agent/web-search.json`,**不是** `$XDG_CONFIG_HOME/pi/`
- [ ] 写清规格的三条运维警示:①切换后端会使进行中的对话丢失上下文续接能力(历史消息仍在 DB),且**作废是双向的**、「切回原后端可恢复原对话」不成立;②后端切换仅 `admin` 账号可操作,**不要重命名或删除该账号**(`db/db.go:38` 那条无守卫迁移会把它升回,改名即锁死 UI 开关);③`--tools` 拦不住扩展**加载期**的任意代码,须 pin `pi-web-access` 版本并管控 `settings.json` 的 `packages` 列表
- [ ] **第四条运维警示(Task 3 源码核实新增,见 R9)**:`web-search.json` 必须显式关掉 `commands.{websearch,curator,search,google-account}.enabled`,否则**任何用户发一条以 `/` 开头的文档问答消息就能直接执行扩展代码**。要写清四道既有防线为何都拦不住(`--tools` 只管工具调用、`--no-skills`/`--no-prompt-templates` 只关 skill 与模板、沙箱 `input` hook 在命令派发**之后**才发),以及其他已加载包的命令只能靠 R1 的运维隔离覆盖

**闸门:** `bash -n start.sh`;`./start.sh` 能起来(不实际消耗配额)
**提交:** `docs: pi 后端的前提、部署安全键与运维警示`

## Task 11: 集成测试、e2e 回归与验收

- [ ] Go 集成测试:spawn 真实 `pi --mode rpc`,发 prompt 要求读 `ALLOWED_DIR` 外文件,断言工具调用被 block;`pi` 不在 PATH 时 `t.Skip`。**并必须证明 extension 确实被加载**(而不是因文件缺失/路径写错被静默跳过)—— 这是 Task 6 明确留下的最大未验证面:一个静默未加载的沙箱与一个正常工作的沙箱,在「没有越界访问发生」时看起来完全一样
- [ ] **本地文件向量集成测试**(本次新增控制的核心验证;`pi-web-access` 未装时 `t.Skip`):诱导 `fetch_content` 取 `ALLOWED_DIR` 外的本地路径(绝对路径视频 / `/etc/passwd`),断言被 hook block,且 `video-extract.ts` 的 `readFile(absolutePath)` 与 `execFileSync("ffmpeg", ...)` 未被触达
- [ ] e2e 回归:`pytest tests/e2e/test_chat_streaming.py`(12)、`test_chat_view.py`、`test_mobile_chat_view.py`、`test_desktop_no_mobile_dom.py`
- [ ] 留意既有脆弱用例 `api.TestDocChat_PersistsChatSessionIDOnInit`:`api/docchat_test.go:17-29` 的假 claude 脚本 `printf` init 事件后 `sleep 5`,而测试等 `onRealSessionID` 的预算只有 `3 * time.Second`(`:71-78`),`go test ./...` 多包并行时 spawn `/bin/sh` + 调度即可超时(Task 1 修复轮实测到一次,单独跑与连跑 10 次均 PASS,且基线同样偶发)。**它恰好是 D1「把 pi 的 `get_state` 响应归一化成 `system/init`」最直接的回归护栏**,Task 5 必须保持它绿;本任务顺手把预算放宽到 8s
  - **✅ 已提前完成(`a815e62`,2026-09-13)**:预算已放宽到 8s。提前做的原因是 Task 2~5 的闸门都是 `go test ./...` 全绿,而实测它在多包并行时以 3.01s 撞满预算失败(单独跑 5/5 PASS、仅 0.52s),一个会随机红的护栏等于没有护栏。**同文件仍有 4 处同类 3s 预算未动**(`:236` 的 `TestDocChat_ResumeFailureClearsCachedID` 是 `time.After`,`:125`/`:175`/`:367` 是 `deadline := time.Now().Add(...)` 轮询),均未观测到 flake,按最小改动不一并放宽;若 Task 5/11 期间偶发红,应先怀疑这几处
- [ ] **扩展命令注入面验证(R9)**:用部署模板的 `web-search.json` spawn 真实 pi,发一条 `/curator hello` 与一条 `/search foo` 作为文档问答消息,断言**没有任何扩展命令被执行**(浏览器不被拉起、命令的 `response` 不出现),消息按普通文本进 LLM。对照组:临时把 `commands.curator.enabled` 改成 `true`,断言命令**确实**会被执行 —— 没有对照组就无法区分「被关掉了」与「本来就没触发」
- [ ] **PDF 逐页转 Markdown 的覆盖缺口**(Task 8 遗留):`api/documents.go` 的 `LLMExtract` 已改走 `agent.Current()` + `OnceArgs` + `proto.Bin()` + prompt 入 stdin,但这条路径**没有任何自动化测试**(e2e 与 go test 都碰不到,它需要真实 LLM + PDF + 逐页 PNG)。Task 8 只做了逐行复核。补一个用假二进制的测试:断言 prompt 确实从 **stdin** 进去(而不是 argv)、用的是 `proto.Bin()`、且 `--model sonnet` 在 claude 侧出现而在 pi 侧被忽略(D3)
- [ ] 手工验收(**两种后端各跑一遍**,由人执行,不消耗配额的自动化不得替代):文档问答多轮 + SSE 断线重连、自由问答带图片、中途 interrupt、ingest 摘要与分节。**补上 PDF 逐页转 Markdown**(同上条,它是 Task 8 改动里唯一既无测试又难自动化的路径)
- [ ] resume 往返验收(两种后端各一遍):第一轮后查 DB 确认 `chat_session_id`/`session_id` 写入的是**该后端自己的** ID 格式;重启进程或等 30s 清理后再提问,确认走 `--resume`/`--session` 且上下文续接成功
- [ ] `LLMBackend=pi` 时把 Settings 切到 pi,重跑 e2e 的聊天用例,确认前端零改动即可工作

**完成定义(规格原文,逐条勾)**

### ⏳ Task 11 部分完成(`aa73189`,2026-09-13)—— 零配额与单回合可做的部分已做完,其余见下方「剩余项」

**已完成:**

- ✅ **沙箱 extension 确实被加载并拦截**(Task 6 留下的最大未验证面):`agent/pi_sandbox_integration_test.go` 用生产路径的 argv/env spawn 真实 pi、诱导越界读 `/etc/passwd`、断言被拦。锚点是 extension 自己产出的理由文本,它只可能来自被加载执行了的 extension,因此**同时就是加载证明**;并反向断言 `/etc/passwd` 内容未泄漏。实跑 PASS(3.07s)。gated 在 `LLM_KNOWLEDGE_PI_INTEGRATION=1` 之后,默认 SKIP(已验证),所以计划闸门 `go test ./...` 保持零配额。
  - **断言收紧过一次**:最初只匹配 `"Access denied:"` 前缀,但「工具不在白名单」与「ALLOWED_DIR 未配置」也用这个前缀 —— 拿它当通过条件会让用例因**错误的原因**变绿。改为只认 `path outside allowed directory` / `sensitive file`,并对 `ALLOWED_DIR not configured` 直接判失败。收紧后仍 PASS,证明拦下它的确实是路径校验本身。
- ✅ **PDF 逐页转 Markdown 的覆盖缺口**(Task 8 遗留):`api/documents_llmextract_test.go` 用假二进制断言 prompt 走 stdin(D2)、用的是 Protocol 给的二进制、`--model sonnet` 在 argv(D3)、安全旗标未丢失、产物链路通。变异验证:prompt 塞回 argv → 判红。
- ✅ **R10 新发现与护栏**(见风险登记):rpc `bash` 命令绕过沙箱,`TestPiEncodeUserMessage_CannotInjectRpcCommands` 钉住唯一屏障。
- ✅ 顺带零配额实证 **D4**:`get_state` 的 `sessionFile` 落在 `Env()` 注入的 `PI_CODING_AGENT_SESSION_DIR` 之下。同时确认 `get_state` **不返回已加载扩展列表**,所以「证明扩展被加载」没有零配额捷径。
- ✅ 完成定义中的两条已由前序任务满足并实测:「前端聊天代码 diff 为空」(Task 9)、`Settings 切到不可用的 pi 时被 400 拦住`(Task 9 的 e2e 移走沙箱文件实跑过,错误文案原样透到 UI)。

**查出一个既有 bug(不在 Plan 2 范围,未修,已开 issue #93 —— https://github.com/bruceding/llm_knowledge/issues/93):** `api/documents.go` 的 `LLMExtract` 与 `pdftoppm` 对页码补零的约定不一致 —— pdftoppm 按**总页数**决定补零宽度(10 页→`page-01.png`,1 页→`page-1.png`),handler 却硬编码 `page-%02d.png`。后果:**任何少于 10 页的 PDF 都静默产出空 `paper.md`,却返回 200 与 "PDF extracted with LLM successfully"**。arXiv 论文通常 ≥10 页,这解释了它为何一直没被发现。不在这里修是因为修它会改变 claude 路径行为、违背本计划验收项之一;修法建议:pdftoppm 之后 glob `page-*.png` 并按数字后缀映射,而不是猜补零宽度。**待维护者决定是否单开一个 commit 修。**

**剩余项(需要配额或需要人执行,故未做):**

- ⬜ **本地文件向量集成测试**(`fetch_content` 取 ALLOWED_DIR 外的绝对路径视频 / `/etc/passwd`,断言被 hook block 且 `video-extract.ts` 的 `readFile`/`execFileSync("ffmpeg")` 未触达)。需要 `pi-web-access` 真实联网工具 + LLM 回合。
- ⬜ **R9 扩展命令注入面验证**
  - **2026-09-13 尝试过一次廉价路径,结论不确定,未采信**:本想用 rpc 的 `get_commands` 做零 LLM 回合的判据(对照组:同一份配置只改 `commands.*.enabled`)。实测两组都只返回 1 条命令、都不含那四个 —— 原因是把 `PI_CODING_AGENT_DIR` 指到空临时目录会让 pi 读不到 `settings.json`,于是 **`pi-web-access` 根本没被加载**,两组自然都没有命令。**要做这个对照,必须把真实的 `settings.json` 一并复制进临时 agent 目录**(且 R1 提醒我们:那份 settings.json 里还有 betterwright / pi-subagents 等包,复制过去等于让它们也加载并执行加载期代码)。因此本项仍待做,不得视为已验证。
(发 `/curator hello` 与 `/search foo`,断言无扩展命令被执行;**并需对照组**把 `commands.curator.enabled` 改成 true 断言命令确实会执行)。对照组会拉起浏览器,需人确认时机。
- ⬜ e2e 回归的另外三个文件(`test_chat_view.py`、`test_mobile_chat_view.py`、`test_desktop_no_mobile_dom.py`)。`test_chat_streaming.py` 的 12 个已在 Task 5/8 各跑过一次全绿。**阻塞点**:`tests/e2e/.auth/state.json` 里的 token 已过期(是 `bruceding` 的),而 conftest 的刷新流程需要人手输凭据+验证码。
- ⬜ `LLMBackend=pi` 时切到 pi 重跑聊天 e2e(验证前端零改动即可工作)。消耗配额。
- ⬜ **手工验收**(计划明确「由人执行,不消耗配额的自动化不得替代」):两种后端各跑一遍 —— 文档问答多轮 + SSE 断线重连、自由问答带图片、中途 interrupt、ingest 摘要与分节、PDF 逐页转 Markdown。
- ⬜ **resume 往返验收**(两种后端各一遍):确认 DB 里写入的是该后端自己的 ID 格式,重启或等 30s 清理后再提问能续接。
- ⬜ 完成定义中的「`LLMBackend=pi` 时手工验收全部通过」依赖上面两条人工项。

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
| R8 | `web-search.json` 的 `toolNames` 写错 → pi-web-access 扩展加载失败 → **`pi` 以退出码 1 退出**(`main.js:723-731`),整个 pi 后端不可用;而切换探测跑 `pi --version` 不加载扩展,**探测不到**(Task 1 已实测:对照组退出 0、实验组退出 1) | Go 侧在该支 `log.Printf` 告警并由 11 个子用例钉住;Task 10 写入运维警示;Task 7 的探测**不能**只靠 `pi --version`(可考虑追加一次带扩展的最小 spawn,代价是探测变慢——留给 Task 7 决定) |
| R9 | **扩展命令注入面(Task 3 对 pi 0.85.1 源码核实时发现,规格与本计划都未覆盖)**:rpc 模式下以 `/` 开头的用户消息会被当成扩展命令派发执行 —— `modes/rpc/rpc-mode.js:301-304` 调 `session.prompt()` 时**没传** `expandPromptTemplates`,而 `core/agent-session.js:822` 的默认值是 `true`,`:828-834` 一旦 `text.startsWith("/")` 就调 `_tryExecuteExtensionCommand`(`:954-961`,`getCommand` 命中即执行)。**四道既有防线全拦不住**:`--tools` 只管工具调用(命令是扩展自己的 JS)、`--no-skills`/`--no-prompt-templates` 只关 skill 与模板、沙箱 extension 的 `input` hook 在 `:839-851`(位于 `:828` 的命令派发**之后**)。已实测的可利用面:`pi-web-access` 自己注册 4 个命令(`index.ts:3164` websearch、`:3427` curator、`:3469` google-account、`:3517` search),本机的 `pi-subagents` 也注册;`/curator` 会拉起浏览器,且扩展命令自行驱动 LLM、绕过我们注入的 `--system-prompt`。**Claude 侧的对应物是 `SlashCommand` 工具,它早就在 `ClaudeDangerousDisallowedTools` 里被硬阻断** —— 所以堵住它是追平两个后端的安全强度,不是额外收紧 | 部署模板显式关掉 `pi-web-access` 的 4 个命令(`commands.*.enabled=false`;`isCommandEnabled` 在 `index.ts:277-279` 是 `!== false`,**默认开**),由 `config_test.go` 的 `TestWebSearchSample_PinsSecurityKeys` 守护(4 处变异均被抓,含「模板里多出未知的 `enabled:true` 命令」)。**残留:其他已加载包的命令不在覆盖范围内**,只能靠 R1 的运维隔离。故意**不**在 `EncodeUserMessage` 里改写以 `/` 开头的用户文本(会污染 LLM 输入与 DB 历史,且与 claude 后端的「/ 就是普通文本」不一致)。Task 10 写第四条运维警示;Task 11 需带对照组验证 |

| R10 | **rpc `bash` 命令完全绕过沙箱 extension(Task 11 实测新增)**:用我们的硬化 argv 启动 pi、发 `{"type":"bash","command":"touch <ALLOWED_DIR>/x && echo BASH_RAN"}`,shell **真的执行了**、文件真的建了、`tool_call` hook 一次都没触发。它是 pi 的直接 shell 执行路径(`docs/rpc.md:479-485`)而非工具调用,且 `pi --help` 里**没有任何旗标能关掉它**(`--tools` 白名单不含 bash 也照跑;`-nt/--no-tools` 关的是工具;`-na` 是 `--no-approve`)。因此唯一屏障是「只有 Go 进程能写 pi stdin,用户文本一律经 `EncodeUserMessage` 变成一条 `prompt` 的字符串字段」 | `json.Marshal` 按构造即可挡住结构逃逸与行逃逸(pi 的 rpc 按行分隔)。新增 `TestPiEncodeUserMessage_CannotInjectRpcCommands` 把两条逃逸路径都钉住(变异验证:改成手工拼接 → 三个子用例全红)。**残留:若将来有改动让用户内容流向裸 rpc 命令构造,该屏障即失效** —— 那条测试是唯一护栏,不得为了"少一次转义"而放松 |

## 开放项(承规格,本计划不做)

1. pi-web 的会话常驻模型(`sessiond` + `web` 拆分)——与现有 30s 清理策略冲突,暂不采用
2. SSE 重连恢复简化(用 `get_messages` 取代 `streamingContent` 累积 + `sseReconnectContent` 去重)——留作 pi 路径稳定后的独立简化项
3. `backend/dependencies` 的孤儿状态(前端至今未消费 `/api/dependencies/status`)——仅复用其探测手法,不扩大范围
4. `config.Load()` 不可测:它内部 `flag.String` + `flag.Parse()`,二次调用即 panic,导致 `PiBin`/`ClaudeBin` 的 env 解析无法单测。应拆成「纯函数解析(可注入 env/args)」+「薄壳 `Load()`」。**本计划不做**(属既有结构问题,与后端切换无关)
