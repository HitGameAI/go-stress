/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 22:30:00
 * @FilePath: \go-stress\executor\run_strategy.go
 * @Description: 运行策略接口 - 消除独立模式和分布式模式的分支判断
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/statistics"
)

// RunStrategy 运行策略接口（策略模式）
type RunStrategy interface {
	// PrepareContext 准备执行上下文和信号处理
	PrepareContext(baseCtx context.Context) (context.Context, context.CancelFunc, chan os.Signal)

	// AfterExecution 执行完成后的处理（打印报告、保存文件等）
	AfterExecution(exec *Executor, report *statistics.Report) error

	// WaitForExit 等待退出
	WaitForExit(exec *Executor, sigCh chan os.Signal, ctx context.Context)
}

// StandaloneStrategy 独立模式策略
type StandaloneStrategy struct {
	logger       logger.ILogger
	reportPrefix string
	noPrint      bool
	noReport     bool
	noWait       bool
}

// NewStandaloneStrategy 创建独立模式策略
func NewStandaloneStrategy(logger logger.ILogger, reportPrefix string, noPrint, noReport, noWait bool) *StandaloneStrategy {
	return &StandaloneStrategy{
		logger:       logger,
		reportPrefix: reportPrefix,
		noPrint:      noPrint,
		noReport:     noReport,
		noWait:       noWait,
	}
}

// PrepareContext 准备独立模式的上下文（带信号监听）
func (s *StandaloneStrategy) PrepareContext(baseCtx context.Context) (context.Context, context.CancelFunc, chan os.Signal) {
	ctx, cancel := context.WithCancel(baseCtx)

	// 独立模式：启动信号监听
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		s.logger.Warn("⚠️  收到中断信号，正在停止...")
		cancel()
	}()

	return ctx, cancel, sigCh
}

// AfterExecution 独立模式的后处理（打印报告、保存文件）
func (s *StandaloneStrategy) AfterExecution(exec *Executor, report *statistics.Report) error {
	// 打印报告
	if !s.noPrint && report != nil {
		report.Print()
	}

	// 保存报告文件
	if !s.noReport {
		if err := saveReports(exec, report, s.reportPrefix, s.logger); err != nil {
			s.logger.Warnf("⚠️  保存报告失败: %v", err)
			return err
		}
	}

	return nil
}

// WaitForExit 独立模式的等待退出
func (s *StandaloneStrategy) WaitForExit(exec *Executor, sigCh chan os.Signal, ctx context.Context) {
	if !s.noWait {
		waitForExit(exec, sigCh, ctx, s.logger)
	}
}

// DistributedStrategy 分布式模式策略（Slave节点）
type DistributedStrategy struct {
	externalCtx context.Context
}

// NewDistributedStrategy 创建分布式模式策略
func NewDistributedStrategy(externalCtx context.Context) *DistributedStrategy {
	return &DistributedStrategy{
		externalCtx: externalCtx,
	}
}

// PrepareContext 准备分布式模式的上下文（使用外部传入的context）
func (d *DistributedStrategy) PrepareContext(baseCtx context.Context) (context.Context, context.CancelFunc, chan os.Signal) {
	// 分布式模式：使用外部 context（由 Master 控制）
	if d.externalCtx != nil {
		return d.externalCtx, func() {}, nil
	}
	// 降级：创建一个新的 context
	ctx, cancel := context.WithCancel(baseCtx)
	return ctx, cancel, nil
}

// AfterExecution 分布式模式的后处理（不做任何操作，由Master统一处理）
func (d *DistributedStrategy) AfterExecution(exec *Executor, report *statistics.Report) error {
	// 分布式模式：不打印报告，不保存文件
	return nil
}

// WaitForExit 分布式模式的等待退出（不等待，由Master控制）
func (d *DistributedStrategy) WaitForExit(exec *Executor, sigCh chan os.Signal, ctx context.Context) {
	// 分布式模式：不等待
}
