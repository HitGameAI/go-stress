/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 23:00:00
 * @FilePath: \go-stress\executor\variable_replacer.go
 * @Description: 变量替换器 - 统一变量解析逻辑
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"github.com/kamalyes/go-stress/config"
)

// VariableReplacer 变量替换器（统一处理动态变量和提取变量）
type VariableReplacer struct {
	resolver      *config.VariableResolver // 动态变量解析器（如 {{$timestamp}}）
	extractedVars map[string]string        // 提取的变量（如从上一个API响应中提取的）
}

// NewVariableReplacer 创建变量替换器
func NewVariableReplacer(resolver *config.VariableResolver, extractedVars map[string]string) *VariableReplacer {
	if extractedVars == nil {
		extractedVars = make(map[string]string)
	}
	return &VariableReplacer{
		resolver:      resolver,
		extractedVars: extractedVars,
	}
}

// ReplaceInAPIConfig 替换 API 配置中的所有变量
func (vr *VariableReplacer) ReplaceInAPIConfig(apiCfg *APIConfig) *APIConfig {
	if apiCfg == nil {
		return nil
	}

	// 整体复制再覆盖需替换的字段，而非逐字段列举：
	// 逐字段写法漏掉了 Host/Path/Weight/Repeat/DependsOn，APIConfig 以后新增字段
	// 同样会被静默丢成零值——这类缺失不会报错，只会让行为莫名其妙
	newCfg := *apiCfg
	newCfg.URL = vr.ReplaceString(apiCfg.URL)
	newCfg.Headers = vr.ReplaceHeaders(apiCfg.Headers)
	newCfg.Body = vr.ReplaceString(apiCfg.Body)

	return &newCfg
}

// ReplaceString 替换字符串中的变量（两步：1.提取变量 2.动态变量）
func (vr *VariableReplacer) ReplaceString(s string) string {
	if s == "" {
		return s
	}

	// 第一步：替换提取的变量（如 {{token}}）
	if len(vr.extractedVars) > 0 {
		s = replaceVars(s, vr.extractedVars)
	}

	// 第二步：替换动态变量（如 {{$timestamp}}）
	if vr.resolver != nil {
		if resolved, err := vr.resolver.Resolve(s); err == nil {
			s = resolved
		}
	}

	return s
}

// ReplaceHeaders 替换 Headers 中的变量（返回新的 map）
func (vr *VariableReplacer) ReplaceHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}

	newHeaders := make(map[string]string, len(headers))
	for k, v := range headers {
		newHeaders[k] = vr.ReplaceString(v)
	}

	return newHeaders
}

// UpdateExtractedVars 更新提取的变量
func (vr *VariableReplacer) UpdateExtractedVars(vars map[string]string) {
	if vars == nil {
		return
	}
	for k, v := range vars {
		vr.extractedVars[k] = v
	}
}

// GetExtractedVars 获取所有提取的变量
func (vr *VariableReplacer) GetExtractedVars() map[string]string {
	return vr.extractedVars
}
