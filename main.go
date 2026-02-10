/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-25 11:57:55
 * @FilePath: \go-stress\main.go
 * @Description: 压测工具主入口
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kamalyes/go-stress/bootstrap"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/logger"
	"github.com/kamalyes/go-stress/types"
)

// 导出常量 - 存储模式
const (
	ExportStorageModeMemory = types.StorageModeMemory
	ExportStorageModeSQLite = types.StorageModeSQLite
	ExportStorageModeBadger = types.StorageModeBadger
)

// 导出常量 - 运行模式
const (
	ExportRunModeStandaloneCLI  = types.RunModeStandaloneCLI
	ExportRunModeStandaloneFile = types.RunModeStandaloneFile
	ExportRunModeMaster         = types.RunModeMaster
	ExportRunModeSlave          = types.RunModeSlave
)

// 导出常量 - 协议类型
const (
	ExportProtocolHTTP      = types.ProtocolHTTP
	ExportProtocolGRPC      = types.ProtocolGRPC
	ExportProtocolWebSocket = types.ProtocolWebSocket
)

var (
	// 基础参数
	configFile  string
	curlFile    string
	protocol    string
	concurrency uint64
	requests    uint64
	url         string
	method      string
	timeout     time.Duration

	// HTTP参数
	http2     bool
	keepalive bool

	// gRPC参数
	grpcReflection bool
	grpcService    string
	grpcMethod     string

	// 其他
	body    string
	headers arrayFlags

	// 日志配置
	logLevel logger.LogLevelFlag
	logFile  string
	quiet    bool
	verbose  bool

	// 报告配置
	reportPrefix string            // 报告文件名前缀
	storageMode  types.StorageMode // 存储模式 (memory/db)
	noReport     bool              // 不生成报告文件
	noPrint      bool              // 不打印报告表格
	noWait       bool              // 压测结束即退出

	// 链路追踪
	noTraceHeader bool // 不注入 traceparent 请求头

	// 内存限制
	maxMemory string // 内存使用阈值

	// 分布式参数
	mode         types.RunMode // 运行模式: standalone/master/slave
	masterAddr   string        // Master 地址 (Slave 模式使用)
	slaveID      string        // Slave ID (Slave 模式使用)
	grpcPort     int           // gRPC 端口
	httpPort     int           // HTTP 端口 (Master 模式使用)
	realtimePort int           // 实时报告端口 (Slave 模式使用)
	region       string        // 节点区域标签

	// Slave 数量计算配置 (Master 模式)
	workersPerSlave int // 每个 Slave 承担的 Worker 数量
	minSlaveCount   int // 最小需要的 Slave 数量

	// Master 配置 (Master 模式)
	heartbeatInterval time.Duration // 心跳间隔
	heartbeatTimeout  time.Duration // 心跳超时
	maxFailures       int           // 最大失败次数
	tokenExpiration   time.Duration // Token 过期时间
	tokenIssuer       string        // Token 签发者
	masterSecret      string        // Master 密钥
)

// arrayFlags 数组flag
type arrayFlags []string

func (a *arrayFlags) String() string {
	return fmt.Sprintf("%v", *a)
}

func (a *arrayFlags) Set(value string) error {
	*a = append(*a, value)
	return nil
}

