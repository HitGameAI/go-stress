/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 00:00:00
 * @FilePath: \go-stress\executor\runner.go
 * @Description: 通用任务执行器 - 支持独立模式和分布式模式
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/statistics"
	"github.com/kamalyes/go-toolbox/pkg/osx"
	"github.com/kamalyes/go-toolbox/pkg/units"
)

// RunOptions 任务执行选项（通用，支持独立模式和分布式模式）
type RunOptions struct {
	// === 配置来源（三选一） ===
	ConfigFile string                // 配置文件路径
	CurlFile   string                // curl 文件路径
	ConfigFunc func() *config.Config // 从命令行构建配置的函数

	// === 运行时参数 ===
	Concurrency uint64        // 并发数（可覆盖配置文件）
	Requests    uint64        // 请求数（可覆盖配置文件）
	Timeout     time.Duration // 超时时间（可覆盖配置文件）

	// === 存储配置 ===
	StorageMode  StorageMode // 存储模式
	ReportPrefix string      // 报告文件前缀
	MaxMemory    string      // 内存阈值

	// === 日志配置 ===
	Logger logger.ILogger // 日志器

	// === 分布式模式专用 ===
	IsDistributed     bool                  // 是否为分布式模式
	ExternalContext   context.Context       // 外部传入的 context（用于 Slave 控制）
	ExternalCollector *statistics.Collector // 外部 Collector（Slave 模式使用）
	NoReport          bool                  // 不生成报告文件（Slave 模式与 -no-report 使用）
	NoPrint           bool                  // 不打印报告（Slave 模式与 -no-print 使用）
	NoWait            bool                  // 不等待退出（Slave 模式与 -no-wait 使用）
}

// RunResult 任务执行结果
type RunResult struct {
	Report   *statistics.Report
	Executor *Executor
	Error    error
}

// RunTask 执行压测任务（使用策略模式，消除分支判断）
func RunTask(opts RunOptions) *RunResult {
	result := &RunResult{}

	// === 0. 创建运行策略（策略模式：消除分支判断） ===
	var strategy RunStrategy
	if opts.IsDistributed {
		strategy = NewDistributedStrategy(opts.ExternalContext)
	} else {
		strategy = NewStandaloneStrategy(
			opts.Logger,
			opts.ReportPrefix,
			opts.NoPrint,
			opts.NoReport,
			opts.NoWait,
		)
	}

	// === 1. 加载配置 ===
	cfg, err := loadConfig(opts)
	if err != nil {
		result.Error = err
		return result
	}

	// === 2. 验证配置 ===
	if err := validateConfig(cfg); err != nil {
		result.Error = fmt.Errorf("配置验证失败: %w", err)
		return result
	}

	// === 3. 准备存储路径 ===
	storagePath, err := prepareStoragePath(opts)
	if err != nil {
		result.Error = err
		return result
	}

	// === 4. 创建执行器 ===
	exec, err := NewExecutor(cfg, opts.StorageMode, storagePath, opts.Logger)
	if err != nil {
		result.Error = fmt.Errorf("创建执行器失败: %w", err)
		return result
	}
	result.Executor = exec

	// 如果提供了外部 Collector，替换掉
	if opts.ExternalCollector != nil {
		exec.ReplaceCollector(opts.ExternalCollector)
	}

	// === 5. 准备执行上下文（策略决定） ===
	ctx, cancel, sigCh := strategy.PrepareContext(context.Background())
	defer cancel()

	// 确保程序退出前关闭实时报告服务器
	defer func() {
		if exec.GetRealtimeServer() != nil {
			if err := exec.GetRealtimeServer().Stop(); err != nil {
				opts.Logger.Warnf("⚠️  关闭实时报告服务器失败: %v", err)
			}
		}
	}()

	// === 6. 启动内存监控 ===
	if opts.MaxMemory != "" {
		if err := startMemoryMonitor(ctx, opts.MaxMemory, cancel, opts.Logger); err != nil {
			opts.Logger.Warnf("⚠️  %v", err)
		}
	}

	// === 7. 执行压测 ===
	report, err := exec.Run(ctx)
	result.Report = report

	if err != nil {
		// 如果是用户中断（context canceled），不视为错误
		if errors.Is(err, context.Canceled) {
			opts.Logger.Warn("⚠️  用户已中断压测")
		} else {
			result.Error = fmt.Errorf("压测执行失败: %w", err)
			return result
		}
	}

	// === 8. 执行后处理（策略决定：打印报告、保存文件等） ===
	if err := strategy.AfterExecution(exec, report); err != nil {
		opts.Logger.Warnf("⚠️  后处理失败: %v", err)
	}

	// === 9. 等待退出（策略决定） ===
	strategy.WaitForExit(exec, sigCh, ctx)

	return result
}

