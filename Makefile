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

# 监控相关
GRAFANA_PROVISIONING_DIR = test/docker/grafana/provisioning
DASHBOARD_SOURCE = test/docker/prometheus.dashboard.txt
DASHBOARD_DEST = test/docker/grafana/dashboards/gateway.json

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
	@rm -f logs/gateway.log  # 清理日志文件
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

.PHONY: manage-test-start
manage-test-start:
	chmod +x ./test/manage_test_services.sh
	@echo "Starting test services via script..."
	@./test/manage_test_services.sh start

.PHONY: manage-test-stop
manage-test-stop:
	chmod +x ./test/manage_test_services.sh
	@echo "Stopping test services via script..."
	@./test/manage_test_services.sh stop

.PHONY: manage-test-status
manage-test-status:
	chmod +x ./test/manage_test_services.sh
	@echo "Checking test services status via script..."
	@./test/manage_test_services.sh status

.PHONY: setup-consul
setup-consul:
	@echo "Checking if Consul is installed..."
	@command -v consul >/dev/null 2>&1 || { echo "Consul not found. Please install Consul first."; exit 1; }
	@echo "Starting Consul agent in dev mode..."
	@consul agent -dev & \
	sleep 2; \
	echo "Pushing load balancer rules to Consul KV Store..."; \
	curl -X PUT -d '{"/api/v1/user": ["http://localhost:8381", "http://localhost:8383"], "/api/v1/order": ["http://localhost:8382"]}' http://localhost:8500/v1/kv/gateway/loadbalancer/rules; \
	echo "Consul test environment setup complete."; \
	echo "Load balancer rules:"; \
	curl http://localhost:8500/v1/kv/gateway/loadbalancer/rules?raw


# 生成 protobuf 文件
.PHONY: proto
proto:
	protoc -I . \
		-I /Users/penwyp/Dat/googleapis \
		--go_out=./proto \
		--go_opt=paths=source_relative \
		--go-grpc_out=./proto \
		--go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=./proto \
		--grpc-gateway_opt=paths=source_relative \
		--grpc-gateway_opt generate_unbound_methods=true \
		--plugin=protoc-gen-grpc-gateway=$(shell go env GOPATH)/bin/protoc-gen-grpc-gateway \
		./proto/hello.proto

# 设置 Grafana 配置
.PHONY: setup-grafana
setup-grafana:
	chmod +x test/docker/setup_grafana.sh
	@./test/docker/setup_grafana.sh

# 启动监控服务
.PHONY: setup-monitoring
setup-monitoring:
	chmod +x test/docker/setup_monitoring.sh
	@./test/docker/setup_monitoring.sh

# 停止监控服务
.PHONY: stop-monitoring
stop-monitoring:
	@docker-compose -f test/docker/docker-compose.yml down
	@echo "监控服务已停止"