func init() {
	// 设置默认值
	storageMode = ExportStorageModeMemory
	mode = ExportRunModeStandaloneCLI

	// 基础参数
	flag.StringVar(&configFile, "config", "", "配置文件路径 (yaml/json)")
	flag.StringVar(&curlFile, "curl", "", "curl命令文件路径")
	flag.StringVar(&protocol, "protocol", "http", "协议类型 (http/grpc/websocket)")
	flag.Uint64Var(&concurrency, "c", 1, "并发数")
	flag.Uint64Var(&requests, "n", 1, "每个并发的请求数")
	flag.StringVar(&url, "url", "", "目标URL")
	flag.StringVar(&method, "method", "GET", "请求方法")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "请求超时时间")

	// HTTP参数
	flag.BoolVar(&http2, "http2", false, "使用HTTP/2")
	flag.BoolVar(&keepalive, "keepalive", false, "使用长连接")

	// gRPC参数
	flag.BoolVar(&grpcReflection, "grpc-reflection", false, "使用gRPC反射")
	flag.StringVar(&grpcService, "grpc-service", "", "gRPC服务名")
	flag.StringVar(&grpcMethod, "grpc-method", "", "gRPC方法名")

	// 其他
	flag.StringVar(&body, "data", "", "请求体数据")
	flag.Var(&headers, "H", "请求头 (可多次使用)")

	// 日志配置
	flag.Var(&logLevel, "log-level", "日志级别 (debug/info/warn/error)")
	flag.StringVar(&logFile, "log-file", "", "日志文件路径")
	flag.BoolVar(&quiet, "quiet", false, "静默模式（仅错误）")
	flag.BoolVar(&verbose, "verbose", false, "详细模式（包含调试信息）")

	// 报告配置
	flag.StringVar(&reportPrefix, "report-prefix", "stress-report", "报告文件名前缀")
	flag.Var(&storageMode, "storage", "存储模式 (memory:内存 | sqlite:SQLite数据库 | badger:BadgerDB高性能存储)")
	flag.BoolVar(&noReport, "no-report", false, "不生成报告文件")
	flag.BoolVar(&noPrint, "no-print", false, "不在终端打印报告表格")
	flag.BoolVar(&noWait, "no-wait", false, "压测结束即退出，不驻留实时报告服务器（CI/脚本场景）")
	flag.BoolVar(&noTraceHeader, "no-trace-header", false, "不注入 traceparent 请求头（默认注入，便于与被压服务的链路对齐）")

	// 内存限制
	flag.StringVar(&maxMemory, "max-memory", "", "内存使用阈值，超过后自动停止测试 (如: 1GB, 512MB, 2048KB)")

	// 分布式参数
	flag.Var(&mode, "mode", "运行模式 (standalone/master/slave)")
	flag.StringVar(&masterAddr, "master", "", "Master节点地址 (Slave模式必需, 如: localhost:9090)")
	flag.StringVar(&slaveID, "slave-id", "", "Slave节点ID (可选,不指定则自动生成)")
	flag.IntVar(&grpcPort, "grpc-port", 9090, "gRPC服务端口")
	flag.IntVar(&httpPort, "http-port", 8080, "HTTP服务端口 (Master模式)")
	flag.IntVar(&realtimePort, "realtime-port", 0, "实时报告服务器端口 (Slave模式, 0表示自动分配)")
	flag.StringVar(&region, "region", "default", "节点区域标签")

	// Slave 数量计算配置 (Master 模式)
	flag.IntVar(&workersPerSlave, "workers-per-slave", 100, "每个 Slave 承担的 Worker 数量 (默认100)")
	flag.IntVar(&minSlaveCount, "min-slave-count", 1, "最小需要的 Slave 数量 (默认1)")

	// Master 配置 (Master 模式)
	flag.DurationVar(&heartbeatInterval, "heartbeat-interval", 5*time.Second, "心跳间隔 (默认5s)")
	flag.DurationVar(&heartbeatTimeout, "heartbeat-timeout", 15*time.Second, "心跳超时 (默认15s)")
	flag.IntVar(&maxFailures, "max-failures", 3, "最大失败次数 (默认3)")
	flag.DurationVar(&tokenExpiration, "token-expiration", 24*time.Hour, "Token过期时间 (默认24h)")
	flag.StringVar(&tokenIssuer, "token-issuer", "go-stress-master", "Token签发者")
	flag.StringVar(&masterSecret, "master-secret", "go-stress-secret-key", "Master密钥")
}

func main() {
	flag.Parse()

	// 初始化日志器
	initLogger()

	// 日志默认走 go-logger 的异步批量输出器，队列中的条目不刷写就会随进程消失
	// 退出路径因此一律走 run 的 return，不再用 os.Exit 直接终止
	defer logger.Flush()

	run()
}

