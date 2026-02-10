/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 17:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 17:00:00
 * @FilePath: \go-stress\config\aliases.go
 * @Description: config 模块类型别名
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package config

import (
	"github.com/kamalyes/go-stress/types"
)

// 类型别名 - 从 types 包导入
type (
	ProtocolType   = types.ProtocolType
	VerifyType     = types.VerifyType
	ExtractorType  = types.ExtractorType
	ExpectOperator = types.ExpectOperator
	RunMode        = types.RunMode
	StorageMode    = types.StorageMode
)

// 常量别名
const (
	// 协议类型
	ProtocolHTTP      = types.ProtocolHTTP
	ProtocolGRPC      = types.ProtocolGRPC
	ProtocolWebSocket = types.ProtocolWebSocket

	// 验证类型
	StorageModeMemory = types.StorageModeMemory
	StorageModeSQLite = types.StorageModeSQLite
	StorageModeBadger = types.StorageModeBadger
)

const (
	VerifyTypeStatusCode = types.VerifyTypeStatusCode
	VerifyTypeJSONPath   = types.VerifyTypeJSONPath
	VerifyTypeContains   = types.VerifyTypeContains
	VerifyTypeRegex      = types.VerifyTypeRegex
	VerifyTypeCustom     = types.VerifyTypeCustom

	// 提取器类型
	ExtractorTypeJSONPath = types.ExtractorTypeJSONPath
	ExtractorTypeRegex    = types.ExtractorTypeRegex
	ExtractorTypeHeader   = types.ExtractorTypeHeader
)
