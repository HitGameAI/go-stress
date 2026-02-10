# 链路追踪

每个请求携带一个 W3C trace 标识，使压测结果能与被压服务的日志和链路对齐

压测报告里出现一条失败请求时，原本只能靠时间戳去服务端日志里猜是哪一次调用 — 现在可以直接拿 trace-id 去搜

## 🚀 默认行为

**默认开启**，无需配置：

```bash
go-stress -url https://api.example.com -c 10 -n 100
```

每个请求会：

1. 生成一个 W3C trace-context 标识
2. 以 `traceparent` 请求头发往被压服务
3. 写入 `ctx`，使 go-stress 自身的日志带上 `trace_id`
4. 记入请求结果，落入存储与报告 JSON 的 `trace_id` 字段

请求头形如：

```
traceparent: 00-6af1f5def8f7a2d50000000000000001-be4b5bd56dc0024f-01
             │  └─ trace-id（32 位十六进制）──────┘ └─ span-id ──┘ └─ flags
             └─ version
```

## 🔌 为什么用 traceparent

用 [W3C trace-context](https://www.w3.org/TR/trace-context/) 标准头而非自造的 `X-Trace-Id`：被压服务只要接了任意 OTel SDK 或主流框架的链路中间件，就会自动把它作为父 span 接上 — 服务端那条链路与压测结果共享同一个 trace-id，**无需对方改一行代码**

典型用法：压测跑完，从报告里挑出 P99 那几条请求的 trace-id，直接去 Jaeger / Tempo / 自建链路系统里搜，看服务端这一次调用慢在哪个环节

## 🔍 在哪能看到 trace-id

| 位置 | 字段 | 说明 |
|:-----|:-----|:-----|
| 被压服务 | `traceparent` 请求头 | 服务端据此接上自己的链路 |
| 报告 JSON | `trace_id` | `stress-report/<时间戳>/index.json` 中每条请求详情 |
| SQLite 存储 | `trace_id` 列 | `-storage sqlite` 时可用 SQL 查询 |
| BadgerDB 存储 | `trace_id` | `-storage badger` 时随结果 JSON 一并保存 |
| go-stress 日志 | `trace_id` | 走 `*Context*` 系列日志方法的条目自动带上 |

SQL 查询示例：

```sql
-- 找出最慢的 10 条请求及其 trace-id
SELECT trace_id, url, status_code, duration
FROM request_details
ORDER BY duration DESC
LIMIT 10;

-- 按 trace-id 定位某一次调用
SELECT * FROM request_details WHERE trace_id = '6af1f5def8f7a2d50000000000000001';
```

## 🔧 关闭注入

`traceparent` 是发往被压服务的真实请求头，需要逐字节复现原始请求时（例如签名校验把全部请求头纳入计算）可以关掉：

```bash
go-stress -url https://api.example.com -no-trace-header
```

```yaml
advanced:
  disable_trace_header: true
```

关闭后仍会生成 trace-id 并记入结果与日志，只是不往外发 — 本地关联仍然可用，只是无法与服务端对齐

## ⚙️ 实现细节

### 标识如何生成

每个 Worker 独占一个生成器，全程无锁、无原子操作：

- **trace-id** = Worker 创建时一次性随机的 8 字节前缀 + 8 字节自增序号
- **span-id** = 前缀与序号的 splitmix64 混淆值，避免与 trace-id 尾部逐位相同
- **flags** 固定 `01`，表示已采样

前缀保证跨 Worker 与跨次压测不撞，序号保证同一 Worker 内唯一

不逐请求调 `crypto/rand`：这里的标识只用于把压测结果与被压服务的日志对上，不承担任何安全语义，而压测工具自身绝不能成为被测系统的瓶颈

实测开销 **37.8 ns/op、96 B/op、2 allocs/op**（Apple M4）— 两次分配躲不掉，Go 字符串不可变，trace-id 没法复用 traceparent 的底层数组

### 为什么不直接上报 OTel

go-stress 不做 OTel 上报，这是刻意的取舍：

`traceparent` 注入后，被压服务已会把自己的 span 报到它自己的采集端，且带的是此处记录的同一个 trace-id — **排查目标已经达成**

而压测端若按每请求一个 span 上报，在本机实测的 19k QPS 下就是 19000 spans/秒：上报开销会开始与被测对象争抢资源，污染测量结果，还会压垮采集端

go-stress 只当 trace 的**发起者**，不做生产者

> go.mod 里的 `go.opentelemetry.io/otel*` 条目全是 `// indirect`，经 go-logger 与 grpc 传递进来，go-stress 自身零 OTel 代码

## 🔗 与分布式模式

分布式模式下每个 Slave 的 Worker 各自生成标识，前缀随机因此跨节点不会相撞

请求结果同时带 `node_id` 与 `task_id`，与 `trace_id` 配合即可定位「哪个节点、哪个任务、哪一次调用」：

```sql
SELECT node_id, task_id, trace_id, duration
FROM request_details
WHERE success = 0
ORDER BY duration DESC;
```
