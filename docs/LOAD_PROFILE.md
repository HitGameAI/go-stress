# 负载形态

压测施加的压力由四件事决定：**并发数**、**到达率**、**请求间停顿**、**每个请求带什么数据**

搞混它们会得出看着合理却没有意义的结论 — 本文逐项说明如何控制

## 🧭 先选开环还是闭环

| | 开环（定速） | 闭环（并发 + 思考时间） |
|:---|:---|:---|
| **谁决定压力** | 你指定的到达率 | 被测系统的响应速度 |
| **系统变慢时** | 压力不变，排队堆积，延迟上升 | 压力自动下降 |
| **回答的问题** | 「固定 500 QPS 下延迟与错误率如何」 | 「500 个用户同时在用时体验如何」 |
| **适用** | 容量规划、SLO 验证、对比不同版本 | 模拟真实用户、找系统上限 |
| **配置** | `-qps` / `advanced.target_qps` | `-c` + `think_time` |

不限速且无思考时间就是「有多快打多快」：这能测出系统上限，但测不出固定负载下的表现 — 因为系统一慢，压力自己就降下来了

## ⚡ 定速压测

把全部并发的发压速率约束到目标值

```bash
go-stress -url https://api.example.com -c 50 -n 5000 -qps 500
```

```yaml
advanced:
  target_qps: 500        # 请求/秒，0 或省略表示全速
```

### 行为细节

| 项 | 说明 |
|:---|:-----|
| **作用范围** | 全局共享，不是每个并发各限该速率 |
| **发牌方式** | 匀速而非令牌桶 — 桶会让攒下的令牌一次性放行造成到达率尖刺 |
| **误差累积** | 每次放行的时刻由序号推算而非增量累加，长跑不漂移 |
| **长时卡顿** | 落后超过十个间隔时重新对齐，避免卡顿后拼命补发造成尖刺 |
| **等待时机** | 在变量替换之后、真正发请求之前等待，故不占用连接，也不计入请求耗时 |
| **可中断** | 等待期间响应 `ctx` 取消，Ctrl-C 不必等满一个发牌间隔 |

### 并发数该给多少

定速时并发数决定的是「能容纳多少个同时在途的请求」，需满足：

```
并发数 ≥ 目标 QPS × 预期延迟
```

例如目标 500 QPS、预期延迟 200ms，则至少需要 `500 × 0.2 = 100` 个并发 — 并发不足时达不到目标速率，报告里的实际 QPS 会低于 `target_qps`

## ⏱️ 思考时间

请求之间的停顿，对应 JMeter 的各类 Timer

```yaml
think_time:
  type: constant       # constant | uniform | gaussian，省略按 constant
  delay: 1s
```

### 三种分布

| type | 实际停顿 | 需要的字段 | 适用 |
|:-----|:---------|:-----------|:-----|
| `constant` | `delay` | `delay` | 固定节奏，便于复现 |
| `uniform` | `delay + [0, range)` 均匀分布 | `delay`、`range` | 打散请求，避免同时到达 |
| `gaussian` | `delay + N(0, deviation)`，负值截断为 0 | `delay`、`deviation` | 最接近真人操作间隔 |

```yaml
# 均匀分布：停顿落在 [500ms, 1500ms)
think_time:
  type: uniform
  delay: 500ms
  range: 1s

# 正态分布：以 1s 为中心，标准差 300ms
think_time:
  type: gaussian
  delay: 1s
  deviation: 300ms
```

正态分布有一侧尾部为负，实现中截断为 0 而非取绝对值 — 取绝对值会把负尾折回正侧，使实际均值高于配置的 `delay`

### 两级作用域

JMeter 的 Timer 既可挂在测试计划下也可挂在单个采样器下，故两级都支持，**API 级覆盖全局**：

```yaml
think_time:              # 全局默认
  type: constant
  delay: 1s

apis:
  - name: login
    url: https://api.example.com/login
    # 未声明，沿用全局 1s

  - name: poll_status
    url: https://api.example.com/status
    think_time:          # 覆盖全局
      type: constant
      delay: 100ms
```

