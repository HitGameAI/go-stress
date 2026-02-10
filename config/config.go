/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-20 12:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 13:57:15
 * @FilePath: \go-stress\config\config.go
 * @Description: 配置管理模块
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */

package config

import (
	"time"

	"github.com/kamalyes/go-logger"
)

// Config 压测配置
type Config struct {
	// 基础配置
	Protocol    ProtocolType  `json:"protocol" yaml:"protocol"`       // 协议类型
	Concurrency uint64        `json:"concurrency" yaml:"concurrency"` // 并发数
	Requests    uint64        `json:"requests" yaml:"requests"`       // 每个并发的请求数
	Duration    time.Duration `json:"duration" yaml:"duration"`       // 压测持续时间(优先级高于requests)
	Timeout     time.Duration `json:"timeout" yaml:"timeout"`         // 单个请求超时

	// 请求配置（作为公共配置，可被APIs覆盖）
	Host    string            `json:"host,omitempty" yaml:"host,omitempty"` // 公共Host（如：https://api.example.com）
	URL     string            `json:"url,omitempty" yaml:"url,omitempty"`   // 完整URL（向后兼容，优先级低于Host+Path）
	Method  string            `json:"method,omitempty" yaml:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body    string            `json:"body,omitempty" yaml:"body,omitempty"`

	// 多API配置（如果定义了APIs，则URL等字段作为公共配置）
	APIs []APIConfig `json:"apis,omitempty" yaml:"apis,omitempty"`

	// 思考时间（全局默认，可被单个 API 覆盖）
	ThinkTime *ThinkTimeConfig `json:"think_time,omitempty" yaml:"think_time,omitempty"`

	// CSV 数据驱动参数源（每条请求序列取走一行，变量以 {{.列名}} 引用）
	CSVData *CSVDataConfig `json:"csv_data,omitempty" yaml:"csv_data,omitempty"`

	// 变量配置
	Variables map[string]any `json:"variables,omitempty" yaml:"variables,omitempty"` // 静态变量
	// 动态变量解析器（运行时注入，不序列化）
	VarResolver *VariableResolver `json:"-" yaml:"-"`

	// HTTP 特定配置
	HTTP *HTTPConfig `json:"http,omitempty" yaml:"http,omitempty"`

	// gRPC 特定配置
	GRPC *GRPCConfig `json:"grpc,omitempty" yaml:"grpc,omitempty"`

	// WebSocket 特定配置
	WebSocket *WebSocketConfig `json:"websocket,omitempty" yaml:"websocket,omitempty"`

	// 高级配置
	Advanced *AdvancedConfig `json:"advanced,omitempty" yaml:"advanced,omitempty"`

	// 验证配置
	Verify *VerifyConfig `json:"verify,omitempty" yaml:"verify,omitempty"`

	// 运行模式标识（用于报告展示）
	RunMode RunMode `json:"run_mode,omitempty" yaml:"run_mode,omitempty"`

	logger logger.ILogger `json:"-" yaml:"-"` // 日志记录器（运行时注入，不序列化）
}

// GetLogger 获取日志器（访问私有字段的方法）
func (c *Config) GetLogger() logger.ILogger {
	return c.logger
}

// SetLogger 设置日志器
func (c *Config) SetLogger(log logger.ILogger) {
	c.logger = log
}

// APIConfig 单个API配置（可继承公共配置）
type APIConfig struct {
	Name       string            `json:"name,omitempty" yaml:"name,omitempty"`             // API名称（可选）
	Host       string            `json:"host,omitempty" yaml:"host,omitempty"`             // Host（可选，继承自公共配置）
	Path       string            `json:"path,omitempty" yaml:"path,omitempty"`             // Path（如：/api/users）
	URL        string            `json:"url,omitempty" yaml:"url,omitempty"`               // 完整URL（可选，优先级高于Host+Path）
	Method     string            `json:"method,omitempty" yaml:"method,omitempty"`         // 可选，继承自公共配置
	Headers    map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`       // 可选，与公共配置合并
	Body       string            `json:"body,omitempty" yaml:"body,omitempty"`             // 可选，继承自公共配置
	Weight     int               `json:"weight,omitempty" yaml:"weight,omitempty"`         // 权重（用于负载分配，默认1）
	Repeat     int               `json:"repeat,omitempty" yaml:"repeat,omitempty"`         // 重复执行次数（默认1）
	Verify     []VerifyConfig    `json:"verify,omitempty" yaml:"verify,omitempty"`         // 可选，覆盖公共验证配置，支持多个验证规则
	DependsOn  []string          `json:"depends_on,omitempty" yaml:"depends_on,omitempty"` // 依赖的API名称列表
	Extractors []ExtractorConfig `json:"extractors,omitempty" yaml:"extractors,omitempty"` // 响应数据提取器

	// ThinkTime 本 API 执行后的思考时间，覆盖全局配置
	// JMeter 的 Timer 既可挂在测试计划下也可挂在单个采样器下，故两级都要有
	ThinkTime *ThinkTimeConfig `json:"think_time,omitempty" yaml:"think_time,omitempty"`
}

