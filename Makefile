.PHONY: build run dev clean

# Build the single binary with embedded frontend
build:
	cd frontend && npm run build
	cd backend && CGO_ENABLED=1 go build -o ../llm-knowledge .
	@mkdir -p scripts
	@cp backend/scripts/path-validator.py scripts/ && chmod +x scripts/path-validator.py
	@# pi-path-validator.ts 必须一起复制:运行时 ScriptsDir 就是这个 scripts/,
	@# 少了它 pi 后端会被 fail-closed 拦下(切换时 400、spawn 时拒绝)。
	@# 总是覆盖而不是缺则复制:陈旧的校验脚本会按旧规则静默放行(fail-open),
	@# 那比文件缺失更危险。
	@cp backend/scripts/pi-path-validator.ts scripts/

# Build and run the binary
run: build
	./llm-knowledge

# Development mode: run backend and frontend separately with hot reload
dev:
	cd backend && go run . &
	cd frontend && npm run dev

# Clean build artifacts
clean:
	rm -f llm-knowledge
	rm -rf backend/fs/dist