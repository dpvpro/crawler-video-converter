# Makefile для Video Converter

BINARY_NAME=crawler-video-converter
GO=go
GOFLAGS=-ldflags="-s -w"
INSTALL_PATH=/usr/local/bin

.PHONY: all build clean run install uninstall test help

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) -o $(BINARY_NAME) .
	@echo "Build complete!"

build-all: build-linux build-windows build-darwin

build-linux:
	@echo "Building for Linux..."
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-linux-arm64 .

build-windows:
	@echo "Building for Windows..."
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-windows-amd64.exe .

build-darwin:
	@echo "Building for macOS..."
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-darwin-arm64 .

run: build
	@if [ -z "$(PATH_ARG)" ]; then \
		echo "Usage: make run PATH_ARG=/path/to/videos"; \
		./$(BINARY_NAME); \
	else \
		./$(BINARY_NAME) $(PATH_ARG); \
	fi

install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	@sudo cp $(BINARY_NAME) $(INSTALL_PATH)/
	@sudo chmod +x $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Installation complete!"
	@echo "You can now run '$(BINARY_NAME)' from anywhere"

# Удаление из системы
uninstall:
	@echo "Uninstalling $(BINARY_NAME) from $(INSTALL_PATH)..."
	@sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Uninstallation complete!"

# Очистка собранных файлов
clean:
	@echo "Cleaning up..."
	@rm -f $(BINARY_NAME)
	@rm -f $(BINARY_NAME)-*
	@echo "Clean complete!"

# Форматирование кода
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Проверка кода
vet:
	@echo "Vetting code..."
	$(GO) vet ./...

# Загрузка зависимостей
deps:
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod tidy

# Тестирование
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Проверка наличия ffmpeg
check-ffmpeg:
	@echo "Checking FFmpeg installation..."
	@if command -v ffmpeg >/dev/null 2>&1; then \
		echo "✓ FFmpeg is installed"; \
		ffmpeg -version | head -n1; \
	else \
		echo "✗ FFmpeg is not installed"; \
		echo "Please install FFmpeg first:"; \
		echo "  Ubuntu/Debian: sudo apt install ffmpeg"; \
		echo "  macOS: brew install ffmpeg"; \
		echo "  Windows: Download from https://ffmpeg.org/download.html"; \
		exit 1; \
	fi

# Полная проверка перед использованием
check: check-ffmpeg vet fmt
	@echo "All checks passed!"

# Помощь
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  build-all     - Build for all platforms (Linux, Windows, macOS)"
	@echo "  run           - Build and run (use PATH_ARG=/path/to/videos)"
	@echo "  install       - Install to system (/usr/local/bin)"
	@echo "  uninstall     - Remove from system"
	@echo "  clean         - Remove built binaries"
	@echo "  fmt           - Format Go code"
	@echo "  vet           - Vet Go code"
	@echo "  deps          - Download and tidy dependencies"
	@echo "  test          - Run tests"
	@echo "  check-ffmpeg  - Check if FFmpeg is installed"
	@echo "  check         - Run all checks"
	@echo "  help          - Show this help message"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make run PATH_ARG=/home/user/videos"
	@echo "  make install"
	@echo "  make check && make build"
