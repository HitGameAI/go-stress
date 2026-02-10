# 快速开始

## 📦 安装

```bash
git clone https://github.com/kamalyes/go-stress.git
cd go-stress
go build -o go-stress
```

## 🚀 基础使用

### HTTP GET 请求

```bash
go-stress -url https://httpbin.org/get -c 10 -n 100
```

- `-url`: 目标 URL
- `-c 10`: 10 个并发
- `-n 100`: 每个并发执行 100 个请求

### POST 请求

```bash
go-stress \
  -url https://httpbin.org/post \
  -method POST \
  -H "Content-Type: application/json" \
  -data '{"test":"data"}' \
  -c 20 -n 500
```

### 使用 curl 文件

如果已有 curl 命令，可直接解析：

```bash
# request.curl
curl 'https://api.example.com/users' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer token123' \
  --data '{"name":"test"}'
```

```bash
go-stress -curl request.curl -c 100 -n 1000
```

## ⚙️ 配置文件

创建 `config.yaml`：

```yaml
protocol: http
concurrency: 50
requests: 1000
timeout: 10s
url: https://api.example.com/users
method: POST
headers:
  Content-Type: application/json
body: '{"username":"test"}'
```

```bash
go-stress -config config.yaml
```

更多配置选项请参考：[配置文件详解](CONFIG_FILE.md)

## 🔥 复杂实战案例

项目包含两个完整的实战案例配置，位于 `testserver/` 目录：

### 案例1：工单创建和消息发送链式测试

**文件**：`testserver/ticket-and-send.yaml`

完整的业务流程压测：创建工单 → 发送50条消息（带依赖）

```bash
# 启动测试服务器
cd testserver
go run test_server.go

# 执行压测（另一个终端）
go-stress -config testserver/ticket-and-send.yaml -storage sqlite
```

**核心特性**：
- ✅ API 依赖链：`send_message` 依赖 `create_ticket`
- ✅ 变量提取：自动提取 `ticket_id`、`session_id`、`user_id`
- ✅ repeat 机制：每个工单发送 50 条消息
- ✅ 复杂变量：`{{md5 (print (unixNano) (randomString 16))}}`
- ✅ 多层验证：STATUS_CODE + JSONPATH + REGEX

**预期结果**：
```
并发: 100
总请求: 100 (create_ticket) + 5000 (send_message) = 5100
```

### 案例2：完整验证器测试

**文件**：`testserver/test-detail.yaml`

覆盖全部 18 种验证器类型的完整测试流程

```bash
go-stress -config testserver/test-detail.yaml
```

**验证器覆盖**：
1. `STATUS_CODE` - 状态码验证
2. `JSON_VALID` - JSON 格式验证
3. `JSONPATH` - JSON 路径值验证
4. `HEADER` - 响应头验证
5. `LENGTH` - 长度验证
6. `UUID` - UUID 格式验证
7. `NOT_EMPTY` - 非空验证
8. `CONTAINS` - 包含文本验证
9. `REGEX` - 正则表达式验证
10. `EMAIL` - 邮箱格式验证
11. `PREFIX` - 前缀验证
12. `SUFFIX` - 后缀验证
13. `RESPONSE_SIZE` - 响应大小验证
14. 等等...

### 案例3：curl 文件压测

**文件**：`testserver/example.curl.txt`

```bash
go-stress -curl testserver/example.curl.txt -c 100 -n 1000
```

自动解析 curl 命令的所有参数（URL、Headers、Body）

### 自定义测试

基于这两个模板修改即可：

```bash
# 复制模板
cp testserver/ticket-and-send.yaml my-test.yaml

# 修改配置
vim my-test.yaml

# 执行压测
go-stress -config my-test.yaml -storage sqlite -max-memory 2GB
```

## 📝 下一步

- [命令行参考](CLI_REFERENCE.md) - 查看所有命令行参数
- [配置文件](CONFIG_FILE.md) - 学习完整配置语法
- [变量和参数化](VARIABLES.md) - 20+ 内置模板函数
- [分布式压测](DISTRIBUTED_MODE.md) - 使用多台机器压测
