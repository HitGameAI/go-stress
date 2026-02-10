# JMeter 脚本导入

把现有 `.jmx` 脚本转成 go-stress 配置，免去照着 GUI 一项项重敲

```bash
# 直接跑
go-stress -jmx plan.jmx

# 转成原生 YAML，便于审阅、手工调整、纳入版本管理
go-stress -jmx plan.jmx -jmx-dump plan.yaml
```

`-jmx-dump` **只转换不压测** — 导出是一次性的配置迁移动作，与真正发压混在一条命令里容易误触

## ⚠️ 先读这一节

导入必然有损，因此**转换报告是这个功能的主体而非附属品**

压测工具最坏的失败模式不是报错，而是静默产出一个「不是你要的那个测试」，数字却煞有介事 — 一个带 1 秒 ConstantTimer 的脚本描述的是带思考时间的闭环负载，丢掉 timer 后变成全速发压，打给服务端的真实 QPS 会差出一个数量级，而报告看起来完全正常

因此遇到**会改变施加负载**的元素时，默认**直接报错退出**并逐条列明原因：

```
JMeter 脚本转换报告: plan.jmx (JMeter 5.6.3)
────────────────────────────────────────────────────────────
  ✓ 线程组「组A」→ 并发 20，每并发 100 次，ramp-up 5s
  ✓ 请求「下单」POST http://shop.example.com/order

  未能导入:
  ✗ [FATAL] IfController 「仅 VIP」
      条件控制器决定请求是否发出，忽略它会多发请求
  ✗ [FATAL] JSR223PostProcessor 「算签名」
      脚本后置处理器可改写提取结果，无法转换

错误: 脚本中有 2 处无法导入且会改变施加的负载（IfController, JSR223PostProcessor），
      按原样导入会得出错误结论；确认可接受差异后加 -jmx-allow-lossy 继续
```

确认差异可接受后：

```bash
go-stress -jmx plan.jmx -jmx-allow-lossy
```

### 另外两点

**导入只还原「测试意图」，不等于能复现 JMeter 的压测结果** — 两者的连接复用、线程模型、断言开销都不同，同一份脚本两边跑出的 QPS 本来就不该一样，拿它做跨工具对比会得出错误结论

**命令行参数可覆盖脚本** — `-c` / `-n` / `-timeout` 在显式写出时覆盖脚本中的对应值，便于用同一份脚本调整压力

## ✅ 已支持的元素

### 线程组

| JMeter | go-stress | 说明 |
|:-------|:----------|:-----|
| `ThreadGroup.num_threads` | `concurrency` | |
| `LoopController.loops` | `requests` | 每并发请求数 |
| `ThreadGroup.ramp_time` | `advanced.ramp_up` | 单位由秒转为 duration |
| `ThreadGroup.scheduler` + `duration` | `duration` | 开启调度器时以持续时间为准，JMeter 此时也忽略循环次数 |
| `ThreadGroup.on_sample_error` | 见下文「失败传播」 | |

`LoopController.continue_forever` 为真且未设持续时间时无法推出请求总数，报 FATAL — 请在脚本里设定循环次数或持续时间，或导入后用 `-n` / `-duration` 指定

### HTTP 采样器

| JMeter | go-stress |
|:-------|:----------|
| `HTTPSampler.protocol` + `domain` + `port` + `path` | `url`（拼成完整地址，默认端口不写入） |
| `HTTPSampler.method` | `method` |
| `HTTPSampler.postBodyRaw` = true | `body`（取 Arguments 首个值作原始报文） |
| `HTTPSampler.postBodyRaw` = false | `body`（Arguments 拼成 `a=1&b=2` 表单） |
| `HeaderManager` | `headers` |

`path` 本身已是完整 URL 时直接采用

**HTTP 请求默认值**（`ConfigTestElement` / HttpDefaultsGui）同样支持：真实脚本极常把域名、端口、协议统一写在这里、采样器只填 path，默认值只填补采样器留空的字段，不做前缀拼接，与 JMeter 行为一致；其 `response_timeout` 映射为 `timeout`

### 定时器

