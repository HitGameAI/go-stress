/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 13:25:08
 * @FilePath: \go-stress\executor\executor.go
 * @Description: 压测执行器 - 核心编排器
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/protocol"
	"github.com/kamalyes/go-stress/statistics"
	"github.com/kamalyes/go-stress/storage"
	"github.com/kamalyes/go-toolbox/pkg/breaker"
	"github.com/kamalyes/go-toolbox/pkg/retry"
)

// StatsReporter 统计上报接口（用于分布式模式）
type StatsReporter interface {
	Add(result *RequestResult)
	SetTaskID(taskID string)
}

// Executor 压测执行器（核心编排器）
// 职责：
// 1. 组装各个组件（连接池、中间件、调度器）
// 2. 编排整体压测流程
// 3. 生成最终报告
type Executor struct {
	config         *config.Config
	collector      *statistics.Collector
	scheduler      *Scheduler
	pool           *ClientPool
	realtimeServer *statistics.RealtimeServer
	logger         logger.ILogger
	// 分布式相关
	statsReporter StatsReporter // 用于分布式模式下的统计上报
	isDistributed bool          // 是否为分布式模式
}

// NewExecutor 根据存储模式创建执行器（使用存储工厂）
func NewExecutor(cfg *config.Config, storageMode StorageMode, storagePath string, log logger.ILogger) (*Executor, error) {
	// 先创建 Executor 实例
	e := &Executor{
		config:        cfg,
		logger:        log,
		isDistributed: false,
	}

	// 使用存储工厂创建存储
	factory := storage.NewStorageFactory(e.logger)

	storageConfig := &storage.StorageConfig{
		Type:   storageMode,
		Path:   storagePath,
		NodeID: "local",
	}

	strg, err := factory.CreateStorage(storageConfig)
	if err != nil {
		// 不静默降级为内存模式
		//
		// 存储模式是使用者显式选的，选 sqlite/badger 就是为了事后能查明细。
		// 原先失败即降级并继续，于是「要了持久化、拿到一次成功的压测、
		// 数据一条没落盘」，退出码还是 0。典型触发场景是 CGO_ENABLED=0 构建的
		// 二进制用 -storage sqlite：go-sqlite3 退化为 stub，建表必然失败
		return nil, fmt.Errorf("创建 %s 存储失败: %w（需要内存模式请显式指定 -storage memory；"+
			"sqlite 依赖 cgo，若二进制以 CGO_ENABLED=0 构建请改用 badger）", storageMode, err)
	}

	// 创建 Collector
	e.collector = statistics.NewCollector(strg, e.logger)

	// 设置运行模式
	e.collector.SetRunMode(e.config.RunMode)

	// 设置配置信息（用于报告显示）
	e.collector.SetConfig(
		string(e.config.Protocol),
		e.config.Concurrency,
		e.config.Requests,
	)

	// 1. 创建客户端工厂
	clientFactory := e.createClientFactory()

	// 2. 创建连接池
	e.pool = NewClientPool(clientFactory, int(e.config.Concurrency))

	// 3. 构建中间件链
	handler, err := e.buildMiddlewareChain(clientFactory)
	if err != nil {
		return nil, fmt.Errorf("构建中间件链失败: %w", err)
	}

	// 4. 创建API选择器（统一处理：CreateAPISelector 内部会判断单/多API）
	apiSelector := CreateAPISelector(e.config)

	apiCount := len(e.config.APIs)
	if apiCount == 0 {
		apiCount = 1 // 单API模式
	}
	e.logger.Info("📋 API配置: %d个", apiCount)

	// 5. 载入 CSV 数据源（整份读入，热路径上不做 IO）
	var csvSource *CSVSource
	if e.config.CSVData != nil {
		data, err := config.LoadCSVData(e.config.CSVData)
		if err != nil {
			return nil, fmt.Errorf("载入 CSV 数据源失败: %w", err)
		}
		csvSource = NewCSVSource(data)
		e.logger.InfoKV("📑 CSV 数据源已载入",
			"path", e.config.CSVData.Path,
			"rows", csvSource.RowCount(),
			"variables", csvSource.Variables(),
			"sharing", string(csvSource.Sharing()),
			"recycle", csvSource.Recycle())
	}

	// 6. 创建速率限流器（目标速率为 0 时返回 nil，热路径上零成本）
	var limiter *RateLimiter
	if e.config.Advanced != nil && e.config.Advanced.TargetQPS > 0 {
		limiter = NewRateLimiter(e.config.Advanced.TargetQPS)
		e.logger.InfoKV("🚦 定速压测已启用", "target_qps", limiter.QPS())
	}

	// 7. 创建调度器
	var rampUp time.Duration
	if e.config.Advanced != nil {
		rampUp = e.config.Advanced.RampUp
	}

	// 直接从 config 取变量解析器
	e.scheduler = NewScheduler(SchedulerConfig{
		WorkerCount:       e.config.Concurrency,
		RequestPerWorker:  e.config.Requests,
		RampUpDuration:    rampUp,
		ClientPool:        e.pool,
		Handler:           handler,
		Collector:         e.collector,
		APISelector:       apiSelector,
		VarResolver:       e.config.VarResolver,
		Controller:        nil, // 稍后设置
		Logger:            e.logger,
		InjectTraceHeader: e.config.Advanced == nil || !e.config.Advanced.DisableTraceHeader,

		ThinkTime: e.config.ThinkTime,
		CSVSource: csvSource,
		Limiter:   limiter,
	})

	return e, nil
}

