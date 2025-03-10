# 测试方案
## 1. 限流
wrk -t10 -c100 -d30s http://localhost:8080/health
这将完整展示限流效果，根据配置中的 QPS 和 burst 参数限制请求速率。可以通过切换 algorithm 值来测试不同算法的效果。

## 2. 染色
1. 手动测试：
- 不带 Header：curl http://localhost:8080/api/v1/user
    - 80% 概率路由到 stable，20% 到 canary。
- 带 Header：curl -H "X-Env: canary" http://localhost:8080/api/v1/user
    - 100% 路由到 canary。
2. 压力测试：
- 使用 wrk：wrk -t10 -c100 -d10s http://localhost:8080/api/v1/user
- 检查日志，验证流量分配比例是否接近 80:20。
3. Header 注入验证：
- 在下游服务打印接收到的 X-Env Header，确保 canary 请求带有正确标记。
4. 日志验证：
- 检查 gateway.log，确认 env 和 target