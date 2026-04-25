.PHONY: build run dev clean

# Build the single binary with embedded frontend
build:
	cd frontend && npm run build
	cd backend && CGO_ENABLED=1 go build -o ../llm-knowledge .

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