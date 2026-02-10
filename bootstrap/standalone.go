/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-25 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-25 12:15:19
 * @FilePath: \go-stress\bootstrap\standalone.go
 * @Description: Standalone 模式启动器
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package bootstrap

import (
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/executor"
)

// StandaloneOptions Standalone 模式选项
type StandaloneOptions struct {
	ConfigFile   string
	CurlFile     string
	JMXFile      string // JMeter .jmx 脚本路径
	JMXLossy     bool   // 允许 .jmx 中存在会改变负载的丢失项
	Concurrency  uint64
	Requests     uint64
	Timeout      time.Duration
	StorageMode  StorageMode
	ReportPrefix string
	MaxMemory    string
	Logger       logger.ILogger
	ConfigFunc   func() *config.Config

	// 压测结束后的行为开关
	NoReport bool // 不生成报告文件
	NoPrint  bool // 不在终端打印报告表格
	NoWait   bool // 压测结束即退出，不驻留实时报告服务器
}

// RunStandalone 运行独立模式
func RunStandalone(opts StandaloneOptions) error {
	result := executor.RunTask(executor.RunOptions{
		ConfigFile:    opts.ConfigFile,
		CurlFile:      opts.CurlFile,
		JMXFile:       opts.JMXFile,
		JMXLossy:      opts.JMXLossy,
		Concurrency:   opts.Concurrency,
		Requests:      opts.Requests,
		Timeout:       opts.Timeout,
		StorageMode:   executor.StorageMode(opts.StorageMode),
		ReportPrefix:  opts.ReportPrefix,
		MaxMemory:     opts.MaxMemory,
		Logger:        opts.Logger,
		ConfigFunc:    opts.ConfigFunc,
		IsDistributed: false,
		NoReport:      opts.NoReport,
		NoPrint:       opts.NoPrint,
		NoWait:        opts.NoWait,
	})

	return result.Error
}
