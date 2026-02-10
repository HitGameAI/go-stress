/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 19:30:00
 * @FilePath: \go-stress\executor\extractor.go
 * @Description: 数据提取器 - 支持从请求和响应提取，支持数据转换
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"text/template"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/types"
	"github.com/kamalyes/go-toolbox/pkg/convert"
	"github.com/oliveagle/jsonpath"
)

// ExtractorContext 提取器上下文
type ExtractorContext struct {
	Request   *Request
	Response  *types.Response
	Variables map[string]string
}

// Extractor 提取器接口
type Extractor interface {
	Extract(ctx *ExtractorContext) (string, error)
}

// ======================== JSONPath 提取器 ========================

type JSONPathExtractor struct {
	path   string
	source config.ExtractorSource
}

func NewJSONPathExtractor(path string, source config.ExtractorSource) *JSONPathExtractor {
	if source == "" {
		source = config.ExtractorSourceResponse
	}
	return &JSONPathExtractor{path: path, source: source}
}

func (e *JSONPathExtractor) Extract(ctx *ExtractorContext) (string, error) {
	body, err := e.getBody(ctx)
	if err != nil {
		return "", err
	}

	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("解析JSON失败: %w", err)
	}

	result, err := jsonpath.JsonPathLookup(data, e.path)
	if err != nil {
		return "", fmt.Errorf("JSONPath提取失败: %w", err)
	}

	return convert.MustString(result), nil
}

func (e *JSONPathExtractor) getBody(ctx *ExtractorContext) ([]byte, error) {
	if e.source == config.ExtractorSourceRequest {
		if ctx.Request == nil || ctx.Request.Body == "" {
			return nil, fmt.Errorf("请求体为空")
		}
		return []byte(ctx.Request.Body), nil
	}

	if ctx.Response == nil || len(ctx.Response.Body) == 0 {
		return nil, fmt.Errorf("响应体为空")
	}
	return ctx.Response.Body, nil
}

// ======================== 正则表达式提取器 ========================

type RegexExtractor struct {
	pattern *regexp.Regexp
	source  config.ExtractorSource
}

func NewRegexExtractor(pattern string, source config.ExtractorSource) (*RegexExtractor, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("编译正则表达式失败: %w", err)
	}
	if source == "" {
		source = config.ExtractorSourceResponse
	}
	return &RegexExtractor{pattern: re, source: source}, nil
}

func (e *RegexExtractor) Extract(ctx *ExtractorContext) (string, error) {
	content := e.getContent(ctx)
	if content == "" {
		return "", fmt.Errorf("%s体为空", e.source)
	}

	matches := e.pattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return "", fmt.Errorf("正则表达式未匹配到数据")
	}

	return matches[1], nil
}

func (e *RegexExtractor) getContent(ctx *ExtractorContext) string {
	if e.source == config.ExtractorSourceRequest && ctx.Request != nil {
		return ctx.Request.Body
	}
	if ctx.Response != nil {
		return string(ctx.Response.Body)
	}
	return ""
}

// ======================== Header 提取器 ========================

type HeaderExtractor struct {
	headerName string
	source     config.ExtractorSource
}

func NewHeaderExtractor(headerName string, source config.ExtractorSource) *HeaderExtractor {
	if source == "" {
		source = config.ExtractorSourceResponse
	}
	return &HeaderExtractor{headerName: headerName, source: source}
}

func (e *HeaderExtractor) Extract(ctx *ExtractorContext) (string, error) {
	headers := e.getHeaders(ctx)
	if headers == nil {
		return "", fmt.Errorf("%s头为空", e.source)
	}

	value, exists := headers[e.headerName]
	if !exists {
		return "", fmt.Errorf("Header [%s] 不存在", e.headerName)
	}

	return value, nil
}

func (e *HeaderExtractor) getHeaders(ctx *ExtractorContext) map[string]string {
	if e.source == config.ExtractorSourceRequest && ctx.Request != nil {
		return ctx.Request.Headers
	}
	if ctx.Response != nil {
		return ctx.Response.Headers
	}
	return nil
}

// ======================== 表达式提取器 ========================

type ExpressionExtractor struct {
	tmpl *template.Template
}

func NewExpressionExtractor(expression string) (*ExpressionExtractor, error) {
	tmpl, err := template.New("expr").Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("解析表达式失败: %w", err)
	}
	return &ExpressionExtractor{tmpl: tmpl}, nil
}

func (e *ExpressionExtractor) Extract(ctx *ExtractorContext) (string, error) {
	if ctx.Variables == nil {
		return "", fmt.Errorf("变量上下文为空")
	}

	var buf bytes.Buffer
	if err := e.tmpl.Execute(&buf, ctx.Variables); err != nil {
		return "", fmt.Errorf("执行表达式失败: %w", err)
	}

	return buf.String(), nil
}

// ======================== 数据转换管道 ========================

type TransformPipeline struct {
	resolver *config.VariableResolver
}

func NewTransformPipeline(resolver *config.VariableResolver) *TransformPipeline {
	return &TransformPipeline{resolver: resolver}
}

