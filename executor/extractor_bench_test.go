/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 15:30:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 16:05:17
 * @FilePath: \go-stress\executor\extractor_bench_test.go
 * @Description: 提取器热路径基准测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"testing"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/types"
)

// benchExtractorConfigs 一组典型的提取器配置：JSONPath + 正则 + 响应头
func benchExtractorConfigs() []config.ExtractorConfig {
	return []config.ExtractorConfig{
		{
			Name:     "token",
			Type:     types.ExtractorTypeJSONPath,
			JSONPath: "$.data.token",
			Source:   config.ExtractorSourceResponse,
		},
		{
			Name:   "user_id",
			Type:   types.ExtractorTypeRegex,
			Regex:  `"user_id"\s*:\s*"([^"]+)"`,
			Source: config.ExtractorSourceResponse,
		},
		{
			Name:   "request_id",
			Type:   types.ExtractorTypeHeader,
			Header: "X-Request-ID",
			Source: config.ExtractorSourceResponse,
		},
	}
}

func benchExtractorContext() *ExtractorContext {
	return &ExtractorContext{
		Request: &Request{Body: "action=login"},
		Response: &types.Response{
			Body:    []byte(`{"data":{"token":"tok-abc123","user_id":"12345"}}`),
			Headers: map[string]string{"X-Request-ID": "rid-0001"},
		},
		Variables: map[string]string{},
	}
}

// BenchmarkNewExtractorManager 单独测量构造成本
//
// 这是从热路径上移除的那部分：内部 NewVariableResolver 要建一张近百项的
// template.FuncMap，正则型提取器还要 regexp.Compile
func BenchmarkNewExtractorManager(b *testing.B) {
	configs := benchExtractorConfigs()
	log := logger.NewEmptyLogger()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewExtractorManager(configs, log); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExtractAll 单独测量提取本身的成本，作为对照的分母
func BenchmarkExtractAll(b *testing.B) {
	configs := benchExtractorConfigs()
	manager, err := NewExtractorManager(configs, logger.NewEmptyLogger())
	if err != nil {
		b.Fatal(err)
	}
	ctx := benchExtractorContext()
	defaults := map[string]string{}

	b.ReportAllocs()
	for b.Loop() {
		manager.ExtractAll(ctx, defaults)
	}
}

// BenchmarkExtractPerRequestConstruct 修复前的形状：每个请求重建一次管理器
func BenchmarkExtractPerRequestConstruct(b *testing.B) {
	configs := benchExtractorConfigs()
	log := logger.NewEmptyLogger()
	ctx := benchExtractorContext()
	defaults := map[string]string{}

	b.ReportAllocs()
	for b.Loop() {
		manager, err := NewExtractorManager(configs, log)
		if err != nil {
			b.Fatal(err)
		}
		manager.ExtractAll(ctx, defaults)
	}
}

// BenchmarkExtractCachedManager 修复后的形状：管理器按 API 名缓存后复用
func BenchmarkExtractCachedManager(b *testing.B) {
	configs := benchExtractorConfigs()
	log := logger.NewEmptyLogger()
	ctx := benchExtractorContext()
	defaults := map[string]string{}

	cache := make(map[string]*ExtractorManager)
	getManager := func(name string) (*ExtractorManager, error) {
		if m, ok := cache[name]; ok {
			return m, nil
		}
		m, err := NewExtractorManager(configs, log)
		if err != nil {
			return nil, err
		}
		cache[name] = m
		return m, nil
	}

	b.ReportAllocs()
	for b.Loop() {
		manager, err := getManager("login")
		if err != nil {
			b.Fatal(err)
		}
		manager.ExtractAll(ctx, defaults)
	}
}

// benchDepCtxSink 防止编译器把下面基准中未逃逸的上下文整体优化掉
//
// 不设这个汇点时 BenchmarkWorkerDepContextNew 会报 0 allocs/op、11.9 ns/op：
// 局部变量不逃逸，两个 map 被编译器消除，测出来的是"什么都没做"的成本
// 真实场景里该上下文会传给 RequestContext 与 VariableReplacer，必然逃逸
var benchDepCtxSink *WorkerDependencyContext

// BenchmarkWorkerDepContextNew 修复前：每轮请求新建依赖上下文（两次 map 分配）
func BenchmarkWorkerDepContextNew(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		benchDepCtxSink = NewWorkerDependencyContext()
	}
}

// BenchmarkWorkerDepContextReset 修复后：原地清空复用
//
// 该路径每个请求都会走到（与是否配置提取器无关），故单次成本虽小但乘以总请求数
func BenchmarkWorkerDepContextReset(b *testing.B) {
	ctx := NewWorkerDependencyContext()

	b.ReportAllocs()
	for b.Loop() {
		ctx.reset()
	}
}
