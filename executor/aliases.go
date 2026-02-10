/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 13:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 10:36:08
 * @FilePath: \go-stress\executor\aliases.go
 * @Description: executor 模块类型别名
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/types"
)

// 类型别名 - 从 types 包导入
type (
	// 协议相关
	Client       = types.Client
	Request      = types.Request
	Response     = types.Response
	ProtocolType = types.ProtocolType

	// 执行器相关
	Result         = types.Result
	ClientFactory  = types.ClientFactory
	RequestHandler = types.RequestHandler
	Middleware     = types.Middleware

	// 统计相关
	RequestResult = types.RequestResult
	ExtractorType = types.ExtractorType

	// 存储相关
	StorageMode = types.StorageMode

	// 验证相关
	VerifyType = types.VerifyType

	// 验证结果
	VerificationResult = types.VerificationResult

	// 配置相关 - 直接使用 config.APIConfig，不再转换
	APIConfig    = config.APIConfig
	VerifyConfig = config.VerifyConfig
)

// 常量别名
const (
	ProtocolHTTP      = types.ProtocolHTTP
	ProtocolGRPC      = types.ProtocolGRPC
	ProtocolWebSocket = types.ProtocolWebSocket

	ExtractorTypeJSONPath   = types.ExtractorTypeJSONPath
	ExtractorTypeRegex      = types.ExtractorTypeRegex
	ExtractorTypeHeader     = types.ExtractorTypeHeader
	ExtractorTypeExpression = types.ExtractorTypeExpression
	// 存储模式
	StorageModeMemory = types.StorageModeMemory
	StorageModeSQLite = types.StorageModeSQLite
	StorageModeBadger = types.StorageModeBadger
)
