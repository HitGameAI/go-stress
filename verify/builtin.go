/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-25 22:56:39
 * @FilePath: \go-stress\verify\builtin.go
 * @Description: 内置验证器实现 - 完全使用 go-toolbox/validator
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package verify

import (
	"fmt"
	"strconv"
	"strings"

	validator "github.com/kamalyes/go-argus"
	"github.com/kamalyes/go-stress/config"
)

// ===== HTTP验证器（统一入口） =====

// HTTPVerifier HTTP验证器 - 使用 go-toolbox/validator
type HTTPVerifier struct {
	config *config.VerifyConfig
}

// NewHTTPVerifier 创建HTTP验证器
func NewHTTPVerifier(cfg *config.VerifyConfig) *HTTPVerifier {
	if cfg == nil {
		cfg = &config.VerifyConfig{
			Type:   VerifyTypeStatusCode,
			Expect: 200,
		}
	}
	return &HTTPVerifier{config: cfg}
}

// Verify 验证HTTP响应
func (v *HTTPVerifier) Verify(resp *Response) (bool, error) {
	if resp.Error != nil {
		return false, resp.Error
	}

	// 初始化验证结果列表
	if resp.Verifications == nil {
		resp.Verifications = make([]VerificationResult, 0)
	}

	switch v.config.Type {
	// 基础验证
	case VerifyTypeStatusCode:
		return v.verifyStatusCode(resp)
	case VerifyTypeJSONPath:
		return v.verifyJSONPath(resp)
	case VerifyTypeContains:
		return v.verifyContains(resp)
	case VerifyTypeRegex:
		return v.verifyRegex(resp)

	// JSON 相关验证
	case VerifyTypeJSONSchema:
		return v.verifyJSONSchema(resp)
	case VerifyTypeJSONValid:
		return v.verifyJSONValid(resp)

	// HTTP 相关验证
	case VerifyTypeHeader:
		return v.verifyHeader(resp)
	case VerifyTypeResponseTime:
		return v.verifyResponseTime(resp)
	case VerifyTypeResponseSize:
		return v.verifyResponseSize(resp)

	// 数据格式验证
	case VerifyTypeEmail:
		return v.verifyEmail(resp)
	case VerifyTypeIP:
		return v.verifyIP(resp)
	case VerifyTypeURL:
		return v.verifyURL(resp)
	case VerifyTypeUUID:
		return v.verifyUUID(resp)
	case VerifyTypeBase64:
		return v.verifyBase64(resp)

	// 字符串验证
	case VerifyTypeLength:
		return v.verifyLength(resp)
	case VerifyTypePrefix:
		return v.verifyPrefix(resp)
	case VerifyTypeSuffix:
		return v.verifySuffix(resp)
	case VerifyTypeEmpty:
		return v.verifyEmpty(resp)
	case VerifyTypeNotEmpty:
		return v.verifyNotEmpty(resp)

	default:
		return true, nil
	}
}

