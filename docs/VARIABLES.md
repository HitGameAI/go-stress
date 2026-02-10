# 变量和参数化

使用 Go 模板语法 `{{function}}` 实现动态参数化，支持 60+ 内置函数，涵盖时间、随机、加密、字符串处理等场景

## 快速开始

```yaml
# Header 参数化
headers:
  Authorization: "Bearer {{env \"TOKEN\"}}"
  X-Request-ID: "{{randomUUID}}"
  X-Timestamp: "{{unix}}"

# Query 参数化
url: https://api.example.com/users?id={{randomInt 1 1000}}&t={{timestamp}}

# Path 参数化
path: /users/{{.login.user_id}}/profile

# Body 参数化
body: |
  {
    "username": "user_{{randomString 8}}",
    "email": "{{randomEmail}}",
    "timestamp": {{unix}}
  }
```

---

## 内置参数化函数

### 环境 & 主机信息

获取系统环境变量和主机信息

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `env` | `{{env "API_TOKEN"}}` | `abc123token` | 读取系统环境变量 |
| `hostname` | `{{hostname}}` | `my-server` | 获取主机名 |
| `localIP` | `{{localIP}}` | `192.168.1.10` | 获取本机IP地址 |

### 序列 & 时间函数

生成自增序列号和各种时间格式

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `seq` | `{{seq}}` | `1`, `2`, `3`... | 自增序列号 |
| `unix` | `{{unix}}` | `1738022400` | Unix时间戳（秒） |
| `unixNano` | `{{unixNano}}` | `1738022400123456789` | Unix纳秒时间戳 |
| `timestamp` | `{{timestamp}}` | `1738022400123` | Unix毫秒时间戳 |
| `now` | `{{now}}` | `2026-01-25T10:30:00Z` | ISO8601格式当前时间 |
| `date` | `{{date "2006-01-02"}}` | `2026-01-25` | 自定义格式日期 |
| `dateAdd` | `{{dateAdd "24h"}}` | `2026-01-26T10:30:00Z` | 时间偏移 |
| `dateFormat` | `{{dateFormat .now "2006-01-02"}}` | `2026-01-25` | 格式化时间对象 |

### 随机函数 - 基础

生成随机数字、字符串、布尔值等基础类型

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `randomInt` | `{{randomInt 1 100}}` | `42` | 随机整数 [min, max] |
| `randomFloat` | `{{randomFloat 10.0 99.99}}` | `59.87` | 随机浮点数 [min, max] |
| `randomString` | `{{randomString 10}}` | `a7Kx9mQp2Z` | 随机字符串（字母+数字） |
| `randomAlpha` | `{{randomAlpha 8}}` | `AbCdEfGh` | 随机字母（大小写混合） |
| `randomNumber` | `{{randomNumber 6}}` | `123456` | 随机纯数字字符串 |
| `randomUUID` | `{{randomUUID}}` | `550e8400-e29b-41d4-a716-446655440000` | UUID v4 |
| `randomBool` | `{{randomBool}}` | `true` 或 `false` | 随机布尔值 |

### 随机函数 - 格式化

生成符合特定格式的随机数据

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `randomEmail` | `{{randomEmail}}` | `user_abc123@example.com` | 随机邮箱地址 |
| `randomPhone` | `{{randomPhone}}` | `13800138000` | 随机手机号（中国格式） |
| `randomIP` | `{{randomIP}}` | `192.168.1.100` | 随机IPv4地址 |
| `randomMAC` | `{{randomMAC}}` | `aa:bb:cc:dd:ee:ff` | 随机MAC地址 |
| `randomUserAgent` | `{{randomUserAgent}}` | `Mozilla/5.0...` | 随机浏览器User-Agent |

### 随机函数 - 业务场景