| JMeter | go-stress |
|:-------|:----------|
| `ConstantTimer` | `think_time: {type: constant, delay}` |
| `UniformRandomTimer` | `think_time: {type: uniform, delay, range}` |
| `GaussianRandomTimer` | `think_time: {type: gaussian, delay, deviation}` |
| `ConstantThroughputTimer` | `advanced.target_qps` |
| `PreciseThroughputTimer` | `advanced.target_qps` |

吞吐量定时器的 `throughput` 单位是**每分钟**，换算为每秒写入 `target_qps`

`calcMode` 按真实语义换算：`0` 表示每线程各跑该速率，故总到达率为 `速率 × 并发数`，据此换算而非原样照搬 — 照搬会让实际压力小到并发倍数分之一；`1` 及以上为全局共享，与 go-stress 的限流器一致

定时器与思考时间详见 [负载形态](LOAD_PROFILE.md)

### 数据源

| JMeter | go-stress |
|:-------|:----------|
| `CSVDataSet.filename` | `csv_data.path`（相对路径按 `.jmx` 所在目录解析） |
| `CSVDataSet.variableNames` | `csv_data.variables` |
| `CSVDataSet.delimiter` | `csv_data.delimiter` |
| `CSVDataSet.recycle` | `csv_data.recycle` |
| `CSVDataSet.ignoreFirstLine` | `csv_data.ignore_first_line` |
| `CSVDataSet.shareMode` | `csv_data.sharing`（`shareMode.all` → `all`，每线程 → `worker`） |

go-stress 目前只支持一个数据源，脚本里有多个 `CSVDataSet` 时报 FATAL — 多份数据会改变参数组合，请合并成一个 CSV 后再导入

### 断言

| JMeter | go-stress |
|:-------|:----------|
| `ResponseAssertion` + `response_code` | `STATUS_CODE`（含 NOT 位 → `ne`） |
| `ResponseAssertion` + `response_data` + Substring/Contains | `CONTAINS`（含 NOT 位 → `not_contains`） |
| `ResponseAssertion` + `response_data` + Matches | `REGEX` |
| `JSONPathAssertion` | `JSONPATH` |
| `DurationAssertion` | `RESPONSE_TIME`（`lte`） |
| `SizeAssertion` | `RESPONSE_SIZE`（按 `operator` 取 `eq`/`ne`/`gt`/`lt`/`gte`/`lte`） |

JMeter 的匹配方式是位掩码（`MATCH=1 CONTAINS=2 NOT=4 EQUALS=8 SUBSTRING=16 OR=32`），逐位解析

不支持的组合会记 WARN 并跳过该条：「响应体全文相等」与「不匹配正则」没有对应的验证类型；多个期望值以「或」组合时逐条转换，于是校验会比脚本更严格（go-stress 的多条验证之间是「与」）

### 提取器

| JMeter | go-stress |
|:-------|:----------|
| `JSONPostProcessor` | `extractors` 型 `JSONPATH`（支持一个元素声明多个变量，分号分隔） |
| `RegexExtractor` | `extractors` 型 `REGEX` |

`defaultValues` 映射为 `default`；`RegexExtractor.template` 非 `$1$` 或 `useHeaders` 非 false 时记 WARN 并按默认行为处理

### 变量

`${name}` 自动改写为 go-stress 的 `{{.name}}`，作用于 URL、请求头、请求体、断言期望值、JSONPath

`TestPlan` 与 `Arguments` 里的用户定义变量映射为 `variables`

JMeter 内置函数 `${__xxx(...)}` **不改写也不求值**，原样保留并记 WARN — 请改用 go-stress 的 [变量函数](VARIABLES.md)

## 🔗 执行顺序与失败传播

JMeter 的采样器在每轮循环里**全部按序执行**，而 go-stress 在没有 `depends_on` 时走 API 选择器（轮询），**每轮只发其中一个**

因此导入时会把采样器按脚本顺序串成一条依赖链 — 不串的话，`登录 → 查询` 会变成交替发送，请求配比与依赖关系双双不成立

依赖链有个副作用必须说明：**上游失败时下游会被跳过**，而 JMeter 的默认错误处理是 `continue`（某个采样器失败，后面的照发）

- 验证失败：已自动设 `continue_on_failure`，不阻断后续请求
- 传输层失败（连接被拒、超时）：仍会使后续请求按「依赖失败」跳过，统计里会多出跳过计数

这点差异会写进转换报告而非默默带过

