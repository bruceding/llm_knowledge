/**
 * pi 沙箱 extension —— `path-validator.py`(Claude CLI 的 PreToolUse hook)在 pi 侧的对应物。
 *
 * 部署位置:tracked 源在本目录(`backend/scripts/`),部署时复制到运行时 `scripts/`
 * (即 `start.sh:137` 导出的 `LLM_SCRIPTS_DIR`),再由 `PiProtocol` 以 `-e <该路径>` 加载。
 * 仓库根的 `scripts/` 被 `.gitignore:6` 整体忽略,放那里等于新克隆里没有沙箱(fail-open)。
 *
 * 与 Python 版的三点差异:
 *
 *  1. **机制**:Python 是 stdin 驱动的独立进程(exit 2 = deny);这里是 pi 进程内的
 *     `tool_call` hook,返回 `{ block: true, reason }` 拦截。
 *  2. **白名单而非黑名单**:pi 内置工具只有 read/bash/powershell/edit/write/grep/find/ls,
 *     所以「白名单外一律 block」比 Python 的 `ALWAYS_DENIED_TOOLS` 黑名单更强。黑名单在
 *     这里退化为兜底 —— 防 `PI_WEB_TOOLS` 被误配成含 `bash` 时白名单反而放行它。
 *  3. **多一项职责**:校验 URL 类入参,堵 `fetch_content` 的本地文件向量 ——
 *     `pi-web-access/video-extract.ts:337` 的 `readFile(info.absolutePath)`(读到的内容
 *     紧接着在 `:338-341` 被 PUT 上传到 Gemini,即任意绝对路径读取 + 外泄),以及
 *     `:211-214` 的 `execFileSync("ffmpeg", [..., "-i", filePath, ...])`(任意路径 spawn)。
 *     `--tools` 白名单拦不住它:白名单只决定工具能不能被调,不校验参数。
 *
 * **不做** IP/DNS 级 SSRF 校验:那是 `pi-web-access/ssrf-protection.ts` 的职责,规格已论证
 * 它比 Python 版更严(重定向目标永不继承 allowLoopback、每次 fetch 前重校验 DNS rebinding、
 * 有域名级 allow/deny)。这里只管「是不是一个远端 http(s) URL」,分工不重叠。
 *
 * 本文件末尾还有一个 CLI 入口(**仅当被直接执行时**生效),供
 * `backend/claude/security_test.go` 用与 Python 版完全相同的手法驱动:stdin 喂
 * `{"toolName":"read","input":{"path":"..."}}`,deny 则 exit 2 并把决定写 stderr。
 * pi 以模块方式 import 本文件时该分支不执行 —— `process.argv[1]` 是 pi 自己的入口,
 * 不可能等于本文件的路径。
 */

import * as fs from "node:fs";
import * as path from "node:path";
import { pathToFileURL } from "node:url";

/*
 * pi 宿主 API 的最小结构化声明。
 *
 * 不用 `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent"`:该包安装在
 * `/opt/homebrew/lib/node_modules`,不在本仓库的 node_modules 解析链上,`tsc --noEmit` 会报
 * TS2307;而加 tsconfig paths 或装依赖都会污染前端工程。这里只声明本文件真正用到的一个
 * 事件,形状逐字对照 pi 0.85.1 的权威定义:
 *
 *   - tool_call 事件:`dist/core/extensions/types.d.ts:678-724`
 *     `ToolCallEventBase{type:"tool_call"; toolCallId}` + `toolName` + `input`。六个内置
 *     文件工具的 input 各自有精确类型(`core/tools/{read,grep,find,ls,write,edit}.d.ts` 的
 *     schema),但**路径字段都叫 `path`**;自定义工具走 `CustomToolCallEvent`,其 input 是
 *     `Record<string, unknown>`(`types.d.ts:717-720`),`fetch_content` 属这一支。
 *     故这里统一按 `Record<string, unknown>` 处理,不做逐工具类型分派。
 *   - 返回值:`dist/core/extensions/types.d.ts:818-828` 的
 *     `ToolCallEventResult{block?, reason?, terminate?}`。返回 `undefined` = 放行。
 *   - `ctx.cwd`:`dist/core/extensions/types.d.ts:217`。
 *
 * 运行时类型全部被擦除,pi 只是调用本文件的 default export,因此该声明不影响行为;
 * 若 pi 升级改了形状,失效方式是 TS 类型检查报错而不是静默放行。
 */
