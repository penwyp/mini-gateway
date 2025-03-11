#!/bin/bash

# manage_test_services.sh
# 用于管理 mini-gateway 测试服务的启动和停止

# 定义服务文件路径
HTTP_SERVICE="test/service/http_services.go"
WEBSOCKET_SERVICE="test/service/websocket_service.go"
GRPC_SERVICE="test/service/grpc_service.go"

# 日志文件
LOG_DIR="logs"
HTTP_LOG="$LOG_DIR/http_service.log"
WEBSOCKET_LOG="$LOG_DIR/websocket_service.log"
GRPC_LOG="$LOG_DIR/grpc_service.log"

# 确保日志目录存在
mkdir -p "$LOG_DIR"

# 启动服务
start_services() {
    echo "Starting all test services..."

    # 启动 HTTP 服务
    go run "$HTTP_SERVICE" > "$HTTP_LOG" 2>&1 &
    HTTP_PID=$!
    echo "HTTP Service started with PID $HTTP_PID"

    # 启动 WebSocket 服务
    go run "$WEBSOCKET_SERVICE" > "$WEBSOCKET_LOG" 2>&1 &
    WEBSOCKET_PID=$!
    echo "WebSocket Service started with PID $WEBSOCKET_PID"

    # 启动 gRPC 服务
    go run "$GRPC_SERVICE" > "$GRPC_LOG" 2>&1 &
    GRPC_PID=$!
    echo "gRPC Service started with PID $GRPC_PID"

    echo "All test services are running in the background."
    echo "Logs are written to: $LOG_DIR"
}

# 停止服务
stop_services() {
    echo "Stopping all test services..."

    # 使用 pkill 停止服务，基于文件名匹配
    pkill -f "go run $HTTP_SERVICE" && echo "HTTP Service stopped" || echo "No HTTP Service running"
    pkill -f "go run $WEBSOCKET_SERVICE" && echo "WebSocket Service stopped" || echo "No WebSocket Service running"
    pkill -f "go run $GRPC_SERVICE" && echo "gRPC Service stopped" || echo "No gRPC Service running"

    ps aux | grep "tmp/go-build" | grep exe | grep _services |  awk '{print $2}' | xargs kill -9

    echo "All test services stopped."
}

# 检查服务状态
status_services() {
    echo "Checking test services status..."

    if pgrep -f "go run $HTTP_SERVICE" > /dev/null; then
        echo "HTTP Service is running"
    else
        echo "HTTP Service is not running"
    fi

    if pgrep -f "go run $WEBSOCKET_SERVICE" > /dev/null; then
        echo "WebSocket Service is running"
    else
        echo "WebSocket Service is not running"
    fi

    if pgrep -f "go run $GRPC_SERVICE" > /dev/null; then
        echo "gRPC Service is running"
    else
        echo "gRPC Service is not running"
    fi
}

# 主逻辑
case "$1" in
    start)
        start_services
        ;;
    stop)
        stop_services
        ;;
    status)
        status_services
        ;;
    *)
        echo "Usage: $0 {start|stop|status}"
        exit 1
        ;;
esac

exit 0