// verifyStatusCode 验证状态码 - 使用 validator.ValidateStatusCode
func (v *HTTPVerifier) verifyStatusCode(resp *Response) (bool, error) {
	expectedCode := 200 // 默认期望200
	operator := v.config.Operator
	if operator == "" {
		operator = validator.OpEqual
	}

	// 处理 Expect 字段,支持int和string类型
	switch exp := v.config.Expect.(type) {
	case int:
		expectedCode = exp
	case float64: // JSON解析时数字会变成float64
		expectedCode = int(exp)
	case string:
		if len(exp) > 0 && exp[0] != '{' {
			if parsed, err := strconv.Atoi(strings.TrimSpace(exp)); err == nil {
				expectedCode = parsed
			}
		}
	}

	// 使用 validator.ValidateStatusCode 进行验证
	compareResult := validator.ValidateStatusCode(resp.StatusCode, expectedCode, operator)

	// 转换为 VerificationResult
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyJSONPath 验证JSON路径 - 使用 validator.ValidateJSONPath
func (v *HTTPVerifier) verifyJSONPath(resp *Response) (bool, error) {
	// 检查状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	// 确定操作符
	operator := v.config.Operator
	if v.config.Regex {
		operator = validator.OpRegex
	}
	if operator == "" {
		operator = validator.OpEqual
	}

	// 使用 validator.ValidateJSONPath 进行验证
	var compareResult validator.CompareResult
	if v.config.Expect != nil {
		compareResult = validator.ValidateJSONPath(resp.Body, v.config.JSONPath, v.config.Expect, operator)
	} else {
		// 没有期望值，只验证路径存在
		compareResult = validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
	}

	// 如果有描述信息，添加到验证结果中
	if v.config.Description != "" && compareResult.Message != "" {
		if compareResult.Success {
			compareResult.Message = v.config.Description + ": 验证通过"
		} else {
			compareResult.Message = v.config.Description + ": " + compareResult.Message
		}
	}

	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	// 添加额外字段
	result.Field = v.config.JSONPath
	result.Operator = operator.String()
	result.Description = v.config.Description

	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyContains 验证包含字符串 - 使用 validator.ValidateContains
func (v *HTTPVerifier) verifyContains(resp *Response) (bool, error) {
	// 检查状态码是否为成功状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	containsStr, ok := v.config.Expect.(string)
	if !ok {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "contains 验证需要字符串类型的 expect 值",
			Expect:  fmt.Sprintf("%v", v.config.Expect),
			Actual:  "类型错误",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("类型错误: %s", result.Message)
	}

	// 使用 validator.ValidateContains
	compareResult := validator.ValidateContains(resp.Body, containsStr)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyRegex 验证正则表达式 - 使用 validator.ValidateRegex
func (v *HTTPVerifier) verifyRegex(resp *Response) (bool, error) {
	// 检查状态码是否为成功状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	pattern, ok := v.config.Expect.(string)
	if !ok {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "regex 验证需要字符串类型的 expect 值",
			Expect:  fmt.Sprintf("%v", v.config.Expect),
			Actual:  "类型错误",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("类型错误: %s", result.Message)
	}

	// 使用 validator.ValidateRegex
	compareResult := validator.ValidateRegex(resp.Body, pattern)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// ===== JSON 相关验证 =====

// verifyJSONSchema 验证 JSON Schema
func (v *HTTPVerifier) verifyJSONSchema(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	schema, ok := v.config.Expect.(map[string]interface{})
	if !ok {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "JSON Schema 验证需要 object 类型的 expect 值",
			Expect:  fmt.Sprintf("%v", v.config.Expect),
			Actual:  "类型错误",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("类型错误: %s", result.Message)
	}

	// 使用 validator.ValidateJSONSchema
	compareResult := validator.ValidateJSONSchema(resp.Body, schema)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyJSONValid 验证 JSON 格式是否有效
func (v *HTTPVerifier) verifyJSONValid(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	// 使用 validator.ValidateJSON - 它返回 error，需要转换为 CompareResult
	err := validator.ValidateJSON(resp.Body)
	result := VerificationResult{
		Type:    v.config.Type,
		Success: err == nil,
		Expect:  "valid JSON",
		Actual:  "JSON response",
	}
	if err != nil {
		result.Message = fmt.Sprintf("JSON 格式验证失败: %s", err.Error())
	} else {
		result.Message = "JSON 格式验证通过"
	}

	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// ===== HTTP 相关验证 =====

// verifyHeader 验证 HTTP 响应头
func (v *HTTPVerifier) verifyHeader(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	// 获取要验证的 header 名称（从 JSONPath 字段或其他配置）
	headerName := v.config.JSONPath
	if headerName == "" {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "Header 验证需要指定 header 名称（使用 jsonpath 字段）",
			Expect:  "header name",
			Actual:  "未指定",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("配置错误: %s", result.Message)
	}

	actualValue := resp.Headers[headerName]
	operator := v.config.Operator
	if operator == "" {
		operator = validator.OpEqual
	}

	// 使用 validator.ValidateString 进行验证
	expectStr, ok := v.config.Expect.(string)
	if !ok {
		expectStr = fmt.Sprintf("%v", v.config.Expect)
	}

	compareResult := validator.ValidateString(actualValue, expectStr, operator)
	compareResult.Message = fmt.Sprintf("Header[%s] %s", headerName, compareResult.Message)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyResponseTime 验证响应时间（毫秒）
func (v *HTTPVerifier) verifyResponseTime(resp *Response) (bool, error) {
	// 从 resp.Duration 获取响应时间（如果有的话，需要在 Response 类型中添加）
	// 这里假设响应时间已经记录在某个字段中
	expectedTime, ok := v.config.Expect.(float64)
	if !ok {
		if intVal, ok := v.config.Expect.(int); ok {
			expectedTime = float64(intVal)
		} else {
			result := VerificationResult{
				Type:    v.config.Type,
				Success: false,
				Message: "响应时间验证需要数值类型的 expect 值（毫秒）",
				Expect:  fmt.Sprintf("%v", v.config.Expect),
				Actual:  "类型错误",
			}
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("类型错误: %s", result.Message)
		}
	}

	operator := v.config.Operator
	if operator == "" {
		operator = validator.OpLessThanOrEqual // 默认检查响应时间不超过期望值
	}

	// 注意：这里需要实际的响应时间，暂时使用占位值
	// 实际使用时需要在 Response 结构中添加 Duration 字段
	actualTime := 0.0 // TODO: 从 resp 中获取实际响应时间

	compareResult := validator.CompareNumbers(actualTime, expectedTime, operator)
	compareResult.Message = fmt.Sprintf("响应时间 %s", compareResult.Message)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyResponseSize 验证响应大小（字节）
func (v *HTTPVerifier) verifyResponseSize(resp *Response) (bool, error) {
	expectedSize, ok := v.config.Expect.(float64)
	if !ok {
		if intVal, ok := v.config.Expect.(int); ok {
			expectedSize = float64(intVal)
		} else {
			result := VerificationResult{
				Type:    v.config.Type,
				Success: false,
				Message: "响应大小验证需要数值类型的 expect 值（字节）",
				Expect:  fmt.Sprintf("%v", v.config.Expect),
				Actual:  "类型错误",
			}
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("类型错误: %s", result.Message)
		}
	}

	operator := v.config.Operator
	if operator == "" {
		operator = validator.OpEqual
	}

	actualSize := float64(len(resp.Body))
	compareResult := validator.CompareNumbers(actualSize, expectedSize, operator)
	compareResult.Message = fmt.Sprintf("响应大小 %s", compareResult.Message)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// ===== 数据格式验证 =====

// verifyEmail 验证 Email 格式
func (v *HTTPVerifier) verifyEmail(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	// 可以从 JSONPath 提取值，或直接验证 Body
	var valueToCheck string
	if v.config.JSONPath != "" {
		// 从 JSONPath 提取
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = strings.TrimSpace(string(resp.Body))
	}

	// 使用 validator.ValidateEmail
	compareResult := validator.ValidateEmail(valueToCheck)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyIP 验证 IP 地址格式
func (v *HTTPVerifier) verifyIP(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = strings.TrimSpace(string(resp.Body))
	}

	// 使用 validator.ValidateIP (支持 IPv4 和 IPv6)
	compareResult := validator.ValidateIP(valueToCheck)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyURL 验证 URL 格式
func (v *HTTPVerifier) verifyURL(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = strings.TrimSpace(string(resp.Body))
	}

	// 使用 validator.ValidateHTTP (验证 HTTP/HTTPS URL)
	compareResult := validator.ValidateHTTP(valueToCheck)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyUUID 验证 UUID 格式
func (v *HTTPVerifier) verifyUUID(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = strings.TrimSpace(string(resp.Body))
	}

	// 使用 validator.ValidateUUID
	compareResult := validator.ValidateUUID(valueToCheck)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyBase64 验证 Base64 编码
func (v *HTTPVerifier) verifyBase64(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = strings.TrimSpace(string(resp.Body))
	}

	// 使用 validator.ValidateBase64
	compareResult := validator.ValidateBase64(valueToCheck)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// ===== 字符串验证 =====

// verifyLength 验证字符串长度
func (v *HTTPVerifier) verifyLength(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	expectedLen, ok := v.config.Expect.(float64)
	if !ok {
		if intVal, ok := v.config.Expect.(int); ok {
			expectedLen = float64(intVal)
		} else {
			result := VerificationResult{
				Type:    v.config.Type,
				Success: false,
				Message: "长度验证需要数值类型的 expect 值",
				Expect:  fmt.Sprintf("%v", v.config.Expect),
				Actual:  "类型错误",
			}
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("类型错误: %s", result.Message)
		}
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = string(resp.Body)
	}

	operator := v.config.Operator
	if operator == "" {
		operator = validator.OpEqual
	}

	actualLen := float64(len(valueToCheck))
	compareResult := validator.CompareNumbers(actualLen, expectedLen, operator)
	compareResult.Message = fmt.Sprintf("字符串长度 %s", compareResult.Message)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyPrefix 验证字符串前缀
func (v *HTTPVerifier) verifyPrefix(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	prefix, ok := v.config.Expect.(string)
	if !ok {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "前缀验证需要字符串类型的 expect 值",
			Expect:  fmt.Sprintf("%v", v.config.Expect),
			Actual:  "类型错误",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("类型错误: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = string(resp.Body)
	}

	// 使用 validator.ValidateString 的 hasPrefix 操作符
	compareResult := validator.ValidateString(valueToCheck, prefix, validator.OpHasPrefix)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifySuffix 验证字符串后缀
func (v *HTTPVerifier) verifySuffix(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	suffix, ok := v.config.Expect.(string)
	if !ok {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: "后缀验证需要字符串类型的 expect 值",
			Expect:  fmt.Sprintf("%v", v.config.Expect),
			Actual:  "类型错误",
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("类型错误: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = string(resp.Body)
	}

	// 使用 validator.ValidateString 的 hasSuffix 操作符
	compareResult := validator.ValidateString(valueToCheck, suffix, validator.OpHasSuffix)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyEmpty 验证字符串为空
func (v *HTTPVerifier) verifyEmpty(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = string(resp.Body)
	}

	// 使用 validator.ValidateString 的 empty 操作符
	compareResult := validator.ValidateString(valueToCheck, "", validator.OpEmpty)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}

// verifyNotEmpty 验证字符串非空
func (v *HTTPVerifier) verifyNotEmpty(resp *Response) (bool, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result := VerificationResult{
			Type:    v.config.Type,
			Success: false,
			Message: fmt.Sprintf("HTTP请求失败，状态码: %d", resp.StatusCode),
			Expect:  "2xx",
			Actual:  fmt.Sprintf("%d", resp.StatusCode),
		}
		resp.Verifications = append(resp.Verifications, result)
		return false, fmt.Errorf("HTTP请求失败: %s", result.Message)
	}

	var valueToCheck string
	if v.config.JSONPath != "" {
		extractResult := validator.ValidateJSONPathExists(resp.Body, v.config.JSONPath)
		if !extractResult.Success {
			result := NewVerificationResultFromCompare(v.config.Type, extractResult)
			resp.Verifications = append(resp.Verifications, result)
			return false, fmt.Errorf("%s", result.Message)
		}
		valueToCheck = extractResult.Actual
	} else {
		valueToCheck = string(resp.Body)
	}

	// 使用 validator.ValidateString 的 notEmpty 操作符
	compareResult := validator.ValidateString(valueToCheck, "", validator.OpNotEmpty)
	result := NewVerificationResultFromCompare(v.config.Type, compareResult)
	resp.Verifications = append(resp.Verifications, result)

	if !result.Success {
		return false, fmt.Errorf("%s", result.Message)
	}

	return true, nil
}
