/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 00:00:00
 * @FilePath: \go-stress\types\verify.go
 * @Description: 验证相关类型定义
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package types

import validator "github.com/kamalyes/go-argus"

// VerifyType 验证类型
type VerifyType string

const (
	// 基础验证
	VerifyTypeStatusCode VerifyType = "STATUS_CODE" // 状态码验证（支持操作符：=, !=, >, <, >=, <=）
	VerifyTypeJSONPath   VerifyType = "JSONPATH"    // JSONPath验证（支持操作符：=, !=, >, <, >=, <=, contains, hasPrefix, hasSuffix）
	VerifyTypeContains   VerifyType = "CONTAINS"    // 包含字符串验证
	VerifyTypeRegex      VerifyType = "REGEX"       // 正则表达式验证

	// JSON 相关验证
	VerifyTypeJSONSchema VerifyType = "JSON_SCHEMA" // JSON Schema 验证
	VerifyTypeJSONValid  VerifyType = "JSON_VALID"  // JSON 格式验证

	// HTTP 相关验证
	VerifyTypeHeader       VerifyType = "HEADER"        // HTTP 响应头验证
	VerifyTypeResponseTime VerifyType = "RESPONSE_TIME" // 响应时间验证（毫秒）
	VerifyTypeResponseSize VerifyType = "RESPONSE_SIZE" // 响应大小验证（字节）

	// 数据格式验证
	VerifyTypeEmail  VerifyType = "EMAIL"  // Email 格式验证
	VerifyTypeIP     VerifyType = "IP"     // IP 地址验证（支持 IPv4/IPv6）
	VerifyTypeURL    VerifyType = "URL"    // URL 格式验证
	VerifyTypeUUID   VerifyType = "UUID"   // UUID 格式验证
	VerifyTypeBase64 VerifyType = "BASE64" // Base64 编码验证

	// 字符串验证
	VerifyTypeLength   VerifyType = "LENGTH"    // 字符串长度验证
	VerifyTypePrefix   VerifyType = "PREFIX"    // 前缀验证
	VerifyTypeSuffix   VerifyType = "SUFFIX"    // 后缀验证
	VerifyTypeEmpty    VerifyType = "EMPTY"     // 空值验证
	VerifyTypeNotEmpty VerifyType = "NOT_EMPTY" // 非空验证

	// 自定义
	VerifyTypeCustom VerifyType = "CUSTOM" // 自定义验证
)

// ToString
func (vt VerifyType) ToString() string {
	return string(vt)
}

// ExpectOperator 比较操作符（用于 STATUS_CODE/JSONPATH 等）
// 使用 go-toolbox/validator 提供的 CompareOperator
type ExpectOperator = validator.CompareOperator

// 操作符常量 - 直接引用 validator 中的定义
const (
	OpEQ                 = validator.OpEqual
	OpNE                 = validator.OpNotEqual
	OpGT                 = validator.OpGreaterThan
	OpGTE                = validator.OpGreaterThanOrEqual
	OpLT                 = validator.OpLessThan
	OpLTE                = validator.OpLessThanOrEqual
	OpContains           = validator.OpContains
	OpNotContains        = validator.OpNotContains
	OpHasPrefix          = validator.OpHasPrefix
	OpHasSuffix          = validator.OpHasSuffix
	OpEmpty              = validator.OpEmpty
	OpNotEmpty           = validator.OpNotEmpty
	OpRegex              = validator.OpRegex
	OpEqual              = validator.OpSymbolEqual
	OpDoubleEqual        = validator.OpSymbolEqual
	OpNotEqual           = validator.OpSymbolNotEqual
	OpGreaterThan        = validator.OpSymbolGreaterThan
	OpGreaterThanOrEqual = validator.OpSymbolGreaterThanOrEqual
	OpLessThan           = validator.OpSymbolLessThan
	OpLessThanOrEqual    = validator.OpSymbolLessThanOrEqual
	OpRegexp             = validator.OpRegex
)

// Verifier 验证器接口
type Verifier interface {
	// Verify 验证响应
	Verify(resp *Response) (bool, error)
}