type ToolCallEventView = {
  readonly type: "tool_call";
  readonly toolCallId: string;
  readonly toolName: string;
  readonly input: Record<string, unknown>;
};

type ToolCallResultView = { block?: boolean; reason?: string; terminate?: boolean };

type ExtensionContextView = { readonly cwd: string };

type PiExtensionAPI = {
  on(
    event: "tool_call",
    handler: (event: ToolCallEventView, ctx: ExtensionContextView) => ToolCallResultView | undefined,
  ): void;
};

/**
 * pi 的内置文件类工具。**这六个名字是 pi 内置的、不可配置**,因此可以硬编码
 * (与 web 工具名不同 —— 后者可被 `web-search.json` 的 `toolNames` 改写,只能从
 * `PI_WEB_TOOLS` 读)。依据:`core/tools/index.d.ts` 的工具清单。
 *
 * 六个工具的入参 schema 里路径字段**都叫 `path`**:`read`/`write`/`edit` 必填,
 * `grep`/`find`/`ls` 可选(缺省即 cwd)。
 */
export const FILE_TOOLS: readonly string[] = ["read", "grep", "find", "ls", "write", "edit"];

/**
 * 无条件拒绝的工具。pi 内置工具中具备任意执行能力的是 `bash` 与 `powershell`;
 * 白名单机制已使它们不可能被调用,这里是兜底(防 `PI_WEB_TOOLS` 误配)。
 *
 * 该集合与 `DENIED_TOOL_MAPPING`/`PI_ONLY_DENIED_TOOLS` 一起,由
 * `backend/claude/security_test.go` 的 `TestDangerousToolsCrossLanguageSync` 与 Go 的
 * `agent.ClaudeDangerousDisallowedTools`、Python 的 `ALWAYS_DENIED_TOOLS` 做三语言对齐。
 */
export const ALWAYS_DENIED_TOOLS: readonly string[] = ["bash", "powershell"];

/**
 * Claude 工具名 → pi 工具名的对应关系(仅列**有**对应物者)。
 * Claude 的 `Bash` 对应 pi 的 `bash`;其余五个在 pi 无对应物,见下一个常量。
 */
export const DENIED_TOOL_MAPPING: Readonly<Record<string, string>> = { Bash: "bash" };

/** pi 独有、Claude 侧没有对应物的危险工具。 */
export const PI_ONLY_DENIED_TOOLS: readonly string[] = ["powershell"];

/**
 * Go/Python 的危险工具里,在 pi **没有对应物**的那些。pi 内置工具仅
 * read/bash/powershell/edit/write/grep/find/ls,不存在 Task/NotebookEdit/KillShell/
 * BashOutput/SlashCommand 的等价物(规格「pi 进程配方」已核实)。
 *
 * 显式列出来是为了让同步测试能区分「有对应物且已覆盖」与「无对应物」:将来 Go 侧新增一个
 * 危险工具时,必须在这里或 `DENIED_TOOL_MAPPING` 里表态,否则测试失败 —— 逼出一次有意识的
 * 决定,而不是静默漏掉。
 */
export const DENIED_TOOLS_WITHOUT_PI_COUNTERPART: readonly string[] = [
  "Task",
  "NotebookEdit",
  "KillShell",
  "BashOutput",
  "SlashCommand",
];

/**
 * 敏感路径正则表 —— 与 `path-validator.py` 的 `SENSITIVE_PATH_PATTERNS` **逐条同步**,
 * 由 `TestDangerousToolsCrossLanguageSync` 守护(比对源码文本,任一漂移即失败)。
 *
 * 用 `String.raw` 而不是普通字符串:表里有大量 `\.`,而 JS 普通字符串会把未知转义的反斜杠
 * 吃掉(`'\.' === '.'`),导致正则与 Python 版语义不再一致。`String.raw` 让反引号内的文本
 * 与 Python 的 `r'...'` **逐字节相同**,同步测试因此可以直接比源码文本、无需任何反转义。
 *
 * 这些是 defense-in-depth:即使路径落在 ALLOWED_DIR 内也照样拦。
 */