// run 执行主流程，正常结束时 return，以保证 main 中的日志刷写得以执行
func run() {
	// 处理子命令
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			printBanner()
			printSimpleUsage()
			return
		case "variables", "vars", "-vars":
			printBanner()
			printVariablesHelp()
			return
		case "examples", "demo", "-demo":
			printBanner()
			printExamplesHelp()
			return
		case "version", "-v", "--version":
			printVersion()
			return
		}
	}

	// 如果没有任何参数，显示简化帮助信息
	if len(os.Args) == 1 {
		printBanner()
		printSimpleUsage()
		return
	}

	// 打印banner
	printBanner()

	// 根据运行模式选择执行路径
	switch mode {
	case ExportRunModeMaster:
		runMasterMode()
	case ExportRunModeSlave:
		runSlaveMode()
	default:
		runStandaloneMode()
	}
}

// buildConfigFromFlags 从命令行参数构建配置
func buildConfigFromFlags() *config.Config {
	cfg := config.DefaultConfig()

	cfg.Protocol = types.ProtocolType(protocol)
	cfg.Concurrency = concurrency
	cfg.Requests = requests
	cfg.URL = url
	cfg.Method = method
	cfg.Timeout = timeout
	cfg.Body = body

	// 链路追踪开关（DefaultConfig 已给出 Advanced，此处只覆盖本项）
	if cfg.Advanced == nil {
		cfg.Advanced = &config.AdvancedConfig{}
	}
	cfg.Advanced.DisableTraceHeader = noTraceHeader

	// 解析Headers
	cfg.Headers = make(map[string]string)
	for _, h := range headers {
		parseHeader(h, cfg.Headers)
	}

	// HTTP配置
	if cfg.Protocol == ExportProtocolHTTP {
		cfg.HTTP = &config.HTTPConfig{
			HTTP2:           http2,
			KeepAlive:       keepalive,
			FollowRedirects: true,
			MaxConnsPerHost: 100,
		}
	}

	// gRPC配置
	if cfg.Protocol == ExportProtocolGRPC {
		cfg.GRPC = &config.GRPCConfig{
			UseReflection: grpcReflection,
			Service:       grpcService,
			Method:        grpcMethod,
			Metadata:      make(map[string]string),
		}
	}

	return cfg
}

// initLogger 初始化日志器
func initLogger() {
	log := logger.New()

	// 优先级：verbose > quiet > logLevel
	switch {
	case verbose:
		log = log.WithLevel(logger.DEBUG).WithShowCaller(true)
	case quiet:
		log = log.WithLevel(logger.ERROR)
	default:
		log = log.WithLevel(logLevel.Level)
	}

	// 配置输出：着色由 go-logger 按输出目标自动判定，落盘时无需再手动关闭
	if logFile != "" {
		log = log.WithOutput(logger.NewRotateWriter(logFile, 0, 0))
	}
	logger.SetDefault(log)
}

// parseHeader 解析请求头字符串
func parseHeader(header string, headers map[string]string) {
	for i := 0; i < len(header); i++ {
		if header[i] == ':' {
			key := header[:i]
			value := header[i+1:]
			// 去除前后空格
			for len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			headers[key] = value
			return
		}
	}
}

// printBanner 打印启动banner
// 用 fmt 而非 logger：banner 是程序界面不是日志事件，走异步日志队列会被推到
// 后续 fmt 输出之后（顺序错乱），还会套上时间戳与 [INFO] 前缀
func printBanner() {
	fmt.Print(`
╔══════════════════════════════════════════════════════════╗
║                                                          ║
║     ⚡ Go Stress Testing Tool ⚡                        ║
║                                                          ║
║     🚀 高性能压测工具                                    ║
║     🔧 支持 HTTP / gRPC / WebSocket                      ║
║     ⚙️  基于 go-toolbox 工具库                           ║
║                                                          ║
╚══════════════════════════════════════════════════════════╝
`)
}

// printVersion 打印版本信息
func printVersion() {
	fmt.Println("go-stress version 1.0.0")
	fmt.Println("高性能 HTTP/gRPC/WebSocket 压测工具")
}