生成符合业务场景的随机数据

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `randomName` | `{{randomName}}` | `ZhangWei` | 随机中文姓名 |
| `randomCity` | `{{randomCity}}` | `Beijing` | 随机城市名 |
| `randomCountry` | `{{randomCountry}}` | `China` | 随机国家名 |
| `randomColor` | `{{randomColor}}` | `red` | 随机颜色名称 |
| `randomHexColor` | `{{randomHexColor}}` | `#ff0000` | 随机十六进制颜色值 |
| `randomDate` | `{{randomDate}}` | `2025-03-15` | 随机日期（最近一年内） |
| `randomTime` | `{{randomTime}}` | `14:30:45` | 随机时间 |
| `randomDateTime` | `{{randomDateTime}}` | `2025-03-15 14:30:45` | 随机日期时间 |
| `randomPrice` | `{{randomPrice 10 100}}` | `59.99` | 随机价格 [min, max] |
| `randomIDCard` | `{{randomIDCard}}` | `110101199001011234` | 随机身份证号（18位） |

### 加密 & 哈希函数

生成各种加密哈希值，常用于签名场景

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `md5` | `{{md5 "test"}}` | `098f6bcd4621d373cade4e832627b4f6` | MD5哈希（32位） |
| `sha1` | `{{sha1 "test"}}` | `a94a8fe5ccb19ba61c4c0873d391e987982fbbd3` | SHA1哈希（40位） |
| `sha256` | `{{sha256 "test"}}` | `9f86d081884c7d659a2feaa0c55ad015a3bf4f1b...` | SHA256哈希（64位） |

### 编码 & 解码函数

处理Base64、URL、Hex等编码格式

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `base64` | `{{base64 "hello"}}` | `aGVsbG8=` | Base64编码 |
| `base64Decode` | `{{base64Decode "aGVsbG8="}}` | `hello` | Base64解码 |
| `urlEncode` | `{{urlEncode "a b c"}}` | `a+b+c` | URL编码 |
| `urlDecode` | `{{urlDecode "a+b+c"}}` | `a b c` | URL解码 |
| `hexEncode` | `{{hexEncode "hello"}}` | `68656c6c6f` | 十六进制编码 |
| `hexDecode` | `{{hexDecode "68656c6c6f"}}` | `hello` | 十六进制解码 |

### 字符串处理函数

丰富的字符串操作功能

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `upper` | `{{upper "hello"}}` | `HELLO` | 转大写 |
| `lower` | `{{lower "HELLO"}}` | `hello` | 转小写 |
| `title` | `{{title "hello world"}}` | `Hello World` | 首字母大写 |
| `trim` | `{{trim " hi "}}` | `hi` | 去除首尾空格 |
| `trimPrefix` | `{{trimPrefix "hello" "he"}}` | `llo` | 去除前缀 |
| `trimSuffix` | `{{trimSuffix "hello" "lo"}}` | `hel` | 去除后缀 |
| `substr` | `{{substr "hello" 0 2}}` | `he` | 截取子字符串 |
| `replace` | `{{replace "hello" "l" "L"}}` | `heLLo` | 字符串替换 |
| `split` | `{{split "a,b,c" ","}}` | `[a b c]` | 字符串分割为数组 |
| `join` | `{{join .array ","}}` | `a,b,c` | 数组连接为字符串 |
| `contains` | `{{contains "hello" "ll"}}` | `true` | 包含判断 |
| `hasPrefix` | `{{hasPrefix "hello" "he"}}` | `true` | 前缀判断 |
| `hasSuffix` | `{{hasSuffix "hello" "lo"}}` | `true` | 后缀判断 |
| `repeat` | `{{repeat "ab" 3}}` | `ababab` | 字符串重复 |
| `reverse` | `{{reverse "hello"}}` | `olleh` | 字符串反转 |

### 数学运算函数