export const SENSITIVE_PATH_PATTERNS: readonly string[] = [
	// System configuration and secrets (Linux)
	String.raw`^/etc/shadow$`,
	String.raw`^/etc/passwd$`,
	String.raw`^/etc/gshadow$`,
	String.raw`^/etc/ssh/`,
	String.raw`^/etc/ssl/(private|certs)`,
	String.raw`^/root/`,
	String.raw`^/proc/`,
	String.raw`^/sys/`,
	String.raw`^/var/log/`,
	String.raw`^/var/run/`,
	// macOS symlink targets (/etc -> /private/etc, /var -> /private/var)
	String.raw`^/private/etc/shadow$`,
	String.raw`^/private/etc/passwd$`,
	String.raw`^/private/etc/gshadow$`,
	String.raw`^/private/etc/ssh/`,
	String.raw`^/private/etc/ssl/(private|certs)`,
	String.raw`^/private/var/log/`,
	String.raw`^/private/var/run/`,
	// User credentials - Linux
	String.raw`^/home/.*/\.ssh/`,
	String.raw`^/home/.*/\.aws/`,
	String.raw`^/home/.*/\.config/gcloud/`,
	String.raw`^/home/.*/\.kube/`,
	String.raw`^/home/.*/\.docker/`,
	String.raw`^/home/.*/\.gnupg/`,
	String.raw`^/home/.*/\.netrc$`,
	String.raw`^/home/.*/\.pgpass$`,
	String.raw`^/home/.*/\.bash_history$`,
	String.raw`^/home/.*/\.zsh_history$`,
	String.raw`^/home/.*/\.python_history$`,
	String.raw`^/home/.*/\.config/git/credentials$`,
	String.raw`^/home/.*/\.git-credentials$`,
	// User credentials - macOS
	String.raw`^/Users/.*/\.ssh/`,
	String.raw`^/Users/.*/\.aws/`,
	String.raw`^/Users/.*/\.config/gcloud/`,
	String.raw`^/Users/.*/\.kube/`,
	String.raw`^/Users/.*/\.docker/`,
	String.raw`^/Users/.*/\.gnupg/`,
	String.raw`^/Users/.*/\.netrc$`,
	String.raw`^/Users/.*/\.pgpass$`,
	String.raw`^/Users/.*/\.bash_history$`,
	String.raw`^/Users/.*/\.zsh_history$`,
	String.raw`^/Users/.*/\.python_history$`,
	String.raw`^/Users/.*/\.config/git/credentials$`,
	String.raw`^/Users/.*/\.git-credentials$`,
	String.raw`^/Users/.*/Library/Keychains/`,
];

const COMPILED_SENSITIVE_PATTERNS: readonly RegExp[] = SENSITIVE_PATH_PATTERNS.map(
  (pattern) => new RegExp(pattern),
);

/** 允许的 URL scheme。其余一切(`file:`/`data:`/`gopher:`/`ftp:`/`javascript:` 等)一律拒绝。 */
const ALLOWED_URL_SCHEMES: readonly string[] = ["http:", "https:"];

/**
 * `scheme://` 之后 authority 为空(如 `http:///path`)。
 *
 * 必须显式处理:Python 的 `urlparse("http:///path").hostname` 是 `None` 因而拒绝,而
 * WHATWG `URL` 会把它解析成 `hostname="path"`(实测 Node v24),不拦就与 Python 行为分歧。
 * 该正则不误伤 `https://a.example//x`(有 host、只是路径以 `//` 开头)。
 */
const EMPTY_AUTHORITY_RE = /^[A-Za-z][A-Za-z0-9+.-]*:\/\/\//;