## ❌ 不支持的元素

### FATAL — 会改变施加的负载，默认拒绝导入

| 元素 | 原因 |
|:-----|:-----|
| `PoissonRandomTimer` | 泊松分布定时器没有对应的思考时间类型 |
| `SyncTimer` | 集合点会把请求攒成批次再齐发，忽略它会改变请求的时间分布 |
| `ThroughputController` | 按比例裁剪请求，忽略它会改变各请求的占比 |
| `IfController` | 决定请求是否发出，忽略它会多发请求 |
| `WhileController` / `LoopController`（嵌套） / `ForeachController` | 决定请求发几次，忽略它会改变请求总数 |
| `RandomController` / `InterleaveControl` / `SwitchController` | 每轮只执行其中一个子项，忽略它会把全部子项都发出去 |
| `RandomOrderController` | 会改变请求顺序 |
| `RunTime` | 限定子项的执行时长 |
| `JSR223PreProcessor` / `JSR223PostProcessor` / `JSR223Sampler` | 脚本可改写请求内容或提取结果 |
| `BeanShellPreProcessor` / `BeanShellPostProcessor` / `BeanShellSampler` | 同上 |
| `AuthManager` | 自动附加认证头，缺了它请求可能全部被拒 |
| `CookieManager` | 维护会话，缺了它依赖登录态的请求会失败 |
| `TransactionController` | 聚合子请求的耗时统计，指标口径与导入后不同 |
| `ModuleController` / `IncludeController` / `TestFragmentController` | 内容不在本处 |
| `CriticalSectionController` | 会串行化子项的执行 |
| 多个 `ThreadGroup` | go-stress 只有一组全局并发，合并会改变各组的并发比例，请拆成多个脚本分别导入 |
| **任何未知元素** | 无法判断是否影响负载，宁可挡下让人确认 |

### WARN — 只影响校验强度或统计口径

`XPathAssertion`、`XPath2Assertion`、`HTMLAssertion`、`MD5HexAssertion`、`SMIMEAssertion`、`CompareAssertion`、`XPathExtractor`、`XPath2Extractor`、`HtmlExtractor`、`BoundaryExtractor`、`UserParameters`、`RandomVariableConfig`、`CounterConfig`、`DNSCacheManager`、`KeystoreConfig`、`LoginConfig`、`setUp`/`tearDown` 线程组

其中部分有替代写法：`BoundaryExtractor` 可改写成正则提取器，`CounterConfig` 可改用内置的 `seq` 函数，`RandomVariableConfig` 可改用 `randomInt` 等函数，`UserParameters` 可改用 CSV 数据源

### INFO — 对负载无影响，静默忽略

各类监听器与结果展示元素（`ResultCollector`、`SummaryReport`、`ViewResultsFullVisualizer`、`BackendListener` 等），以及 `enabled="false"` 的元素 — 后者 JMeter 本身也不会执行，一并算上等于凭空多打一批请求

## 📄 完整示例

`plan.jmx`（节选）：

```xml
<ThreadGroup testname="主线程组">
  <stringProp name="ThreadGroup.num_threads">50</stringProp>
  <stringProp name="ThreadGroup.ramp_time">10</stringProp>
  <elementProp name="ThreadGroup.main_controller" elementType="LoopController">
    <stringProp name="LoopController.loops">200</stringProp>
  </elementProp>
</ThreadGroup>
```

导入结果：

```
  ✓ 用户定义变量 1 项 → variables
  ✓ 线程组「主线程组」→ 并发 50，每并发 200 次，ramp-up 10s
  ✓ CSVDataSet「账号数据」→ csv_data（users.csv，sharing=all，recycle=true）
  ✓ 线程组级请求头 2 条 → 应用到该组所有请求
  ✓ 线程组级思考时间 → 1.5s
  ✓ 请求「登录」POST https://{{.host}}:8443/v1/login（2 条验证、1 个提取器、思考时间 1.5s）
  ✓ 请求「查询订单」GET https://{{.host}}/v1/orders?token={{.token}}（1 条验证、思考时间 1.5s）
  ✓ 2 个请求按脚本顺序串为依赖链，每轮循环依次执行
```

`-jmx-dump` 导出的 YAML 开头带转换说明注释，提醒对照报告确认差异后再用于压测