缺少随机参数时自动退化为 `constant`：`type: uniform` 但没给 `range`，等效于固定 `delay`

等待期间响应 `ctx` 取消 — 用裸 `Sleep` 的话，带数秒思考时间的压测在 Ctrl-C 之后最坏要等满一个思考周期才退出

## 📈 渐进启动

并发不是瞬间全开，而在给定时间内线性拉起

```yaml
advanced:
  ramp_up: 30s           # 30 秒内把并发从 0 拉到配置值
```

避免瞬时冲击让服务端在连接风暴中直接拒绝，掩盖掉真正要测的东西

## 📑 CSV 数据驱动

让每个请求各带不同参数

一万次请求都打同一个账号时，服务端的缓存命中率、唯一索引冲突、按用户维度的限流都会把结果全面带偏

```yaml
csv_data:
  path: users.csv        # 相对路径按配置文件所在目录解析
  sharing: all
  recycle: true

url: "https://api.example.com/login?user={{.username}}"
body: '{"user":"{{.username}}","pass":"{{.password}}"}'
```

`users.csv`：

```csv
username,password
alice,p1
bob,p2
carol,p3
```

列名即变量名，用 `{{.列名}}` 引用，可出现在 URL、请求头、请求体中

### 全部字段

| 字段 | 类型 | 默认 | 说明 |
|:-----|:-----|:-----|:-----|
| `path` | string | 必填 | CSV 文件路径 |
| `variables` | string[] | 取首行表头 | 显式指定变量名，按列顺序对应 |
| `delimiter` | string | `,` | 分隔符，只取首个字符 |
| `recycle` | bool | `true` | 数据用尽后是否回到首行 |
| `ignore_first_line` | bool | `false` | 显式给了 `variables` 时是否仍跳过首行 |
| `sharing` | string | `all` | 游标共享方式，见下 |

### 两种共享方式

| sharing | 行为 | 对应 JMeter |
|:--------|:-----|:------------|
| `all` | 全部并发共享一个游标，各条请求序列取走不同行 | All threads |
| `worker` | 每个并发各持独立游标，都从第一行开始 | Current thread |

需要「每个虚拟用户用不同账号」时选 `all`；需要「每个并发重复跑同一批数据」时选 `worker`

### 取数时机

按**请求序列**推进，而非每个请求推进一次 — 一行数据代表一个虚拟用户的身份，`登录 → 下单` 这条依赖链理应共用同一行，这与 JMeter 的 CSVDataSet 一致

```yaml
# 每轮循环取一行，login 与 order 共用同一个 username
csv_data:
  path: users.csv

apis:
  - name: login
    body: '{"user":"{{.username}}"}'
  - name: order
    depends_on: [login]
    body: '{"user":"{{.username}}","item":"x"}'
```

### 数据用尽

`recycle: true`（默认）回到首行继续；`recycle: false` 时数据取完即结束该并发，与 JMeter 的 `stopThread` 行为一致，日志中会给出已完成的请求数

### 性能

数据在启动时整份读入内存，热路径上取一行只是一次原子自增加一次下标访问，不做文件 IO 与解析 — 大文件会占用相应内存，但不会拖慢发压

## 🧮 组合示例

模拟 200 个真实用户，各自带不同账号、操作间隔约 2 秒、整体不超过 80 请求/秒：

```yaml
protocol: http
concurrency: 200
requests: 100
timeout: 10s

think_time:
  type: gaussian
  delay: 2s
  deviation: 500ms

csv_data:
  path: users.csv
  sharing: all
  recycle: true

advanced:
  ramp_up: 20s
  target_qps: 80

url: "https://api.example.com/profile?user={{.username}}"
```

定速与思考时间可以共存：前者约束全局到达率（开环），后者约束单个虚拟用户的操作间隔（闭环），两者取更严格的那个生效