/**
 * 需要校验的 URL 类入参键名。
 *
 * **按键名判断,不按工具名判断**:`fetch_content` 与 `get_search_content` 的名字都可被
 * `web-search.json` 的 `toolNames` 改写(`pi-web-access/index.ts:234-239`),按名字硬编码
 * 会在运维改名后静默失效。两个键名本身是 schema 固定的:`fetch_content` 的
 * `url`/`urls`(`index.ts:2492-2494`)、`get_search_content` 的 `url`(`:2840`)。
 *
 * 顺带说明 `get_search_content`:它的 `execute` 是从已存缓存取(`getResult(responseId)`,
 * `index.ts:2851-2864`),`url` 只是选择键、不发起抓取,因此**不构成**第二条文件向量;
 * 但这是第三方包的实现细节、随时可能变,统一校验的成本近乎零,故一并覆盖。
 */
const URL_INPUT_KEYS: readonly string[] = ["url", "urls"];

/** `validateToolCall` 的输入。全部来自调用方,函数本身不读 env(便于测试)。 */
export interface ValidateOptions {
  /** `ALLOWED_DIR`,由 Go 侧 `PiProtocol.Env()` 注入(已做 realpath 解析)。空串表示未配置。 */
  readonly allowedDir: string;
  /** `PI_WEB_TOOLS` 解析出的 web 工具名,与 `--tools` 白名单同源。 */
  readonly webTools: readonly string[];
  /**
   * 解析相对路径的基准目录。生产环境传 hook 的 `ctx.cwd` —— pi 自己就是用
   * `resolveToCwd(searchDir || ".", ctx?.cwd || cwd)` 解析的(`find.js:65`、`grep.js:57`),
   * 用别的基准会校验到与实际访问不同的路径。
   */
  readonly cwd: string;
}

/** 拦截决定。返回 `undefined` 表示放行(与 pi 的 `ToolCallEventResult` 兼容)。 */
export interface BlockDecision {
  readonly block: true;
  readonly reason: string;
}

/** 解析 `PI_WEB_TOOLS`(逗号分隔)。未设置/空串 → 空数组,即不额外放行任何 web 工具。 */
export function parseWebTools(raw: string | undefined): string[] {
  if (raw === undefined || raw.trim() === "") return [];
  return raw
    .split(",")
    .map((entry) => entry.trim())
    .filter((entry) => entry !== "");
}

/**
 * 尽力解析真实路径。
 *
 * Python 的 `os.path.realpath` 对不存在的路径不报错(解析已存在的前缀,其余原样保留),
 * 而 Node 的 `fs.realpathSync` 会抛 ENOENT。`write` 新建文件时路径必然不存在,若让异常
 * 冒泡,按 `docs/extensions.md:2925`「`tool_call` errors block the tool (fail-safe)」
 * 会把**所有新建文件的写操作**一并拦掉 —— 那是功能性破坏,不是安全增强。
 * 故逐级回退到最近的存在的祖先,对它做 realpath 再拼回剩余部分。
 */
export function realpathBestEffort(target: string): string {
  const normalized = path.normalize(target);
  let current = normalized;
  const tail: string[] = [];
  for (;;) {
    try {
      const real = fs.realpathSync(current);
      return tail.length === 0 ? real : path.join(real, ...tail);
    } catch {
      const parent = path.dirname(current);
      if (parent === current) return normalized; // 已到根仍不存在
      tail.unshift(path.basename(current));
      current = parent;
    }
  }
}

/** 是否命中敏感路径正则表。与 Python 版一致:先 normpath 再匹配。 */
export function isSensitivePath(target: string): boolean {
  const normalized = path.normalize(target);
  return COMPILED_SENSITIVE_PATTERNS.some((pattern) => pattern.test(normalized));
}

/**
 * 目录边界检查。用 `path.sep` 做边界,防前缀碰撞
 * (否则 `/data/users/1` 会匹配 `/data/users/10`)。与 Python 的 `is_path_within_dir` 等价。
 */
export function isPathWithinDir(resolved: string, allowedDir: string): boolean {
  if (allowedDir === "") return false;
  return resolved === allowedDir || resolved.startsWith(allowedDir + path.sep);
}

