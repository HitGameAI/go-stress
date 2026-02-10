/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-11-20 12:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-22 17:15:11
 * @FilePath: \go-stress\config\loader.go
 * @Description: 配置加载器
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"gopkg.in/yaml.v3"
)

// Loader 配置加载器
type Loader struct {
	varResolver *VariableResolver
	logger      logger.ILogger
}

// WithLogger 注入日志器
//
// 加载过程原先用 fmt.Printf 直接写 stdout：那些输出绕过日志级别，-quiet 下照样
// 刷屏，还会和报告表格混在一起。默认为空实现，调用方不注入即完全静默
func (l *Loader) WithLogger(log logger.ILogger) *Loader {
	if log != nil {
		l.logger = log
	}
	return l
}

// NewLoader 创建配置加载器
func NewLoader() *Loader {
	return &Loader{
		logger:      logger.NewEmptyLogger(),
		varResolver: NewVariableResolver(),
	}
}

// LoadFromFile 从文件加载配置
func (l *Loader) LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// filepath.Ext 返回 ".yaml" / ".yml" / ".json"，去掉前缀点号
	ext := filepath.Ext(path)
	if len(ext) > 0 {
		ext = ext[1:] // 去掉 "." 前缀，例如 ".yaml" -> "yaml"
	}
	return l.LoadFromBytes(data, ext)
}

// LoadFromBytes 从字节数据加载配置（支持 YAML 和 JSON）
func (l *Loader) LoadFromBytes(data []byte, format string) (*Config, error) {
	config := DefaultConfig()

	// 解析配置（支持 yaml/yml/json 格式）
	switch format {
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("解析YAML配置失败: %w", err)
		}
	case "json":
		if err := json.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("解析JSON配置失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的配置格式: %s (仅支持yaml/yml/json)", format)
	}

	return l.processConfig(config)
}

// processConfig 处理配置（变量解析、API合并、验证）
func (l *Loader) processConfig(config *Config) (*Config, error) {
	// 设置变量解析器
	l.varResolver.SetVariables(config.Variables)
	config.VarResolver = l.varResolver

	// 合并API配置
	if err := l.mergeAPIsWithCommon(config); err != nil {
		return nil, fmt.Errorf("合并API配置失败: %w", err)
	}

	for i, api := range config.APIs {
		l.logger.DebugKV("已加载 API", "index", i+1, "name", api.Name,
			"method", api.Method, "url", api.URL)
	}

	// 验证配置
	if err := l.validate(config); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return config, nil
}

// mergeAPIsWithCommon 将公共配置合并到各个API配置中
func (l *Loader) mergeAPIsWithCommon(config *Config) error {
	// 如果没有定义APIs，则使用单个配置模式（向后兼容）
	if len(config.APIs) == 0 {
		return nil
	}

	// 遍历每个API配置，合并公共配置
	for i := range config.APIs {
		api := &config.APIs[i]

		// 构建完整URL - 优先级：api.URL > api.Host+api.Path > config.Host+api.Path > config.URL
		api.URL = mathx.IfEmpty(api.URL, buildAPIURL(api, config))
		if api.URL == "" {
			return fmt.Errorf("第%d个API [%s] 的URL不能为空（需要URL或Host+Path）", i+1, api.Name)
		}

		// 继承公共配置
		api.Method = mathx.IfEmpty(api.Method, mathx.IfEmpty(config.Method, "GET"))
		api.Body = mathx.IfEmpty(api.Body, config.Body)

		// 合并Headers（公共headers + API特定headers，API的优先）
		api.Headers = mergeHeaders(config.Headers, api.Headers)

		// 继承Verify配置
		if len(api.Verify) == 0 {
			api.Verify = []VerifyConfig{*config.Verify}
		}

		// 设置默认权重
		api.Weight = mathx.IfNotZero(api.Weight, 1)
	}

	return nil
}

// buildAPIURL 构建API完整URL
func buildAPIURL(api *APIConfig, config *Config) string {
	// 继承Host
	host := mathx.IfEmpty(api.Host, config.Host)

	// 如果有Host和Path，组合成完整URL
	if host != "" && api.Path != "" {
		return host + api.Path
	}
	if host != "" {
		return host
	}
	if api.Path != "" {
		return api.Path
	}
	return config.URL
}

// mergeHeaders 合并Headers（公共headers + API特定headers，API的优先）
func mergeHeaders(common, specific map[string]string) map[string]string {
	if specific == nil {
		specific = make(map[string]string)
	}
	// 先复制公共headers
	for k, v := range common {
		if _, exists := specific[k]; !exists {
			specific[k] = v
		}
	}
	return specific
}

// validate 验证配置
func (l *Loader) validate(config *Config) error {

	// 如果定义了APIs，已经在mergeAPIsWithCommon中验证过了
	if len(config.APIs) > 0 {
		// APIs配置已经通过merge验证
		// 这里只需要验证基础配置
	} else {
		// 单API模式，验证URL
		if config.URL == "" {
			return fmt.Errorf("URL不能为空")
		}
	}

	if config.Concurrency == 0 {
		return fmt.Errorf("并发数必须大于0")
	}

	if config.Requests == 0 && config.Duration == 0 {
		return fmt.Errorf("请求数和持续时间至少要设置一个")
	}

	// 协议特定验证
	switch config.Protocol {
	case ProtocolGRPC:
		if config.GRPC == nil {
			return fmt.Errorf("gRPC配置不能为空")
		}
		if !config.GRPC.UseReflection && config.GRPC.ProtoFile == "" {
			return fmt.Errorf("未启用反射时必须指定proto文件")
		}
		if config.GRPC.Service == "" || config.GRPC.Method == "" {
			return fmt.Errorf("gRPC服务名和方法名不能为空")
		}
	}

	return nil
}

// GetVariableResolver 获取变量解析器
func (l *Loader) GetVariableResolver() *VariableResolver {
	return l.varResolver
}
