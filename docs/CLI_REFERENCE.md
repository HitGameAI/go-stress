# 命令行参数参考

共 45 个参数，下表逐项对应 `main.go` 中的 flag 定义

> 所有默认值均取自源码，`-` 表示默认为空

## 📋 配置来源

一次运行只会采用一个配置来源，优先级自上而下：

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-jmx` | string | - | JMeter 脚本路径（.jmx），见 [JMeter 导入](JMETER_IMPORT.md) |
| `-curl` | string | - | curl 命令文件路径 |
| `-config` | string | - | 配置文件路径（yaml/json），见 [配置文件](CONFIG_FILE.md) |
| 命令行参数 | - | - | 以上都未给出时，由 `-url` 等参数直接构建 |

`-c` / `-n` / `-timeout` / `-storage` **只在命令行显式写出时**才覆盖文件里的同名配置

这四个参数都有非零默认值（`1` / `1` / `30s` / `memory`），无法靠「值是否为零」区分「用户要求并发 1」和「用户没提并发」，因此改用 `flag.Visit` 判断是否被显式指定 — 否则脚本里写的并发 50 会被默认值 1 静默改掉

## 🎯 请求参数

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-protocol` | string | `http` | 协议：`http` / `grpc` / `websocket` |
| `-url` | string | - | 目标 URL |
| `-method` | string | `GET` | 请求方法 |
| `-H` | string | - | 请求头，可重复多次，形如 `-H "Content-Type: application/json"` |
| `-data` | string | - | 请求体数据 |
| `-timeout` | duration | `30s` | 单个请求超时 |

> `-data` 是请求体参数，**没有** `-body` 这个参数

## ⚡ 压力参数

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-c` | uint64 | `1` | 并发数 |
| `-n` | uint64 | `1` | 每个并发的请求数，总请求数 = `c × n` |
| `-qps` | float64 | `0` | 目标速率（请求/秒），`0` 表示全速发压 |

`-qps` 把全部并发的发压速率约束到给定值，用于回答「在固定到达率下延迟与错误率如何」 — 不限速时施加的压力取决于被测系统的响应速度，系统一慢压力自己就降下来，固定负载下的表现无从测出

更多负载形态（思考时间、渐进启动、数据驱动）见 [负载形态](LOAD_PROFILE.md)

## 🔌 HTTP 参数

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-http2` | bool | `false` | 启用 HTTP/2 协商 |
| `-keepalive` | bool | `true` | 使用长连接，`-keepalive=false` 关闭以测量建连开销 |

关掉长连接后每个请求各用一条新连接：实测 100 个请求从 4 条连接变为 99 条

## 🔗 gRPC 参数

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-grpc-reflection` | bool | `false` | 通过服务端反射获取方法定义 |
| `-grpc-service` | string | - | gRPC 服务名 |
| `-grpc-method` | string | - | gRPC 方法名 |

gRPC 的 TLS 配置只能在配置文件里给出，见 [配置文件 · gRPC](CONFIG_FILE.md#grpc-配置)

## 🔍 链路追踪

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-no-trace-header` | bool | `false` | 不注入 `traceparent` 请求头 |

默认每个请求都带 W3C `traceparent`，使压测结果能与被压服务的日志对齐，见 [链路追踪](TRACING.md)

## 💾 存储与报告

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-storage` | string | `memory` | 存储模式：`memory` / `sqlite` / `badger` |
| `-report-prefix` | string | `stress-report` | 报告目录名前缀 |
| `-no-report` | bool | `false` | 不生成报告文件 |
| `-no-print` | bool | `false` | 不在终端打印报告表格 |
| `-no-wait` | bool | `false` | 压测结束即退出，不驻留实时报告服务器 |
| `-max-memory` | string | - | 内存阈值，超过即自动停止，形如 `1GB` / `512MB` / `2048KB` |

默认行为是压测结束后**保持进程运行**以便查看实时报告，CI 与脚本场景需要加 `-no-wait`

存储模式的取舍见 [存储适配器](STORAGE_ADAPTERS.md)

## 🧩 JMeter 导入

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-jmx` | string | - | JMeter 脚本路径 |
| `-jmx-dump` | string | - | 把脚本转换为 go-stress 原生 YAML 写入该路径，**只转换不压测** |
| `-jmx-allow-lossy` | bool | `false` | 即使存在会改变施加负载的元素也继续导入 |