// createClientFactory 创建客户端工厂
func (e *Executor) createClientFactory() ClientFactory {
	return func() (Client, error) {
		// Debug 而非 Info：本闭包每个 worker 各调一次，-c 1000 时会刷出一千行同样的内容
		e.logger.Debugf("创建客户端: protocol=%s (type=%T)", e.config.Protocol, e.config.Protocol)
		switch e.config.Protocol {
		case ProtocolHTTP:
			return protocol.NewHTTPClient(e.config)
		case ProtocolGRPC:
			return protocol.NewGRPCClient(e.config)
		case ProtocolWebSocket:
			return protocol.NewWebSocketClient(e.config)
		default:
			return nil, fmt.Errorf("不支持的协议: %s (type=%T, raw=%q)", e.config.Protocol, e.config.Protocol, string(e.config.Protocol))
		}
	}
}

// buildMiddlewareChain 构建中间件链
// 执行顺序：熔断器 -> 重试器 -> 验证器 -> 客户端
func (e *Executor) buildMiddlewareChain(factory ClientFactory) (RequestHandler, error) {
	// 创建临时客户端用于中间件
	client, err := factory()
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %w", err)
	}

	chain := NewMiddlewareChain()

	// 1. 熔断器中间件（最外层）
	if e.config.Advanced != nil && e.config.Advanced.EnableBreaker {
		circuit := breaker.New("stress-test", breaker.Config{
			MaxFailures:       e.config.Advanced.MaxFailures,
			ResetTimeout:      e.config.Advanced.ResetTimeout,
			HalfOpenSuccesses: 2,
		})
		chain.Use(BreakerMiddleware(circuit))
	}

	// 2. 重试中间件
	if e.config.Advanced != nil && e.config.Advanced.EnableRetry {
		retrier := retry.NewRunner[error]()
		chain.Use(RetryMiddleware(retrier))
	}

	// 3. 构建处理器（客户端是最底层）
	//
	// 这里不再装验证中间件：验证统一由 Worker 执行
	//
	// 此前两条路径并存，而 DefaultConfig 总会给出一条顶层 verify（STATUS_CODE=200），
	// 于是 config.Verify 从不为 nil、中间件总被安装；同时 loader 又把这条顶层配置
	// 复制进每个未自带 verify 的 API，Worker 再跑一遍。结果每个请求被验证两次，
	// 报告里出现两条一模一样的验证记录 —— 声明 1 条却显示 2 条
	//
	// 选 Worker 而非中间件作为唯一入口，是因为只有它拿得到：
	//   - 每个 API 各自的多条验证规则（中间件只有那一条全局配置）
	//   - 期望值里的变量替换结果（如 expect: "{{.token}}"）
	// 单 URL 模式也不会丢验证：CreateAPISelector 会把顶层 verify 带进合成的那个 API
	//
	// 代价：验证失败不再触发重试与熔断。这对压测更正确 ——
	// 因断言不通过而重试会多发请求，报告里的请求数便不再是你要求的那个数
	handler := chain.Build(ClientMiddleware(client))

	return handler, nil
}

