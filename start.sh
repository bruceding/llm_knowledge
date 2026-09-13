#!/bin/bash

# LLM Knowledge 启动脚本

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# 先把常用路径补进 PATH,**必须在所有 command -v 检查之前**。
#
# /opt/homebrew/bin 是 Apple Silicon 上 npm/brew 的全局 bin:实测本机 `pi` 就在
# /opt/homebrew/bin/pi,而原先这里只补了 /usr/local/bin(Intel mac 的位置)。之所以
# 一直没暴露,是因为脚本继承了交互 shell 的 PATH;换成 systemd/launchd 那种最小
# PATH 的环境,pi 与 brew 装的 pdftotext 都会"找不到",进而触发下面不必要的重装。
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:$PATH"

# 检查 Go 是否安装
if ! command -v go &> /dev/null; then
    echo "错误: Go 未安装，请先安装 Go"
    exit 1
fi

# 检查 Node/npm 是否安装（用于前端）
if ! command -v npm &> /dev/null; then
    echo "错误: npm 未安装，请先安装 Node.js"
    exit 1
fi

# 检查 pdftotext 是否安装（PDF 文本提取工具）
if ! command -v pdftotext &> /dev/null; then
    echo "pdftotext 未安装，正在安装..."
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        if command -v brew &> /dev/null; then
            # 不加兜底会让一次失败的 brew install 在 `set -e` 下直接终止启动脚本,
            # 而 pdftotext 只影响 PDF 文本提取这一条路径。下面的 qpdf 早就是这么
            # 处理的,这里原先漏了。
            brew install poppler || echo "警告: poppler 安装失败，PDF 文本提取功能可能受限"
        else
            echo "错误: Homebrew 未安装，请先安装 Homebrew 或手动安装 poppler"
            exit 1
        fi
    elif [[ "$OSTYPE" == "linux"* ]]; then
        # Linux
        if command -v apt-get &> /dev/null; then
            sudo apt-get update && sudo apt-get install -y poppler-utils
        elif command -v yum &> /dev/null; then
            sudo yum install -y poppler-utils
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y poppler-utils
        else
            echo "错误: 无法识别的包管理器，请手动安装 poppler-utils"
            exit 1
        fi
    else
        echo "错误: 无法识别的系统类型，请手动安装 poppler-utils"
        exit 1
    fi
fi

# 检查 Python 3.12 是否安装（pdf2zh 需要 PEP 695 语法支持）
PYTHON312=""
if command -v python3.12 &> /dev/null; then
    PYTHON312="python3.12"
elif [[ -x "/usr/local/opt/python@3.12/bin/python3.12" ]]; then
    PYTHON312="/usr/local/opt/python@3.12/bin/python3.12"
elif [[ -x "/opt/homebrew/opt/python@3.12/bin/python3.12" ]]; then
    PYTHON312="/opt/homebrew/opt/python@3.12/bin/python3.12"
elif [[ -x "/usr/bin/python3.12" ]]; then
    PYTHON312="/usr/bin/python3.12"
fi

if [[ -z "$PYTHON312" ]]; then
    echo "警告: pdf2zh 需要 Python 3.12 (PEP 695 类型参数语法)"
    echo "PDF 翻译功能将不可用。安装方式:"
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "  brew install python@3.12"
    elif [[ "$OSTYPE" == "linux"* ]]; then
        echo "  Ubuntu: sudo add-apt-repository ppa:deadsnakes/ppa && sudo apt install python3.12"
        echo "  Fedora: sudo dnf install python3.12"
        echo "  或使用 pyenv: pyenv install 3.12"
    fi
    # 不退出，pdf2zh 可以后续手动安装，其他功能正常使用
fi

# 检查 qpdf 是否安装（pdf2zh 的 pikepdf 依赖）
if ! command -v qpdf &> /dev/null; then
    echo "qpdf 未安装，正在安装..."
    if [[ "$OSTYPE" == "darwin"* ]]; then
        if command -v brew &> /dev/null; then
            brew install qpdf || echo "警告: qpdf 安装失败，PDF 翻译功能可能受限"
        else
            echo "警告: Homebrew 未安装，请手动安装 qpdf"
        fi
    elif [[ "$OSTYPE" == "linux"* ]]; then
        if command -v apt-get &> /dev/null; then
            sudo apt-get update && sudo apt-get install -y qpdf || echo "警告: qpdf 安装失败"
        elif command -v yum &> /dev/null; then
            sudo yum install -y qpdf || echo "警告: qpdf 安装失败"
        elif command -v dnf &> /dev/null; then
            sudo dnf install -y qpdf || echo "警告: qpdf 安装失败"
        else
            echo "警告: 无法识别的包管理器，请手动安装 qpdf"
        fi
    else
        echo "警告: 无法识别的系统类型，请手动安装 qpdf"
    fi