// ExtractorConfig 数据提取器配置
type ExtractorConfig struct {
	Name       string            `json:"name" yaml:"name"`                                 // 提取变量的名称
	Source     ExtractorSource   `json:"source,omitempty" yaml:"source,omitempty"`         // 提取源：response(默认) | request
	Type       ExtractorType     `json:"type,omitempty" yaml:"type,omitempty"`             // 提取类型：jsonpath(默认), regex, header, expression
	JSONPath   string            `json:"jsonpath,omitempty" yaml:"jsonpath,omitempty"`     // JSONPath表达式（如：$.data.token）
	Regex      string            `json:"regex,omitempty" yaml:"regex,omitempty"`           // 正则表达式
	Header     string            `json:"header,omitempty" yaml:"header,omitempty"`         // 响应头名称
	Expression string            `json:"expression,omitempty" yaml:"expression,omitempty"` // 表达式（如：{{.first_name}} {{.last_name}}）
	Transforms []TransformConfig `json:"transforms,omitempty" yaml:"transforms,omitempty"` // 数据转换管道
	Default    string            `json:"default,omitempty" yaml:"default,omitempty"`       // 默认值（提取失败时使用）
}

// ExtractorSource 提取源
type ExtractorSource string

const (
	ExtractorSourceResponse ExtractorSource = "response" // 从响应提取（默认）
	ExtractorSourceRequest  ExtractorSource = "request"  // 从请求提取
)

// TransformConfig 数据转换配置（复用 VariableResolver 的函数系统）
type TransformConfig struct {
	Function string        `json:"function" yaml:"function"`                     // 函数名（如：trim, upper, md5, date "2006-01-02"）
	Args     []interface{} `json:"args,omitempty" yaml:"args,omitempty"`         // 函数参数（可选）
	Template string        `json:"template,omitempty" yaml:"template,omitempty"` // 模板表达式（如：{{upper .value}}，用于复杂转换）
}

// HTTPConfig HTTP协议配置
// DefaultMaxConnsPerHost 每个 host 的默认最大连接数
const DefaultMaxConnsPerHost = 100

type HTTPConfig struct {
	HTTP2           bool `json:"http2" yaml:"http2"`                           // 是否使用HTTP/2
	KeepAlive       bool `json:"keepalive" yaml:"keepalive"`                   // 是否保持连接
	FollowRedirects bool `json:"follow_redirects" yaml:"follow_redirects"`     // 是否跟随重定向
	MaxConnsPerHost int  `json:"max_conns_per_host" yaml:"max_conns_per_host"` // 每个host的最大连接数
}

// GRPCConfig gRPC协议配置
type GRPCConfig struct {
	UseReflection bool              `json:"use_reflection" yaml:"use_reflection"` // 是否使用反射
	Service       string            `json:"service" yaml:"service"`               // 服务名
	Method        string            `json:"method" yaml:"method"`                 // 方法名
	ProtoFile     string            `json:"proto_file" yaml:"proto_file"`         // proto文件路径
	Metadata      map[string]string `json:"metadata" yaml:"metadata"`             // gRPC metadata
	TLS           *TLSConfig        `json:"tls,omitempty" yaml:"tls,omitempty"`   // TLS配置
}

// WebSocketConfig WebSocket协议配置
type WebSocketConfig struct {
	PingInterval time.Duration `json:"ping_interval" yaml:"ping_interval"` // ping间隔
	PingTimeout  time.Duration `json:"ping_timeout" yaml:"ping_timeout"`   // ping超时
}