func (p *TransformPipeline) Apply(value string, transforms []config.TransformConfig) (string, error) {
	result := value

	for i, transform := range transforms {
		transformed, err := p.applyTransform(result, transform)
		if err != nil {
			return "", fmt.Errorf("第 %d 个转换失败: %w", i+1, err)
		}
		result = transformed
	}

	return result, nil
}

func (p *TransformPipeline) applyTransform(value string, transform config.TransformConfig) (string, error) {
	// 模板方式
	if transform.Template != "" {
		data := map[string]interface{}{"value": value}
		for i, arg := range transform.Args {
			data[fmt.Sprintf("arg%d", i)] = arg
		}

		tmpl, err := template.New("transform").Parse(transform.Template)
		if err != nil {
			return "", fmt.Errorf("解析模板失败: %w", err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("执行模板失败: %w", err)
		}

		return buf.String(), nil
	}

	// 函数方式：直接使用 VariableResolver 执行函数
	if transform.Function != "" {
		if p.resolver == nil {
			return "", fmt.Errorf("VariableResolver 未初始化")
		}

		// 构造函数调用字符串：{{function "value" "arg1" "arg2"}}
		funcCall := fmt.Sprintf("{{%s %q", transform.Function, value)
		for _, arg := range transform.Args {
			funcCall += fmt.Sprintf(" %q", fmt.Sprint(arg))
		}
		funcCall += "}}"

		// 使用 VariableResolver 解析
		resolved, err := p.resolver.Resolve(funcCall)
		if err != nil {
			return "", fmt.Errorf("执行函数 %s 失败: %w", transform.Function, err)
		}
		return resolved, nil
	}

	return value, nil
}

// ======================== 提取器管理器 ========================

type ExtractorManager struct {
	extractors map[string]Extractor
	transforms map[string][]config.TransformConfig
	pipeline   *TransformPipeline
	logger     logger.ILogger
}

func NewExtractorManager(configs []config.ExtractorConfig, log logger.ILogger) (*ExtractorManager, error) {
	manager := &ExtractorManager{
		extractors: make(map[string]Extractor),
		transforms: make(map[string][]config.TransformConfig),
		pipeline:   NewTransformPipeline(config.NewVariableResolver()),
		logger:     log,
	}

	for _, cfg := range configs {
		extractor, err := createExtractor(cfg)
		if err != nil {
			return nil, fmt.Errorf("创建提取器 [%s] 失败: %w", cfg.Name, err)
		}
		manager.extractors[cfg.Name] = extractor

		// 保存转换配置
		if len(cfg.Transforms) > 0 {
			manager.transforms[cfg.Name] = cfg.Transforms
		}
	}

	return manager, nil
}

func createExtractor(cfg config.ExtractorConfig) (Extractor, error) {
	extractorType := cfg.Type
	if extractorType == "" {
		extractorType = types.ExtractorTypeJSONPath
	}

	source := cfg.Source
	if source == "" {
		source = config.ExtractorSourceResponse
	}

	switch extractorType {
	case types.ExtractorTypeJSONPath:
		if cfg.JSONPath == "" {
			return nil, fmt.Errorf("JSONPath不能为空")
		}
		return NewJSONPathExtractor(cfg.JSONPath, source), nil

	case types.ExtractorTypeRegex:
		if cfg.Regex == "" {
			return nil, fmt.Errorf("正则表达式不能为空")
		}
		return NewRegexExtractor(cfg.Regex, source)

	case types.ExtractorTypeHeader:
		if cfg.Header == "" {
			return nil, fmt.Errorf("Header名称不能为空")
		}
		return NewHeaderExtractor(cfg.Header, source), nil

	case types.ExtractorTypeExpression:
		if cfg.Expression == "" {
			return nil, fmt.Errorf("表达式不能为空")
		}
		return NewExpressionExtractor(cfg.Expression)

	default:
		return nil, fmt.Errorf("不支持的提取器类型: %s", extractorType)
	}
}

func (m *ExtractorManager) ExtractAll(ctx *ExtractorContext, defaultValues map[string]string) map[string]string {
	results := make(map[string]string)

	for name, extractor := range m.extractors {
		value, err := extractor.Extract(ctx)
		if err != nil {
			if defaultVal, exists := defaultValues[name]; exists {
				m.logger.Warn("提取变量 [%s] 失败，使用默认值: %s, 错误: %v", name, defaultVal, err)
				results[name] = defaultVal
			} else {
				m.logger.Warn("提取变量 [%s] 失败且无默认值: %v", name, err)
			}
			continue
		}

		// 应用转换
		if transforms, hasTransforms := m.transforms[name]; hasTransforms && len(transforms) > 0 {
			transformed, err := m.pipeline.Apply(value, transforms)
			if err != nil {
				m.logger.Warn("转换变量 [%s] 失败: %v，使用原始值", name, err)
			} else {
				// 先打日志再覆盖 value：顺序颠倒会把"转换前 -> 转换后"打成"后 -> 后"
				m.logger.Debug("转换变量 [%s]: %s -> %s", name, value, transformed)
				value = transformed
			}
		}

		results[name] = value
		m.logger.Debug("成功提取变量 [%s] = %s", name, value)
	}

	return results
}
