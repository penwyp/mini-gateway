# 项目基本信息
BINARY_NAME = mini-gateway
BIN_DIR = bin
CMD_DIR = cmd/gateway
MODULE = github.com/penwyp/mini-gateway
VERSION = 0.1.0
BUILD_TIME = $(shell date +%Y-%m-%dT%H:%M:%S%z)
GIT_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GO_VERSION = $(shell go version | awk '{print $$3}')

# 编译标志
LDFLAGS = -ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT) -X main.GoVersion=$(GO_VERSION)"

# 工具
GO = go
GOLINT = golangci-lint
DOCKER = docker
WRK = wrk

# 默认目标
.PHONY: all
all: build

# 安装依赖
.PHONY: deps
deps:
	$(GO) mod tidy
	$(GO) mod download

# 编译项目并将二进制放入 bin 目录
.PHONY: build
build: deps
	@mkdir -p $(BIN_DIR)
	$(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_DIR)/main.go

# 运行项目
.PHONY: run
run: build
	$(BIN_DIR)/$(BINARY_NAME)

# 测试
.PHONY: test
test:
	$(GO) test -v ./... -cover

# 格式化代码
.PHONY: fmt
fmt:
	$(GO) fmt ./...
	gofmt -s -w .
	goimports -w .

# 检查代码质量
.PHONY: lint
lint:
	$(GOLINT) run ./...

# 清理编译产物
.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
	$(GO) clean

# 生成 Swagger 文档（假设使用 swag）
.PHONY: swagger
swagger:
	swag init -g $(CMD_DIR)/main.go -o api/swagger

# 构建 Docker 镜像
.PHONY: docker-build
docker-build:
	$(DOCKER) build -t $(MODULE):$(VERSION) -f Dockerfile .

# 运行 Docker 容器
.PHONY: docker-run
docker-run: docker-build
	$(DOCKER) run -p 8080:8080 $(MODULE):$(VERSION)

# 性能测试（使用 wrk）
.PHONY: bench
bench: build
	$(BIN_DIR)/$(BINARY_NAME) & \
	sleep 2; \
	$(WRK) -t10 -c100 -d30s http://localhost:8080/health; \
	pkill $(BINARY_NAME)

# 安装工具（可选）
.PHONY: tools
tools:
	$(GO) install github.com/swaggo/swag/cmd/swag@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	# 如果 wrk 未安装，可手动安装：https://github.com/wg/wrk

# 显示版本信息
.PHONY: version
version:
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Go Version: $(GO_VERSION)"

.PHONY: run-test-services
run-test-services:
	$(GO) run test/services.go