/**
 * 把工具入参里的路径解析成绝对真实路径。
 *
 * 相对路径以 `cwd` 为基准 —— 这是 pi 的实际行为(`resolveToCwd`)。Python 版是拼到
 * `ALLOWED_DIR` 上;两者在 `cwd == ALLOWED_DIR`(生产环境的常态:Go 侧 `cmd.Dir = userDir`
 * 且 `ALLOWED_DIR = realpath(userDir)`)时等价,而一旦不一致,以 cwd 为准才不会放行
 * pi 实际会去访问的目录之外的路径。此时边界检查会拒绝,fail-closed。
 */
export function resolveToolPath(rawPath: string, cwd: string): string {
  const absolute = path.isAbsolute(rawPath) ? rawPath : path.resolve(cwd, rawPath);
  return realpathBestEffort(absolute);
}

/**
 * 校验单个 URL。返回拒绝理由,`undefined` 表示通过。
 *
 * 与 Python 的 `validate_webfetch_url` 对齐的部分:空值拒绝、scheme 限 http/https、
 * 缺 host 拒绝。**不对齐且有意为之**的部分:不做字面 IP 与 DNS 解析检查 ——
 * 那是 `pi-web-access/ssrf-protection.ts` 的职责(规格已论证它更严),重复实现只会互相遮蔽。
 */
export function validateUrl(value: unknown): string | undefined {
  if (typeof value !== "string" || value.trim() === "") {
    return "Access denied: empty URL";
  }
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    // 相对路径与本地路径(`/etc/passwd`、`../../x.mp4`)在这里就抛错 —— 正是我们要拒的
    return `Access denied: '${value}' is not an absolute http(s) URL`;
  }
  if (!ALLOWED_URL_SCHEMES.includes(parsed.protocol)) {
    return `Access denied: URL scheme '${parsed.protocol}' not allowed`;
  }
  if (EMPTY_AUTHORITY_RE.test(value)) {
    return "Access denied: URL missing host";
  }
  return undefined;
}

/**
 * 校验一次工具调用。返回 `BlockDecision` 表示拦截,`undefined` 表示放行。
 *
 * 纯函数:所有外部输入都经 `opts` 传入,不读 env、不做 IO(除路径解析必需的 realpath),
 * 因此既能被 hook 调用,也能被 CLI 入口调用供 Go 测试驱动。
 */
export function validateToolCall(
  toolName: string,
  input: Record<string, unknown>,
  opts: ValidateOptions,
): BlockDecision | undefined {
  const deny = (reason: string): BlockDecision => ({ block: true, reason });

  // 0) fail-closed:ALLOWED_DIR 未配置 → 拒绝一切。
  //    与 Python 版一致:`main()` 在分派到具体工具之前就 deny,因此联网工具也一并被拒。
  //    这比「只拒文件工具」更强,且 ALLOWED_DIR 未配置本身说明 Go 侧已经出问题了。
  if (opts.allowedDir === "") {
    return deny("Access denied: ALLOWED_DIR not configured — access denied by default");
  }

  // 1) 危险工具兜底,**优先于**白名单:即使 PI_WEB_TOOLS 被误配成含 bash 也拦住。
  if (ALWAYS_DENIED_TOOLS.includes(toolName)) {
    return deny(`Access denied: tool '${toolName}' is not permitted`);
  }

  // 2) 白名单 = 文件工具 ∪ PI_WEB_TOOLS。其余一律拒(等价 Python 的 ALWAYS_DENIED_TOOLS
  //    兜底,但对 pi 更强:pi 的工具集是有限已知的)。
  if (!FILE_TOOLS.includes(toolName) && !opts.webTools.includes(toolName)) {
    return deny(`Access denied: tool '${toolName}' is not permitted`);
  }

  // 3) URL 类入参。对任何被放行的工具生效,理由见 URL_INPUT_KEYS 的注释。
  for (const key of URL_INPUT_KEYS) {
    if (!Object.prototype.hasOwnProperty.call(input, key)) continue;
    const value = input[key];
    if (key === "urls" || Array.isArray(value)) {
      // `urls` 缺失不算错(工具自己会报参数不足),但**显式给了空数组**要拒 ——
      // 与 Python 对空 url 的处理一致(空即拒),不给「空列表 = 跳过校验」的口子。
      if (!Array.isArray(value) || value.length === 0) {
        return deny("Access denied: empty URL list");
      }
      for (const entry of value) {
        const reason = validateUrl(entry);
        if (reason !== undefined) return deny(reason); // 任一元素非法即整体拦截
      }
      continue;
    }
    const reason = validateUrl(value);
    if (reason !== undefined) return deny(reason);
  }

  // 4) 文件工具的路径校验。
  if (FILE_TOOLS.includes(toolName)) {
    // grep/find/ls 的 path 可选,缺省语义是 cwd(pi 用 `searchDir || "."`),故以 "." 复现。
    // read/write/edit 的 path 必填;真缺了就用 "." 走同一套校验,而不是放行。
    const raw = typeof input.path === "string" ? input.path : "";
    const resolved = resolveToolPath(raw === "" ? "." : raw, opts.cwd);
    const allowedReal = realpathBestEffort(opts.allowedDir);

    // 与 Python 的 validate_path 同序:先敏感路径(defense-in-depth,即使在 ALLOWED_DIR 内
    // 也拦),再目录边界。
    if (isSensitivePath(resolved)) {
      return deny("Access denied: sensitive file");
    }
    if (!isPathWithinDir(resolved, allowedReal)) {
      return deny("Access denied: path outside allowed directory");
    }
    // pattern/glob 不需要校验:它们只作为 searchPath **内部**的匹配模式传给 fd/rg
    // (find.js:78 的 `ops.glob(pattern, searchPath, ...)`、grep.js:100 的
    // `args.push("--", pattern, searchPath)`),文件系统根始终来自上面已校验的 path。
  }

  return undefined;
}