// printSimpleUsage 打印简化的使用说明
func printSimpleUsage() {
	printHeader("使用方法:")
	flag.Usage()

	fmt.Println("\n常用子命令:")
	fmt.Println("  go-stress help          - 显示完整帮助信息")
	fmt.Println("  go-stress variables     - 显示所有可用变量函数")
	fmt.Println("  go-stress examples      - 显示详细使用示例")
	fmt.Println("  go-stress version       - 显示版本信息")

	fmt.Println("\n快速开始:")
	fmt.Println("  # HTTP压测")
	fmt.Println("  go-stress -url https://example.com -c 10 -n 100")
	fmt.Println("")
	fmt.Println("  # 使用配置文件")
	fmt.Println("  go-stress -config config.yaml")
	fmt.Println("")
	fmt.Println("  # Master模式（等待Slave连接，通过Web界面提交任务）")
	fmt.Println("  go-stress -mode master -http-port 8080 -grpc-port 9090")
	fmt.Println("")
	fmt.Println("  # Slave模式（连接到Master）")
	fmt.Println("  go-stress -mode slave -master master-ip:9090 -grpc-port 9091 -region default")

	fmt.Println("\n💡 提示: 使用 'go-stress variables' 查看所有参数化变量")
	fmt.Println("💡 提示: 使用 'go-stress examples' 查看详细示例")
}

// printVariablesHelp 打印变量功能帮助
func printVariablesHelp() {
	resolver := config.NewVariableResolver()

	printHeader("变量功能说明:")
	fmt.Println("  支持在 URL、请求体、请求头中使用变量，使用 {{variable}} 或 {{function}} 语法")
	fmt.Println("")

	printHeader("基本用法:")
	printVariableExamples(resolver)

	printHeader("所有可用变量函数:")
	printAvailableFunctions(resolver)

	fmt.Println("\n💡 详细文档: docs/VARIABLES.md")
}

// printExamplesHelp 打印示例帮助
func printExamplesHelp() {
	printHeader("基本示例:")
	printExamples()

	printHeader("配置文件示例 (config.yaml):")
	printConfigExample()

	fmt.Println("\n更多示例:")
	fmt.Println("  # 使用变量")
	fmt.Println("  go-stress -url 'https://api.example.com/user/{{seq}}' -c 10 -n 100")
	fmt.Println("")
	fmt.Println("  # 自定义请求头")
	fmt.Println("  go-stress -url https://api.example.com -H 'Authorization: Bearer token' -H 'X-Request-ID: {{randomUUID}}'")
	fmt.Println("")
	fmt.Println("  # 内存限制")
	fmt.Println("  go-stress -config config.yaml -max-memory 1GB")
	fmt.Println("")
	fmt.Println("  # 持久化存储")
	fmt.Println("  go-stress -config config.yaml -storage sqlite")
	fmt.Println("")
	fmt.Println("  # 分布式压测 (Master)")
	fmt.Println("  go-stress -mode master -http-port 8080 -grpc-port 9090 -config config.yaml")
	fmt.Println("")
	fmt.Println("  # 分布式压测 (Slave)")
	fmt.Println("  go-stress -mode slave -master localhost:9090 -region us-west")

	fmt.Println("\n💡 完整文档:")
	fmt.Println("  - 快速开始: docs/GETTING_STARTED.md")
	fmt.Println("  - 配置文件: docs/CONFIG_FILE.md")
	fmt.Println("  - 命令参考: docs/CLI_REFERENCE.md")
	fmt.Println("  - 分布式模式: docs/DISTRIBUTED_MODE.md")
	fmt.Println("  - 变量函数: docs/VARIABLES.md")
}

func printHeader(title string) {
	fmt.Println("\n" + title)
}

