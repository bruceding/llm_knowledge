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
./llm-knowledge -port "$PORT" > logs/llm-knowledge.log 2>&1 & 