支持基本算术和高级数学运算

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `add` | `{{add 1 2}}` | `3` | 加法 |
| `sub` | `{{sub 5 2}}` | `3` | 减法 |
| `mul` | `{{mul 3 4}}` | `12` | 乘法 |
| `div` | `{{div 10 2}}` | `5` | 除法 |
| `mod` | `{{mod 10 3}}` | `1` | 取模（余数） |
| `max` | `{{max 5 10}}` | `10` | 最大值 |
| `min` | `{{min 5 10}}` | `5` | 最小值 |
| `abs` | `{{abs -5}}` | `5` | 绝对值 |
| `pow` | `{{pow 2.0 3.0}}` | `8` | 幂运算 (2³) |
| `sqrt` | `{{sqrt 16.0}}` | `4` | 平方根 |
| `ceil` | `{{ceil 1.2}}` | `2` | 向上取整 |
| `floor` | `{{floor 1.8}}` | `1` | 向下取整 |
| `round` | `{{round 1.5}}` | `2` | 四舍五入 |

### 条件 & 类型转换

条件判断和类型转换工具函数

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `ternary` | `{{ternary true "yes" "no"}}` | `yes` | 三元运算符 |
| `default` | `{{default .value "default"}}` | 值或默认值 | 提供默认值 |
| `toString` | `{{toString 123}}` | `"123"` | 转换为字符串 |
| `toInt` | `{{toInt "123"}}` | `123` | 转换为整数 |
| `toFloat` | `{{toFloat "1.5"}}` | `1.5` | 转换为浮点数 |

### 组合函数

用于组合其他函数的辅助函数

| 函数 | 语法示例 | 输出示例 | 说明 |
|:----|:--------|:--------|:-----|
| `print` | `{{print "a" "b" "c"}}` | `abc` | 拼接多个字符串 |

---

## 数据提取器

从响应中提取数据并传递给后续请求，支持 JSONPath、Header、Regex 三种提取方式

### 支持的提取器类型

| 类型 | 提取目标 | 配置示例 | 引用语法 |
|:----|:--------|:--------|:--------|
| **JSONPath** | JSON响应字段 | `jsonpath: "$.data.id"` | `{{.api_name.var_name}}` |
| **Header** | HTTP响应头 | `header: "X-Token"` | `{{.api_name.var_name}}` |
| **Regex** | 正则匹配内容 | `regex: "session=([a-f0-9]+)"` | `{{.api_name.var_name}}` |

### 完整示例

```yaml
apis:
  - name: login
    path: /auth/login
    method: POST
    body: '{"username":"test","password":"123456"}'
    extractors:
      - name: token
        type: jsonpath
        jsonpath: "$.data.token"
        default: ""
      
      - name: user_id
        type: jsonpath
        jsonpath: "$.data.user_id"
        default: "0"

  - name: get_profile
    path: /users/{{.login.user_id}}/profile
    depends_on: [login]
    headers:
      Authorization: "Bearer {{.login.token}}"
```

---

## 高级用法

### 函数组合

多个函数可以嵌套组合使用：

```yaml
headers:
  # 签名场景：拼接后MD5
  X-Nonce: "{{randomString 22}}"
  X-Timestamp: "{{unix}}"
  X-Signature: "{{md5 (print .X-Nonce .X-Timestamp)}}"

body: |
  {
    # 唯一ID：时间戳+随机字符串的MD5
    "user_id": "{{md5 (print (unixNano) (randomString 16))}}",
    
    # 订单号：前缀+日期+随机数字
    "order_no": "ORD{{date \"20060102\"}}{{randomNumber 8}}",
    
    # UUID
    "request_id": "{{randomUUID}}"
  }
```

### 条件判断

```yaml
body: |
  {
    "status": "{{ternary (randomBool) \"active\" \"inactive\"}}",
    "value": "{{default .optionalValue \"default_value\"}}"
  }
```

### 数学计算

```yaml
body: |
  {
    "total": {{add (mul 10 5) 20}},
    "discount": {{round (mul (randomFloat 0.1 0.3) 100)}}
  }
```

## 相关文档

- [配置文件](CONFIG_FILE.md) - 完整配置选项
- [快速开始](GETTING_STARTED.md) - 实战案例