func printExamples() {
	examples := []string{
		"# 简单HTTP压测",
		"go-stress -url https://example.com -c 10 -n 100",
		"",
		"# POST请求",
		"go-stress -url https://api.example.com/users -method POST -data '{\"name\":\"test\"}' -H \"Content-Type: application/json\" -c 5 -n 50",
		"",
		"# 使用配置文件",
		"go-stress -config config.yaml",
		"",
		"# 使用curl文件",
		"go-stress -curl requests.txt -c 10 -n 100",
		"",
		"# 自定义报告前缀",
		"go-stress -url https://example.com -c 10 -n 100 -report-prefix my-test",
		"",
		"# gRPC压测",
		"go-stress -protocol grpc -url localhost:50051 -grpc-reflection -grpc-service myservice -grpc-method MyMethod -c 5 -n 50",
		"",
		"# WebSocket压测",
		"go-stress -protocol websocket -url ws://localhost:8080/ws -body '{\"action\":\"ping\"}' -c 10 -n 100",
		"",
		"# 实时监控",
		"运行后自动打开浏览器查看实时报告（默认端口: 8088，可通过配置文件的 realtime_port 修改）",
		"测试完成后生成静态HTML报告: stress-report-{时间戳}.html",
	}
	for _, example := range examples {
		fmt.Println(example)
	}
}

func printVariableExamples(resolver *config.VariableResolver) {
	seqExample, _ := resolver.Resolve("{{seq}}")
	unixExample, _ := resolver.Resolve("{{unix}}")

	fmt.Println("  支持在 URL、请求体、请求头中使用变量，使用 {{variable}} 或 {{function}} 语法")
	fmt.Println("  go-stress -url 'https://api.example.com/user/{{seq}}' -c 10 -n 100")
	fmt.Printf("    实际示例: https://api.example.com/user/%s\n", seqExample)
	fmt.Println("  go-stress -data '{\"timestamp\": {{unix}}, \"id\": {{seq}}}' ...")
	fmt.Printf("    实际示例: {\"timestamp\": %s, \"id\": %s}\n", unixExample, seqExample)

	printRandomExamples(resolver)
	printEnvironmentExamples(resolver)
}

func printRandomExamples(resolver *config.VariableResolver) {
	randomStr, _ := resolver.Resolve("{{randomString 8}}")
	randomInt, _ := resolver.Resolve("{{randomInt 18 60}}")
	randomUUID, _ := resolver.Resolve("{{randomUUID}}")

	fmt.Println("  # 随机值")
	fmt.Println("  go-stress -data '{\"username\": \"user_{{randomString 8}}\", \"age\": {{randomInt 18 60}}}' ...")
	fmt.Printf("    实际示例: {\"username\": \"user_%s\", \"age\": %s}\n", randomStr, randomInt)
	fmt.Println("  go-stress -H 'X-Request-ID: {{randomUUID}}' ...")
	fmt.Printf("    实际示例: X-Request-ID: %s\n", randomUUID)
}

func printEnvironmentExamples(resolver *config.VariableResolver) {
	hostname, _ := resolver.Resolve("{{hostname}}")
	dateExample, _ := resolver.Resolve("{{date \"2006-01-02 15:04:05\"}}")

	fmt.Println("  # 环境变量和其他")
	fmt.Println("  go-stress -H 'X-Hostname: {{hostname}}' ...")
	fmt.Printf("    实际示例: X-Hostname: %s\n", hostname)
	fmt.Println("  go-stress -data '{\"date\": \"{{date \"2006-01-02 15:04:05\"}}\"}' ...")
	fmt.Printf("    实际示例: {\"date\": \"%s\"}\n", dateExample)
}