// Run 执行压测
func (e *Executor) Run(ctx context.Context) (*statistics.Report, error) {
	// 打印启动信息
	e.printStartInfo()

	// 启动实时报告服务器
	e.startRealtimeServer()

	// 按时长压测：到点即取消 ctx，Worker 据此停止
	//
	// duration 字段此前只有校验与分布式协议读过，独立模式的执行器从不理它 ——
	// 配置文件里写 duration: 3s 会被完全忽略，跑的是默认的「每并发 1 个请求」
	if e.config.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.config.Duration)
		defer cancel()
	}

	startTime := time.Now()

	// 运行调度器
	err := e.scheduler.Run(ctx)

	totalDuration := time.Since(startTime)

	// 标记测试完成（固定 QPS 计算时间）
	if e.realtimeServer != nil {
		e.realtimeServer.MarkCompleted()
	}

	// 清理资源
	e.pool.Close()

	// 生成报告（即使出错也要生成）- 使用 ReportBuilder
	builder := statistics.NewReportBuilder(e.collector)
	report := builder.BuildSummary(totalDuration)

	// 按时长压测到点结束：这是正常完成而非失败
	//
	// Worker 在 ctx 结束时返回 ctx.Err()，于是按时长跑完必然得到
	// DeadlineExceeded。此前它落进「其他错误」分支，使一次正常的定时压测
	// 以 FATAL 收场，报告也不生成
	if err != nil && e.config.Duration > 0 && errors.Is(err, context.DeadlineExceeded) {
		e.logger.Infof("⏱️  已达到设定时长 %s，压测结束", e.config.Duration)
		err = nil
	}

	// 检查是否因为context取消而中断
	if err != nil {
		// 用户主动取消：不关闭实时服务器，返回当前报告
		if errors.Is(err, context.Canceled) {
			e.logger.Warn("⚠️  压测已被用户中断")
			e.logger.Info("📊 正在保存当前统计数据...")
			return report, fmt.Errorf("执行压测失败: %w", err)
		}
		// 其他错误，关闭服务器
		if e.realtimeServer != nil {
			e.realtimeServer.Stop()
		}
		return nil, fmt.Errorf("执行压测失败: %w", err)
	}

	e.logger.Info("✅ 压测完成!")
	// 只在服务器真的在跑时才这么说：它可能被 advanced.disable_realtime 关掉
	// 或启动失败，而无条件宣称「继续运行」会与前一行的关闭日志自相矛盾
	if e.realtimeServer != nil {
		e.logger.Info("📊 实时报告服务器继续运行，按 Ctrl+C 可停止并退出")
	}
	return report, nil
}

// printStartInfo 打印启动信息
func (e *Executor) printStartInfo() {
	e.logger.Info("🚀 开始压测...")
	e.logger.Info("📊 协议: %s", e.config.Protocol)
	e.logger.Info("🔢 并发数: %d", e.config.Concurrency)
	e.logger.Info("📈 每并发请求数: %d", e.config.Requests)
	e.logger.Info("⏱️  超时时间: %v", e.config.Timeout)
	if e.config.Advanced != nil && e.config.Advanced.RampUp > 0 {
		e.logger.Info("⏲️  渐进启动: %v", e.config.Advanced.RampUp)
	}
	e.logger.Info("")
}

// GetCollector 获取统计收集器
func (e *Executor) GetCollector() *statistics.Collector {
	return e.collector
}

// ReplaceCollector 替换 Collector（用于分布式模式重用 Collector）
func (e *Executor) ReplaceCollector(collector *statistics.Collector) {
	e.collector = collector
	// 更新 Scheduler 的 Collector
	if e.scheduler != nil {
		e.scheduler.collector = collector
	}
}

// GetRealtimeServer 获取实时报告服务器
func (e *Executor) GetRealtimeServer() *statistics.RealtimeServer {
	return e.realtimeServer
}

// SetStatsReporter 设置统计上报器（用于分布式模式）
func (e *Executor) SetStatsReporter(reporter StatsReporter) {
	e.statsReporter = reporter
	e.isDistributed = true
	// 在分布式模式下，同时将结果发送到本地收集器和远程上报器
	if reporter != nil {
		e.collector.SetExternalReporter(func(result *RequestResult) {
			reporter.Add(result)
		})
	}
}

// IsDistributed 是否为分布式模式
func (e *Executor) IsDistributed() bool {
	return e.isDistributed
}

// startRealtimeServer 启动实时报告服务器
//
// advanced.disable_realtime 为真时整段跳过：CI 与批量跑并不需要它占端口，
// 而此前它总是启动且无从关闭，示例配置里的 realtime.enabled 写了也不生效
func (e *Executor) startRealtimeServer() {
	if e.config.Advanced != nil && e.config.Advanced.DisableRealtime {
		e.logger.Debug("实时报告服务器已按配置关闭")
		return
	}

	port := 8088 // 默认端口
	if e.config.Advanced != nil && e.config.Advanced.RealtimePort > 0 {
		port = e.config.Advanced.RealtimePort
	}

	server := statistics.NewRealtimeServer(e.collector, port, e.logger)
	if err := server.Start(); err != nil {
		e.logger.Warnf("⚠️  启动实时报告服务器失败: %v", err)
		// 启动失败时不保留引用，避免后续误操作
		e.realtimeServer = nil
		return
	}

	e.realtimeServer = server
	// 将 RealtimeServer 设置为控制器
	e.scheduler.controller = server

	realtimeURL := fmt.Sprintf("http://localhost:%d", port)
	e.logger.Info("🌐 实时监控地址: %s", realtimeURL)
	go e.openBrowser(realtimeURL)
}

// openBrowser 在默认浏览器中打开URL
func (e *Executor) openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
	if err != nil {
		e.logger.Debugf("自动打开浏览器失败: %v", err)
	}
}
