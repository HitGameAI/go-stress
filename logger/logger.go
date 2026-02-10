/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 13:00:00
 * @FilePath: \go-stress\logger\logger.go
 * @Description: go-stress 日志接口，直接复用 go-logger
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package logger

import (
	"time"

	"github.com/kamalyes/go-logger"
)

// 类型别名
type (
	ILogger  = logger.ILogger
	Logger   = logger.Logger
	LogLevel = logger.LogLevel
	IWriter  = logger.IWriter
)

// 常量别名 - 日志级别
const (
	DEBUG = logger.DEBUG
	INFO  = logger.INFO
	WARN  = logger.WARN
	ERROR = logger.ERROR
	FATAL = logger.FATAL
)

// 日志文件滚动的默认参数
const (
	DefaultMaxSize  int64 = 100 * 1024 * 1024 // 单文件 100MB
	DefaultMaxFiles       = 5                 // 保留 5 个历史文件
)

// 函数别名
var (
	ParseLevel = logger.ParseLevel
)

// Default 全局默认 logger 实例
var Default ILogger

func init() {
	// Windows 控制台需要显式打开 Virtual Terminal Processing 才认 ANSI 转义序列，
	// 否则着色输出会显示成裸转义码；只需进程级执行一次，其他平台为空实现
	enableWindowsANSI()
	Default = New()
}

// New 创建带 [STRESS] 前缀的 logger
//
// 以下三处与 go-logger 的默认值不同，原因记在此处以免后续被"对齐默认值"改回：
//   - FormatText：go-logger 默认 JSON（面向日志采集），而本工具的日志是给人在
//     终端上读的，banner、进度表格混着 JSON 行无法阅读
//   - ShowCaller(false)：压测输出关心进度与结果，文件行号只是噪声
//   - 不碰 Colorful：交给 go-logger 按输出目标自动判定（NO_COLOR、TERM=dumb、
//     非字符设备一律关闭）。显式 WithColorful(true) 会关掉这套判定，
//     把 "\033[32m" 一并写进重定向的日志文件里
func New() *Logger {
	return logger.New().
		WithPrefix("[STRESS] ").
		WithFormat(logger.FormatText).
		WithShowCaller(false).
		WithTimeFormat(time.RFC3339)
}

// SetDefault 设置全局默认 logger
func SetDefault(l ILogger) {
	Default = l
}

// NewRotateWriter 创建按大小滚动的文件输出器
// maxSize 为单文件字节上限，maxFiles 为保留的历史文件数，传 0 取默认值
func NewRotateWriter(filePath string, maxSize int64, maxFiles int) IWriter {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	return logger.NewRotateWriter(
		logger.WithFilePath(filePath),
		logger.WithMaxSize(maxSize),
		logger.WithMaxFiles(maxFiles),
	)
}

// Flush 刷写异步输出队列中尚未落盘的日志
//
// go-logger 的默认输出目标是进程级共享的异步批量输出器：日志先入队，再由后台
// flusher 合并写出以省掉逐条 syscall。代价是进程在 os.Exit 处直接终止时，
// 队列里的条目会随进程一起消失——所有退出路径都要先调用它
func Flush() error {
	return logger.Flush()
}

// Close 刷写并关闭异步输出器，供进程退出前的最后一步调用
// 关闭后继续写日志会失败，因此只在确定不再有日志输出时调用
func Close() error {
	return logger.Close()
}

// LogLevelFlag 日志级别标志（实现 flag.Value 接口）
type LogLevelFlag struct {
	Level LogLevel
}

// String 返回日志级别的字符串表示（实现 flag.Value 接口）
func (f *LogLevelFlag) String() string {
	return f.Level.String()
}

// Set 从字符串设置日志级别（实现 flag.Value 接口）
func (f *LogLevelFlag) Set(value string) error {
	level, err := logger.ParseLevel(value)
	if err != nil {
		return err
	}
	f.Level = level
	return nil
}