func printAvailableFunctions(resolver *config.VariableResolver) {
	// 生成示例
	seqExample, _ := resolver.Resolve("{{seq}}")
	unixExample, _ := resolver.Resolve("{{unix}}")
	unixNano, _ := resolver.Resolve("{{unixNano}}")
	timestamp, _ := resolver.Resolve("{{timestamp}}")
	dateEx, _ := resolver.Resolve("{{date \"2006-01-02\"}}")

	randomInt, _ := resolver.Resolve("{{randomInt 1 100}}")
	randomFloat, _ := resolver.Resolve("{{randomFloat 0.0 1.0}}")
	randomStr, _ := resolver.Resolve("{{randomString 10}}")
	randomAlpha, _ := resolver.Resolve("{{randomAlpha 8}}")
	randomNum, _ := resolver.Resolve("{{randomNumber 6}}")
	uuidEx, _ := resolver.Resolve("{{randomUUID}}")

	emailEx, _ := resolver.Resolve("{{randomEmail}}")
	phoneEx, _ := resolver.Resolve("{{randomPhone}}")
	ipEx, _ := resolver.Resolve("{{randomIP}}")
	macEx, _ := resolver.Resolve("{{randomMAC}}")

	nameEx, _ := resolver.Resolve("{{randomName}}")
	cityEx, _ := resolver.Resolve("{{randomCity}}")
	countryEx, _ := resolver.Resolve("{{randomCountry}}")
	dateRandEx, _ := resolver.Resolve("{{randomDate}}")
	timeEx, _ := resolver.Resolve("{{randomTime}}")
	priceEx, _ := resolver.Resolve("{{randomPrice 10 100}}")

	hostname, _ := resolver.Resolve("{{hostname}}")
	localIP, _ := resolver.Resolve("{{localIP}}")

	md5Ex, _ := resolver.Resolve("{{md5 \"test\"}}")
	sha1Ex, _ := resolver.Resolve("{{sha1 \"test\"}}")
	sha256Ex, _ := resolver.Resolve("{{sha256 \"test\"}}")

	base64Ex, _ := resolver.Resolve("{{base64 \"hello\"}}")
	urlEncodeEx, _ := resolver.Resolve("{{urlEncode \"a b c\"}}")

	upperEx, _ := resolver.Resolve("{{upper \"hello\"}}")
	lowerEx, _ := resolver.Resolve("{{lower \"HELLO\"}}")
	trimEx, _ := resolver.Resolve("{{trim \" hi \"}}")
	replaceEx, _ := resolver.Resolve("{{replace \"hello\" \"l\" \"L\"}}")
	substrEx, _ := resolver.Resolve("{{substr \"hello\" 0 2}}")

	addEx, _ := resolver.Resolve("{{add 1 2}}")
	subMathEx, _ := resolver.Resolve("{{sub 5 2}}")
	mulEx, _ := resolver.Resolve("{{mul 3 4}}")
	divEx, _ := resolver.Resolve("{{div 10 2}}")
	maxEx, _ := resolver.Resolve("{{max 5 10}}")
	minEx, _ := resolver.Resolve("{{min 5 10}}")

	printEx, _ := resolver.Resolve("{{print \"a\" \"b\" \"c\"}}")
	combineEx, _ := resolver.Resolve("{{md5 (print (seq) (unix))}}")

	base64DecEx, _ := resolver.Resolve("{{base64Decode \"aGVsbG8=\"}}")
	urlDecEx, _ := resolver.Resolve("{{urlDecode \"a+b+c\"}}")
	hexEncEx, _ := resolver.Resolve("{{hexEncode \"hello\"}}")
	hexDecEx, _ := resolver.Resolve("{{hexDecode \"68656c6c6f\"}}")
	idCardEx, _ := resolver.Resolve("{{randomIDCard}}")
	boolEx, _ := resolver.Resolve("{{randomBool}}")

	fmt.Println("  环境变量 & 主机:")
	fmt.Println("    {{env \"VAR_NAME\"}}           - 获取环境变量")
	fmt.Printf("    {{hostname}}                  - 主机名 (示例: %s)\n", hostname)
	fmt.Printf("    {{localIP}}                   - 本机IP (示例: %s)\n", localIP)

	fmt.Println("\n  序列 & 时间:")
	fmt.Printf("    {{seq}}                       - 自增序列号 (示例: %s)\n", seqExample)
	fmt.Printf("    {{unix}}                      - Unix时间戳/秒 (示例: %s)\n", unixExample)
	fmt.Printf("    {{unixNano}}                  - Unix纳秒时间戳 (示例: %s)\n", unixNano)
	fmt.Printf("    {{timestamp}}                 - Unix毫秒时间戳 (示例: %s)\n", timestamp)
	fmt.Printf("    {{date \"2006-01-02\"}}         - 格式化日期 (示例: %s)\n", dateEx)

	fmt.Println("\n  随机-基础:")
	fmt.Printf("    {{randomInt 1 100}}           - 随机整数 (示例: %s)\n", randomInt)
	fmt.Printf("    {{randomFloat 0.0 1.0}}       - 随机浮点数 (示例: %s)\n", randomFloat)
	fmt.Printf("    {{randomString 10}}           - 随机字符串 (示例: %s)\n", randomStr)
	fmt.Printf("    {{randomAlpha 8}}             - 随机字母 (示例: %s)\n", randomAlpha)
	fmt.Printf("    {{randomNumber 6}}            - 随机数字 (示例: %s)\n", randomNum)
	fmt.Printf("    {{randomUUID}}                - UUID (示例: %s)\n", uuidEx)
	fmt.Printf("    {{randomBool}}                - 随机布尔值 (示例: %s)\n", boolEx)

	fmt.Println("\n  随机-格式化:")
	fmt.Printf("    {{randomEmail}}               - 随机邮箱 (示例: %s)\n", emailEx)
	fmt.Printf("    {{randomPhone}}               - 随机手机号 (示例: %s)\n", phoneEx)
	fmt.Printf("    {{randomIP}}                  - 随机IP地址 (示例: %s)\n", ipEx)
	fmt.Printf("    {{randomMAC}}                 - 随机MAC地址 (示例: %s)\n", macEx)

	fmt.Println("\n  随机-业务场景:")
	fmt.Printf("    {{randomName}}                - 随机姓名 (示例: %s)\n", nameEx)
	fmt.Printf("    {{randomCity}}                - 随机城市 (示例: %s)\n", cityEx)
	fmt.Printf("    {{randomCountry}}             - 随机国家 (示例: %s)\n", countryEx)
	fmt.Printf("    {{randomDate}}                - 随机日期 (示例: %s)\n", dateRandEx)
	fmt.Printf("    {{randomTime}}                - 随机时间 (示例: %s)\n", timeEx)
	fmt.Printf("    {{randomPrice 10 100}}        - 随机价格 (示例: %s)\n", priceEx)
	fmt.Printf("    {{randomIDCard}}              - 随机身份证号 (示例: %s)\n", idCardEx)

	fmt.Println("\n  加密/哈希:")
	fmt.Printf("    {{md5 \"text\"}}               - MD5 (示例: %s)\n", md5Ex)
	fmt.Printf("    {{sha1 \"text\"}}              - SHA1 (示例: %s...)\n", sha1Ex[:16])
	fmt.Printf("    {{sha256 \"text\"}}            - SHA256 (示例: %s...)\n", sha256Ex[:16])

	fmt.Println("\n  编码/解码:")
	fmt.Printf("    {{base64 \"hello\"}}           - Base64编码 (示例: %s)\n", base64Ex)
	fmt.Printf("    {{base64Decode \"aGVsbG8=\"}}  - Base64解码 (示例: %s)\n", base64DecEx)
	fmt.Printf("    {{urlEncode \"a b c\"}}        - URL编码 (示例: %s)\n", urlEncodeEx)
	fmt.Printf("    {{urlDecode \"a+b+c\"}}        - URL解码 (示例: %s)\n", urlDecEx)
	fmt.Printf("    {{hexEncode \"hello\"}}        - 十六进制编码 (示例: %s)\n", hexEncEx)
	fmt.Printf("    {{hexDecode \"68656c6c6f\"}}   - 十六进制解码 (示例: %s)\n", hexDecEx)

	fmt.Println("\n  字符串操作:")
	fmt.Printf("    {{upper \"hello\"}}            - 转大写 (示例: %s)\n", upperEx)
	fmt.Printf("    {{lower \"HELLO\"}}            - 转小写 (示例: %s)\n", lowerEx)
	fmt.Printf("    {{trim \" hi \"}}              - 去除空格 (示例: %s)\n", trimEx)
	fmt.Printf("    {{replace \"hello\" \"l\" \"L\"}} - 字符串替换 (示例: %s)\n", replaceEx)
	fmt.Printf("    {{substr \"hello\" 0 2}}       - 截取子串 (示例: %s)\n", substrEx)

	fmt.Println("\n  数学运算:")
	fmt.Printf("    {{add 1 2}}                   - 加法 (示例: %s)\n", addEx)
	fmt.Printf("    {{sub 5 2}}                   - 减法 (示例: %s)\n", subMathEx)
	fmt.Printf("    {{mul 3 4}}                   - 乘法 (示例: %s)\n", mulEx)
	fmt.Printf("    {{div 10 2}}                  - 除法 (示例: %s)\n", divEx)
	fmt.Printf("    {{max 5 10}}                  - 最大值 (示例: %s)\n", maxEx)
	fmt.Printf("    {{min 5 10}}                  - 最小值 (示例: %s)\n", minEx)

	fmt.Println("\n  组合函数:")
	fmt.Printf("    {{print \"a\" \"b\" \"c\"}}       - 拼接字符串 (示例: %s)\n", printEx)
	fmt.Printf("    {{md5 (print (seq) (unix))}}  - 组合使用 (示例: %s)\n", combineEx)

	fmt.Println("\n  💡 更多函数请参考文档: docs/VARIABLES.md")
}