默认遇到会改变负载模型的元素（限速定时器、逻辑控制器、脚本处理器等）即报错退出并逐条列明原因，详见 [JMeter 导入](JMETER_IMPORT.md)

## 🌐 分布式参数

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-mode` | string | `standalone` | 运行模式：`standalone` / `master` / `slave` |
| `-master` | string | - | Master 地址，Slave 模式必需，形如 `localhost:9090` |
| `-slave-id` | string | - | Slave 节点 ID，留空则自动生成 |
| `-region` | string | `default` | 节点区域标签，供按区域选择节点使用 |
| `-grpc-port` | int | `9090` | gRPC 服务端口 |
| `-http-port` | int | `8080` | HTTP 管理端口，Master 模式使用 |
| `-realtime-port` | int | `0` | 实时报告端口，Slave 模式使用，`0` 表示自动分配 |

### 节点调度

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-workers-per-slave` | int | `100` | 每个 Slave 承担的 Worker 数量 |
| `-min-slave-count` | int | `1` | 启动任务所需的最小 Slave 数量 |
| `-max-failures` | int | `3` | 判定节点不健康的最大连续失败次数 |
| `-heartbeat-interval` | duration | `5s` | 心跳间隔 |
| `-heartbeat-timeout` | duration | `15s` | 心跳超时 |

### 认证

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-master-secret` | string | `go-stress-secret-key` | Master 密钥，生产环境务必修改 |
| `-token-issuer` | string | `go-stress-master` | Token 签发者 |
| `-token-expiration` | duration | `24h` | Token 有效期 |

完整说明见 [分布式模式](DISTRIBUTED_MODE.md)

## 📝 日志参数

| 参数 | 类型 | 默认值 | 说明 |
|:-----|:-----|:-------|:-----|
| `-log-level` | string | `info` | 日志级别：`debug` / `info` / `warn` / `error` |
| `-log-file` | string | - | 日志文件路径，给出后按 100MB 滚动、保留 5 个历史文件 |
| `-verbose` | bool | `false` | 等价于 `-log-level debug` 并打开调用者信息 |
| `-quiet` | bool | `false` | 等价于 `-log-level error` |

优先级：`-verbose` > `-quiet` > `-log-level`

着色按输出目标自动判定：`NO_COLOR` 非空、`TERM=dumb`、输出不是终端时自动关闭，重定向到文件不会混入 ANSI 转义序列

## 🔖 子命令

| 子命令 | 说明 |
|:-------|:-----|
| `help` / `-h` / `--help` | 完整帮助 |
| `variables` / `vars` | 列出全部 78 个内置变量函数 |
| `examples` / `demo` | 详细使用示例 |
| `version` / `-v` / `--version` | 版本信息 |

## 💡 常用组合

```bash
# 最简压测
go-stress -url https://api.example.com -c 10 -n 100

# CI 场景：不驻留、不写报告文件、只报错误
go-stress -config plan.yaml -no-wait -no-report -quiet

# 定速压测：固定 500 请求/秒
go-stress -url https://api.example.com -c 50 -n 1000 -qps 500

# 按时长压测：持续 5 分钟，定速 200 请求/秒
go-stress -url https://api.example.com -c 30 -duration 5m -qps 200

# 测量建连开销：关掉长连接
go-stress -url https://api.example.com -c 20 -n 500 -keepalive=false

# 持久化到 BadgerDB，便于事后按节点/任务查询
go-stress -config plan.yaml -storage badger

# 导入 JMeter 脚本并导出为原生配置
go-stress -jmx plan.jmx -jmx-dump plan.yaml
```