/** 从进程环境组装校验参数。`cwdFallback` 用于拿不到 `ctx.cwd` 的场合(CLI 模式)。 */
export function optionsFromEnv(cwdFallback: string): ValidateOptions {
  return {
    allowedDir: process.env.ALLOWED_DIR ?? "",
    webTools: parseWebTools(process.env.PI_WEB_TOOLS),
    cwd: cwdFallback,
  };
}

/** pi extension 入口。 */
export default function (pi: PiExtensionAPI): void {
  // 同步 handler:校验只涉及常数次 realpath,没有 IO 等待;返回 Promise 反而多一层
  // 失败模式(unhandled rejection)。抛错时 pi 会 block 该工具(fail-safe,
  // docs/extensions.md:2925),方向正确。
  pi.on("tool_call", (event, ctx) =>
    validateToolCall(event.toolName, event.input, {
      allowedDir: process.env.ALLOWED_DIR ?? "",
      webTools: parseWebTools(process.env.PI_WEB_TOOLS),
      cwd: ctx?.cwd ?? process.cwd(),
    }),
  );
}

/*
 * CLI 入口 —— 仅供 `backend/claude/security_test.go` 驱动,手法与 Python 版一致:
 * stdin 喂 JSON,deny 则 stderr 写 `{"decision":"deny","reason":...}` 并 exit 2,放行 exit 0。
 *
 * 判定「被直接执行」用 argv[1] 与本模块 URL 比较:pi 加载扩展时 argv[1] 是 pi 自己的入口,
 * 不可能等于本文件,故 pi 侧永远不会走进这个分支。
 */
const invokedDirectly =
  process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;

if (invokedDirectly) {
  const denyAndExit = (reason: string): never => {
    process.stderr.write(JSON.stringify({ decision: "deny", reason }) + "\n");
    process.exit(2);
  };

  let raw = "";
  try {
    raw = fs.readFileSync(0, "utf8");
  } catch {
    denyAndExit("Failed to read input");
  }

  let payload: unknown;
  try {
    payload = JSON.parse(raw);
  } catch {
    denyAndExit("Invalid JSON input");
  }
  if (typeof payload !== "object" || payload === null) {
    denyAndExit("Invalid input format");
  }

  const record = payload as Record<string, unknown>;
  const toolName = typeof record.toolName === "string" ? record.toolName : "";
  const input =
    typeof record.input === "object" && record.input !== null
      ? (record.input as Record<string, unknown>)
      : {};

  const decision = validateToolCall(toolName, input, optionsFromEnv(process.cwd()));
  if (decision !== undefined) denyAndExit(decision.reason);
  process.exit(0);
}