func printConfigExample() {
	fmt.Println("protocol: http")
	fmt.Println("url: https://api.example.com/users")
	fmt.Println("method: POST")
	fmt.Println("concurrency: 10")
	fmt.Println("requests: 100")
	fmt.Println("headers:")
	fmt.Println("  Content-Type: application/json")
	fmt.Println("  X-Request-ID: \"{{randomUUID}}\"")
	fmt.Println("  X-Trace-ID: \"{{md5 (print (seq) (timestamp))}}\"")
	fmt.Println("  Authorization: \"Bearer {{env \"API_TOKEN\"}}\"")
	fmt.Println("body: |")
	fmt.Println("  {")
	fmt.Println("    \"id\": {{seq}},")
	fmt.Println("    \"username\": \"user_{{randomString 8}}\",")
	fmt.Println("    \"email\": \"{{randomEmail}}\",")
	fmt.Println("    \"phone\": \"{{randomPhone}}\",")
	fmt.Println("    \"timestamp\": {{timestamp}},")
	fmt.Println("    \"client_ip\": \"{{randomIP}}\",")
	fmt.Println("    \"token\": \"{{base64 (randomString 16)}}\"")
	fmt.Println("  }")
}

// runMasterMode 运行 Master 模式
func runMasterMode() {
	// 判断是否有任务配置
	opts := bootstrap.MasterOptions{
		GRPCPort:          grpcPort,
		HTTPPort:          httpPort,
		Logger:            logger.Default,
		WorkersPerSlave:   workersPerSlave,
		MinSlaveCount:     minSlaveCount,
		HeartbeatInterval: heartbeatInterval,
		HeartbeatTimeout:  heartbeatTimeout,
		MaxFailures:       maxFailures,
		TokenExpiration:   tokenExpiration,
		TokenIssuer:       tokenIssuer,
		Secret:            masterSecret,
	}

	if err := bootstrap.RunMaster(opts); err != nil {
		logger.Default.Fatalf("❌ 运行 Master 失败: %v", err)
	}
}

