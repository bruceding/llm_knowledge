#!/bin/bash

# LLM Knowledge 启动脚本

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

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
            brew install poppler
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

# 设置端口（默认 9999）
PORT=${PORT:-9999}

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

# 确保 PATH 包含常用路径（claude 在 /usr/local/bin）
export PATH="/usr/local/bin:/usr/local/go/bin:$PATH"

./llm-knowledge -port "$PORT" > logs/llm-knowledge.log 2>&1 & 