fi

# 检查 pi 后端是否可用。**可选**:claude 是默认后端,pi 只在 Settings 里切过去时才需要,
# 所以这里只告警不退出 —— 否则没装 pi 的部署连启动都启动不了。
# 真正的 fail-closed 在服务端:切到 pi 时 PUT /api/admin/settings 会探测并返回 400,
# 缺文件时 PiProtocol 也会拒绝 spawn(不会静默退化成无沙箱执行)。
if command -v pi &> /dev/null; then
    echo "pi 后端: $(command -v pi) ($(pi --version 2>/dev/null || echo '版本未知'))"

    # pi-web-access 提供联网检索工具。缺它不会让 pi 启动失败,但 web_search 等工具不可用。
    # 路径必须与 Go 侧同源:$PI_CODING_AGENT_DIR,未设时为 <服务账号 HOME>/.pi/agent。
    PI_AGENT_DIR="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}"
    if [ -d "$PI_AGENT_DIR/npm/node_modules/pi-web-access" ]; then
        echo "  pi-web-access: 已安装"
    else
        echo "  警告: 未在 $PI_AGENT_DIR/npm/node_modules/ 下找到 pi-web-access"
        echo "        pi 后端的联网检索工具将不可用(其余功能不受影响)"
    fi

    # web-search.json 的 toolNames 写错会让**整个 pi 后端 exit 1**(不只是联网不可用,
    # 连不用 web 工具的 ingest 链路一起死)。Go 侧启动时会做同一份校验并告警;
    # 这里只提示文件位置,避免把同一套 JSON 解析逻辑在 shell 里再实现一遍。
    if [ -f "$PI_AGENT_DIR/web-search.json" ]; then
        echo "  web-search.json: $PI_AGENT_DIR/web-search.json"
        echo "        注意:toolNames 写错会让 pi 整体拒绝启动;commands.* 不显式关掉"
        echo "        会让任何用户发一条以 / 开头的消息就执行扩展代码(详见 README)"
    else
        echo "  web-search.json: 未找到($PI_AGENT_DIR/web-search.json),将使用默认值"
        echo "        默认值下 commands.* 全部为启用状态,部署前务必按 README 固化该文件"
    fi
else
    echo "提示: 未检测到 pi,Settings 里只能使用 claude 后端(不影响启动)"
    echo "      安装: npm install -g @earendil-works/pi-coding-agent"
fi

# 检查前端依赖是否安装
if [ ! -d "frontend/node_modules" ]; then
    echo "安装前端依赖..."
    cd frontend && npm install && cd ..
fi

# 检查后端依赖是否安装
if [ ! -f "backend/go.sum" ]; then
    echo "安装后端依赖..."
    cd backend && go mod download && cd ..
fi

# 每次都重新构建后端
echo "构建服务..."
make build

# 设置端口（默认 9090）
PORT=${PORT:-9090}

# 终止旧进程
echo "检查并终止旧进程..."
OLD_PID=$(pgrep -f "llm-knowledge.*-port.*$PORT" 2>/dev/null || true)
if [ -n "$OLD_PID" ]; then
    echo "发现旧进程 (PID: $OLD_PID)，正在终止..."
    kill "$OLD_PID" 2>/dev/null || true
    sleep 1
fi

# 确保日志目录存在
mkdir -p logs

echo "启动 LLM Knowledge 服务 (端口: $PORT)..."

# 确保 PATH 包含常用路径(已在脚本开头导出,这里不再重复设置)

# Set scripts directory for security hooks
export LLM_SCRIPTS_DIR="${SCRIPT_DIR}/scripts"

# 把两个沙箱校验脚本复制到运行时目录。**总是覆盖**而不是"缺则复制":
# 一份来自旧版本的 pi-path-validator.ts 会静默地按旧规则放行,那比文件缺失更危险
# (缺失是 fail-closed,会拒绝 spawn;陈旧是 fail-open,看不出来)。
# pi-path-validator.ts 尤其必须在这里复制:make build 只复制了 path-validator.py,
# 少了它 pi 后端会在切换探测与每次 spawn 时被 fail-closed 拦下。
mkdir -p "$LLM_SCRIPTS_DIR"
cp backend/scripts/path-validator.py "$LLM_SCRIPTS_DIR/" && chmod +x "$LLM_SCRIPTS_DIR/path-validator.py"
cp backend/scripts/pi-path-validator.ts "$LLM_SCRIPTS_DIR/"
echo "沙箱脚本已部署到 $LLM_SCRIPTS_DIR"

./llm-knowledge -port "$PORT" > logs/llm-knowledge.log 2>&1 & 