// runSlaveMode 运行 Slave 模式
func runSlaveMode() {
	opts := bootstrap.SlaveOptions{
		SlaveID:        slaveID,
		MasterAddr:     masterAddr,
		GRPCPort:       grpcPort,
		RealtimePort:   realtimePort,
		Region:         region,
		MaxConcurrency: 5,
		CanReuse:       true,
		Logger:         logger.Default,
	}
	if err := bootstrap.RunSlave(opts); err != nil {
		logger.Default.Fatalf("❌ 运行 Slave 失败: %v", err)
	}
}

// runStandaloneMode 运行独立模式
func runStandaloneMode() {
	opts := bootstrap.StandaloneOptions{
		ConfigFile:   configFile,
		CurlFile:     curlFile,
		Concurrency:  concurrency,
		Requests:     requests,
		Timeout:      timeout,
		StorageMode:  storageMode,
		ReportPrefix: reportPrefix,
		MaxMemory:    maxMemory,
		Logger:       logger.Default,
		ConfigFunc:   buildConfigFromFlags,
		NoReport:     noReport,
		NoPrint:      noPrint,
		NoWait:       noWait,
	}
	if err := bootstrap.RunStandalone(opts); err != nil {
		logger.Default.Fatalf("❌ 运行 Standalone 失败: %v", err)
	}
}
