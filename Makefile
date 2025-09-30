# Makefile для Video Converter

# Переменные
BINARY_NAME=video-converter
GO=go
GOFLAGS=-ldflags="-s -w"
INSTALL_PATH=/usr/local/bin

# Цвета для вывода
GREEN=\033[0;32m
YELLOW=\033[1;33m
NC=\033[0m # No Color

.PHONY: all build clean run install uninstall test help

# Цель по умолчанию
all: build

# Сборка программы
build:
	@echo "$(GREEN)Building $(BINARY_NAME)...$(NC)"
	$(GO) build $(GOFLAGS) -o $(BINARY_NAME) .
	@echo "$(GREEN)Build complete!$(NC)"

# Сборка для разных платформ
build-all: build-linux build-windows build-darwin

build-linux:
	@echo "$(GREEN)Building for Linux...$(NC)"
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-linux-arm64 .

build-windows:
	@echo "$(GREEN)Building for Windows...$(NC)"
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-windows-amd64.exe .

build-darwin:
	@echo "$(GREEN)Building for macOS...$(NC)"
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -o $(BINARY_NAME)-darwin-arm64 .

# Запуск программы (для тестирования)
run: build
	@if [ -z "$(PATH_ARG)" ]; then \
		echo "$(YELLOW)Usage: make run PATH_ARG=/path/to/videos$(NC)"; \
		./$(BINARY_NAME); \
	else \
		./$(BINARY_NAME) $(PATH_ARG); \
	fi

# Установка в систему
install: build
	@echo "$(GREEN)Installing $(BINARY_NAME) to $(INSTALL_PATH)...$(NC)"
	@sudo cp $(BINARY_NAME) $(INSTALL_PATH)/
	@sudo chmod +x $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "$(GREEN)Installation complete!$(NC)"
	@echo "$(YELLOW)You can now run '$(BINARY_NAME)' from anywhere$(NC)"

# Удаление из системы
uninstall:
	@echo "$(GREEN)Uninstalling $(BINARY_NAME) from $(INSTALL_PATH)...$(NC)"
	@sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "$(GREEN)Uninstallation complete!$(NC)"

# Очистка собранных файлов
clean:
	@echo "$(GREEN)Cleaning up...$(NC)"
	@rm -f $(BINARY_NAME)
	@rm -f $(BINARY_NAME)-*
	@echo "$(GREEN)Clean complete!$(NC)"

# Форматирование кода
fmt:
	@echo "$(GREEN)Formatting code...$(NC)"
	$(GO) fmt ./...

# Проверка кода
vet:
	@echo "$(GREEN)Vetting code...$(NC)"
	$(GO) vet ./...

# Загрузка зависимостей
deps:
	@echo "$(GREEN)Downloading dependencies...$(NC)"
	$(GO) mod download
	$(GO) mod tidy

# Тестирование
test:
	@echo "$(GREEN)Running tests...$(NC)"
	$(GO) test -v ./...

# Проверка наличия ffmpeg
check-ffmpeg:
	@echo "$(GREEN)Checking FFmpeg installation...$(NC)"
	@if command -v ffmpeg >/dev/null 2>&1; then \
		echo "$(GREEN)✓ FFmpeg is installed$(NC)"; \
		ffmpeg -version | head -n1; \
	else \
		echo "$(YELLOW)✗ FFmpeg is not installed$(NC)"; \
		echo "$(YELLOW)Please install FFmpeg first:$(NC)"; \
		echo "  Ubuntu/Debian: sudo apt install ffmpeg"; \
		echo "  macOS: brew install ffmpeg"; \
		echo "  Windows: Download from https://ffmpeg.org/download.html"; \
		exit 1; \
	fi

# Полная проверка перед использованием
check: check-ffmpeg vet fmt
	@echo "$(GREEN)All checks passed!$(NC)"

# Помощь
help:
	@echo "$(GREEN)Available targets:$(NC)"
	@echo "  $(YELLOW)build$(NC)        - Build the binary"
	@echo "  $(YELLOW)build-all$(NC)    - Build for all platforms (Linux, Windows, macOS)"
	@echo "  $(YELLOW)run$(NC)          - Build and run (use PATH_ARG=/path/to/videos)"
	@echo "  $(YELLOW)install$(NC)      - Install to system (/usr/local/bin)"
	@echo "  $(YELLOW)uninstall$(NC)    - Remove from system"
	@echo "  $(YELLOW)clean$(NC)        - Remove built binaries"
	@echo "  $(YELLOW)fmt$(NC)          - Format Go code"
	@echo "  $(YELLOW)vet$(NC)          - Vet Go code"
	@echo "  $(YELLOW)deps$(NC)         - Download and tidy dependencies"
	@echo "  $(YELLOW)test$(NC)         - Run tests"
	@echo "  $(YELLOW)check-ffmpeg$(NC) - Check if FFmpeg is installed"
	@echo "  $(YELLOW)check$(NC)        - Run all checks"
	@echo "  $(YELLOW)help$(NC)         - Show this help message"
	@echo ""
	@echo "$(GREEN)Examples:$(NC)"
	@echo "  make build"
	@echo "  make run PATH_ARG=/home/user/videos"
	@echo "  make install"
	@echo "  make check && make build"