// loadConfig 加载配置
func loadConfig(opts RunOptions) (*config.Config, error) {
	var cfg *config.Config
	var err error

	// 从curl文件加载
	if opts.CurlFile != "" {
		opts.Logger.InfoKV("📄 解析curl文件", "file", opts.CurlFile)
		data, err := os.ReadFile(opts.CurlFile)
		if err != nil {
			return nil, fmt.Errorf("读取curl文件失败: %w", err)
		}
		cfg, err = config.NewCurlParser(string(data), opts.Logger)
		if err != nil {
			return nil, fmt.Errorf("解析curl文件失败: %w", err)
		}
		// 命令行参数覆盖
		if opts.Concurrency > 0 {
			cfg.Concurrency = opts.Concurrency
		}
		if opts.Requests > 0 {
			cfg.Requests = opts.Requests
		}
		if opts.Timeout > 0 {
			cfg.Timeout = opts.Timeout
		}
	} else if opts.ConfigFile != "" {
		// 从配置文件加载
		opts.Logger.InfoKV("📄 加载配置文件", "file", opts.ConfigFile)
		loader := config.NewLoader()
		cfg, err = loader.LoadFromFile(opts.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("加载配置文件失败: %w", err)
		}
	} else if opts.ConfigFunc != nil {
		// 使用命令行参数
		cfg = opts.ConfigFunc()
	} else {
		return nil, fmt.Errorf("必须提供配置文件、curl文件或命令行参数")
	}

	return cfg, nil
}

// validateConfig 验证配置
func validateConfig(cfg *config.Config) error {
	// 多API模式下，URL已经在config.Loader中验证过了
	if len(cfg.APIs) == 0 {
		if cfg.URL == "" {
			return fmt.Errorf("URL不能为空")
		}
	}

	if cfg.Concurrency == 0 {
		return fmt.Errorf("并发数不能为0")
	}

	if cfg.Requests == 0 {
		return fmt.Errorf("请求数不能为0")
	}

	// gRPC特定验证
	if cfg.Protocol == ProtocolHTTP || cfg.Protocol == "grpc" {
		// 允许 grpc 字符串
		if cfg.GRPC != nil && cfg.GRPC.UseReflection {
			if cfg.GRPC.Service == "" {
				return fmt.Errorf("gRPC服务名不能为空")
			}
			if cfg.GRPC.Method == "" {
				return fmt.Errorf("gRPC方法名不能为空")
			}
		}
	}

	return nil
}

// prepareStoragePath 准备存储路径
func prepareStoragePath(opts RunOptions) (string, error) {
	var storagePath string

	switch opts.StorageMode {
	case StorageModeMemory:
		opts.Logger.Info("💾 存储模式: Memory (高速、无限制、不持久化)")
		storagePath = "" // 内存模式不需要路径

	case StorageModeSQLite:
		reportDir := filepath.Join(opts.ReportPrefix, fmt.Sprintf("%d", time.Now().Unix()))
		if err := os.MkdirAll(reportDir, os.ModePerm); err != nil {
			return "", fmt.Errorf("创建报告目录失败: %w", err)
		}
		storagePath = filepath.Join(reportDir, "details.db")
		opts.Logger.Info("💾 存储模式: SQLite (持久化、SQL查询、事务支持)")
		opts.Logger.InfoKV("💾 数据库路径", "path", storagePath)

	case StorageModeBadger:
		reportDir := filepath.Join(opts.ReportPrefix, fmt.Sprintf("%d", time.Now().Unix()))
		if err := os.MkdirAll(reportDir, os.ModePerm); err != nil {
			return "", fmt.Errorf("创建报告目录失败: %w", err)
		}
		storagePath = filepath.Join(reportDir, "badger")
		opts.Logger.Info("💾 存储模式: BadgerDB (高性能写入、LSM-Tree、纯Go实现)")
		opts.Logger.InfoKV("💾 数据库路径", "path", storagePath)

	default:
		return "", fmt.Errorf("不支持的存储模式: %s ，支持的模式: %s, %s, %s",
			opts.StorageMode, StorageModeMemory, StorageModeSQLite, StorageModeBadger)
	}

	return storagePath, nil
}

