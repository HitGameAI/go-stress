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
	"github.com/kamalyes/go-stress/verify"
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
		e.logger.Errorf("❌ 创建存储失败: %v，降级为内存模式", err)
		strg = storage.NewMemoryStorage("local", e.logger)
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

	// 3. 验证中间件
	if e.config.Verify != nil && e.config.Verify.Type != "" {
		verifier, err := verify.Get(VerifyType(e.config.Verify.Type), e.config.Verify)
		if err != nil {
			return nil, fmt.Errorf("获取验证器失败: %w", err)
		}
		chain.Use(VerifyMiddleware(verifier))
	}

	// 4. 构建处理器（客户端是最底层）
	handler := chain.Build(ClientMiddleware(client))

	return handler, nil
}

// Run 执行压测
func (e *Executor) Run(ctx context.Context) (*statistics.Report, error) {
	// 打印启动信息
	e.printStartInfo()

	// 启动实时报告服务器
	port := 8088 // 默认端口
	if e.config.Advanced != nil && e.config.Advanced.RealtimePort > 0 {
		port = e.config.Advanced.RealtimePort
	}
	e.realtimeServer = statistics.NewRealtimeServer(e.collector, port, e.logger)
	if err := e.realtimeServer.Start(); err != nil {
		e.logger.Warnf("⚠️  启动实时报告服务器失败: %v", err)
		// 启动失败时，清空realtimeServer 引用，避免后续误操作
		e.realtimeServer = nil
	} else {
		// 将RealtimeServer设置为控制器
		e.scheduler.controller = e.realtimeServer
		// 自动打开浏览器
		realtimeURL := fmt.Sprintf("http://localhost:%d", port)
		e.logger.Info("🌐 实时监控地址: %s", realtimeURL)
		go e.openBrowser(realtimeURL)
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

	// 检查是否因为context取消而中断
	if err != nil {
		// 如果是用户主动取消，不关闭实时服务器，返回当前报告
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
	e.logger.Info("📊 实时报告服务器继续运行，按 Ctrl+C 可停止并退出")
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
