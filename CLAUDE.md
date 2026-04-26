# CLAUDE.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.

## Document Chat 架构

基于 Claude CLI 的 stream-json 模式实现多轮对话。

### Claude CLI 启动参数

```go
args := []string{
    "--print",                          // 非交互模式
    "--output-format", "stream-json",   // 输出 JSON 流
    "--input-format", "stream-json",    // 输入 JSON 流（支持多轮）
    "--verbose",
    "--allowedTools", "Read",           // 只允许 Read 工具
    "--dangerously-skip-permissions",
    "--system-prompt", systemPrompt,    // 注入文档上下文
}
cmd.Dir = dataDir  // 工作目录设为数据目录，Read 工具可访问本地文件
```

### 双向流式交互

```
前端 EventSource → SSE /api/doc-chat/stream → SessionPool.StartSession
                              ↓
                    Claude 进程 stdin ← stdout (JSON 流)
                              ↓
POST /api/doc-chat/message → stdin 写入 user message
                              ↓
                    stdout → eventCh → SSE 推送给前端
```

### stdin 消息格式

```json
{"type":"user","message":{"role":"user","content":"用户问题"}}
```

### 关键注意事项

1. **必须先发送 init message**：启动 Claude 进程后，需立即向 stdin 发送一条初始消息，否则 stdout 不会 emit `init` 事件（无法获取 session_id）
2. **session_id 来自 system init 事件**：首条 stdout 输出是 `{type: "system", subtype: "init", session_id: "..."}`
3. **Session 超时清理**：SSE 断开 30 秒后自动关闭 session，避免资源泄漏
4. **stdout buffer**：设置 1MB buffer，防止大消息截断