// TLSConfig TLS配置
type TLSConfig struct {
	Enabled            bool   `json:"enabled" yaml:"enabled"`
	CertFile           string `json:"cert_file" yaml:"cert_file"`
	KeyFile            string `json:"key_file" yaml:"key_file"`
	CAFile             string `json:"ca_file" yaml:"ca_file"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
}

// AdvancedConfig 高级配置
type AdvancedConfig struct {
	EnableBreaker bool          `json:"enable_breaker" yaml:"enable_breaker"` // 启用熔断器
	MaxFailures   int32         `json:"max_failures" yaml:"max_failures"`     // 熔断器最大失败次数
	ResetTimeout  time.Duration `json:"reset_timeout" yaml:"reset_timeout"`   // 熔断器重置超时

	EnableRetry   bool          `json:"enable_retry" yaml:"enable_retry"`     // 启用重试
	MaxRetries    int           `json:"max_retries" yaml:"max_retries"`       // 最大重试次数
	RetryInterval time.Duration `json:"retry_interval" yaml:"retry_interval"` // 重试间隔

	RampUp       time.Duration `json:"ramp_up" yaml:"ramp_up"`             // 渐进式启动时间
	RealtimePort int           `json:"realtime_port" yaml:"realtime_port"` // 实时报告服务器端口（默认8088）

	// TargetQPS 目标速率（每秒请求数），0 表示不限速按全速发压
	//
	// 定速压测与全速压测回答的是不同问题：全速测的是系统上限，定速测的是
	// 「在给定到达率下延迟与错误率如何」。后者才是容量规划与 SLO 验证要的
	TargetQPS float64 `json:"target_qps,omitempty" yaml:"target_qps,omitempty"`

	// DisableTraceHeader 关闭 traceparent 请求头的注入
	//
	// 取反向命名，使配置省略时即为"启用"：trace 头是定位问题的主要线索，默认该开
	// 需要逐字节复现原始请求（如签名校验把全部头纳入计算）时才关掉
	DisableTraceHeader bool `json:"disable_trace_header,omitempty" yaml:"disable_trace_header,omitempty"`
}

// VerifyConfig 验证配置
type VerifyConfig struct {
	Type              VerifyType     `json:"type" yaml:"type"`                                                   // 验证类型: status, jsonpath, contains, custom
	JSONPath          string         `json:"jsonpath,omitempty" yaml:"jsonpath,omitempty"`                       // JSON路径表达式（仅type=jsonpath时使用）
	Custom            string         `json:"custom,omitempty" yaml:"custom,omitempty"`                           // 自定义验证器名称（仅type=custom时使用）
	Operator          ExpectOperator `json:"operator,omitempty" yaml:"operator,omitempty"`                       // 比较操作符: eq, ne, gt, gte, lt, lte, contains, regex等
	Expect            interface{}    `json:"expect" yaml:"expect"`                                               // 期望值（通用字段，所有类型都使用此字段）
	Regex             bool           `json:"regex,omitempty" yaml:"regex,omitempty"`                             // 是否使用正则表达式匹配（与operator=regex等效）
	Description       string         `json:"description,omitempty" yaml:"description,omitempty"`                 // 验证描述信息
	ContinueOnFailure bool           `json:"continue_on_failure,omitempty" yaml:"continue_on_failure,omitempty"` // 验证失败时是否继续执行（不阻断后续API）
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Protocol:    ProtocolHTTP,
		Concurrency: 1,
		Requests:    1,
		Timeout:     30 * time.Second,
		Method:      "GET",
		Headers:     make(map[string]string),
		Variables:   make(map[string]any),
		VarResolver: NewVariableResolver(), // 初始化变量解析器
		HTTP: &HTTPConfig{
			HTTP2: false,
			// 默认开长连接：这本就是此前的实际行为（两个预设客户端都复用连接），
			// 只是 KeepAlive 字段没被读到。既然现在它真的生效了，默认值必须保持
			// 原有行为，否则所有未显式配置的用户会突然退化成每请求一条新连接
			KeepAlive:       true,
			FollowRedirects: true,
			MaxConnsPerHost: DefaultMaxConnsPerHost,
		},
		GRPC: &GRPCConfig{
			UseReflection: false,
			Metadata:      make(map[string]string),
		},
		WebSocket: &WebSocketConfig{
			PingInterval: 30 * time.Second,
			PingTimeout:  10 * time.Second,
		},
		Advanced: &AdvancedConfig{
			EnableBreaker: false,
			MaxFailures:   5,
			ResetTimeout:  30 * time.Second,
			EnableRetry:   false,
			MaxRetries:    3,
			RetryInterval: 1 * time.Second,
		},
		Verify: &VerifyConfig{
			Type:   VerifyTypeStatusCode,
			Expect: 200,
		},
	}
}