// startMemoryMonitor 启动内存监控
func startMemoryMonitor(ctx context.Context, maxMemory string, cancel context.CancelFunc, log logger.ILogger) error {
	threshold, err := units.ParseBytes(maxMemory)
	if err != nil {
		return fmt.Errorf("内存阈值格式错误: %w,将忽略内存监控", err)
	}

	log.Infof("🔍 启动内存监控，阈值: %s (%d MB)", maxMemory, threshold/(1024*1024))

	monitor := osx.NewAdvancedMonitor().
		AddThreshold(osx.LevelWarning, threshold*80/100).
		AddThreshold(osx.LevelCritical, threshold).
		SetMetricType(osx.MetricAlloc).
		SetCheckOnce(false).
		SetMaxHistory(200).
		EnableGrowthCheck(20.0, 30*time.Second).
		OnWarning(func(snapshot osx.Snapshot) {
			log.Warnf("[⚠️  警告] 内存使用: %s / %s (%.1f%%), Goroutines: %d",
				units.FormatBytes(snapshot.Alloc),
				maxMemory,
				float64(snapshot.Alloc)/float64(threshold)*100,
				snapshot.Goroutines)
		}).
		OnCritical(func(snapshot osx.Snapshot) {
			log.Warnf("[🚨 严重] 内存使用超过阈值: %s / %s (%.1f%%)",
				units.FormatBytes(snapshot.Alloc),
				maxMemory,
				float64(snapshot.Alloc)/float64(threshold)*100)
			log.Warnf("  GC次数: %d, Goroutines: %d", snapshot.NumGC, snapshot.Goroutines)
			log.Warn("🛑 自动停止测试任务...")
			cancel()
		}).
		OnGrowthAlert(func(rate osx.GrowthRate, snapshot osx.Snapshot) {
			log.Warnf("[📈 增长告警] 增长率: %.2f%%, 绝对增长: %s, 时间窗口: %v",
				rate.Percentage,
				units.FormatBytes(uint64(rate.Absolute)),
				rate.Duration)
		}).
		OnCheck(func(snapshot osx.Snapshot) {
			log.Debugf("📊 内存监控 - Alloc: %s, Sys: %s, Goroutines: %d, GC: %d",
				units.FormatBytes(snapshot.Alloc),
				units.FormatBytes(snapshot.Sys),
				snapshot.Goroutines,
				snapshot.NumGC)
		})

	go monitor.Start(ctx, 5*time.Second)
	return nil
}

// saveReports 保存报告
func saveReports(exec *Executor, report *statistics.Report, reportPrefix string, log logger.ILogger) error {
	reportDir := filepath.Join(reportPrefix, fmt.Sprintf("%d", time.Now().Unix()))

	if err := os.MkdirAll(reportDir, os.ModePerm); err != nil {
		if err := exec.GetCollector().Close(); err != nil {
			log.Warnf("⚠️  关闭存储失败: %v", err)
		}
		return fmt.Errorf("创建报告目录失败: %w", err)
	}

	// 生成并保存HTML报告 - 使用 ReportExporter
	htmlReportFile := filepath.Join(reportDir, "index.html")
	totalDuration := time.Duration(0)
	if report != nil {
		totalDuration = report.TotalTime
	}

	// 使用新的 ReportExporter 架构
	exporter := statistics.NewReportExporter(exec.GetCollector())
	if err := exporter.ExportHTML(totalDuration, htmlReportFile); err != nil {
		return fmt.Errorf("生成HTML报告失败: %w", err)
	}

	log.Info("🌐 在浏览器中打开查看详细图表: %s", fileURL(htmlReportFile))

	// 确保所有数据都写入存储
	if err := exec.GetCollector().Close(); err != nil {
		return fmt.Errorf("关闭存储失败: %w", err)
	}

	return nil
}

// fileURL 把本地文件路径转成可点击的 file:// URL
//
// 报告路径是相对于工作目录的（如 stress-report/1793610589/index.html），
// 直接拼 "file:///" + 相对路径得到的是指向文件系统根的坏链接，点开必然 404
// 这里先解析成绝对路径，再交给 url.URL 处理转义（路径含空格时不转义同样打不开）
func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Windows 的 C:\dir\file 需转成 /C:/dir/file 才是合法的 file URL 路径
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	u := url.URL{Scheme: "file", Path: abs}
	return u.String()
}

// waitForExit 等待退出
func waitForExit(exec *Executor, sigCh chan os.Signal, ctx context.Context, log logger.ILogger) {
	realtimePort := 8088
	if realtimeServer := exec.GetRealtimeServer(); realtimeServer != nil {
		realtimePort = realtimeServer.GetPort()
	}

	log.Info("💡 提示: 实时报告服务器仍在运行")
	log.Info("   访问 http://localhost:%d 查看实时报告", realtimePort)
	log.Info("   按 Ctrl+C 退出程序")

	select {
	case <-sigCh:
		log.Info("👋 程序已退出")
	case <-ctx.Done():
		log.Info("👋 程序已退出")
	}
}
