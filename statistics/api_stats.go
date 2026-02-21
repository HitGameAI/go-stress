/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-21 10:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-21 10:00:00
 * @FilePath: \go-stress\statistics\api_stats.go
 * @Description: 按 API 名聚合的统计——多接口场景下每个接口各自的延迟与成功率
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// APIStat 单个 API 的聚合统计（JSON 中耗时一律为毫秒，与 Report 其他字段口径一致）
type APIStat struct {
	Name        string  `json:"name"`         // API 名称
	Requests    uint64  `json:"requests"`    // 总请求数
	Success     uint64  `json:"success"`     // 成功数
	Failed      uint64  `json:"failed"`      // 失败数
	Skipped     uint64  `json:"skipped"`     // 跳过数
	SuccessRate float64 `json:"success_rate"` // 成功率（百分比，分母不含跳过）
	AvgMs       float64 `json:"avg_ms"`      // 平均耗时（毫秒）
	MinMs       float64 `json:"min_ms"`      // 最小耗时（毫秒）
	MaxMs       float64 `json:"max_ms"`      // 最大耗时（毫秒）
	P50Ms       float64 `json:"p50_ms"`      // P50（毫秒）
	P95Ms       float64 `json:"p95_ms"`      // P95（毫秒）
	P99Ms       float64 `json:"p99_ms"`      // P99（毫秒）
	QPS         float64 `json:"qps"`         // 该 API 的 QPS
}

// apiAgg 单个 API 的原子聚合器
type apiAgg struct {
	requests   atomic.Uint64
	success    atomic.Uint64
	failed     atomic.Uint64
	skipped    atomic.Uint64
	totalNanos atomic.Int64
	minNanos   atomic.Int64
	maxNanos   atomic.Int64
	latency    *LatencyHistogram
}

// apiAggregator 按 API 名聚合，热路径无锁
type apiAggregator struct {
	apis *syncx.Map[string, *apiAgg]
}

// newAPIAggregator 创建聚合器
func newAPIAggregator() *apiAggregator {
	return &apiAggregator{apis: syncx.NewMap[string, *apiAgg]()}
}

// record 记录一次请求结果
//
// 热路径上一次 map 读取加若干原子自增；首个请求为该 API 建聚合器时
// 并发 LoadOrStore 可能各建一份，只有先到者被保留，被丢弃的不会被写入
func (a *apiAggregator) record(name string, result *RequestResult) {
	agg, ok := a.apis.Load(name)
	if !ok {
		newAgg := &apiAgg{latency: NewLatencyHistogram()}
		newAgg.minNanos.Store(math.MaxInt64)
		agg, _ = a.apis.LoadOrStore(name, newAgg)
	}

	agg.requests.Add(1)
	if result.Skipped {
		agg.skipped.Add(1)
	} else if result.Success {
		agg.success.Add(1)
	} else {
		agg.failed.Add(1)
	}

	nanos := result.Duration.Nanoseconds()
	agg.totalNanos.Add(nanos)
	agg.latency.Record(result.Duration)
	storeMin(&agg.minNanos, nanos)
	storeMax(&agg.maxNanos, nanos)
}

// stats 生成聚合统计切片（按请求数降序；totalTime 用于计算各 API 的 QPS）
func (a *apiAggregator) stats(totalTime time.Duration) []APIStat {
	m := a.apis.ToMap()
	if len(m) == 0 {
		return nil
	}

	seconds := totalTime.Seconds()
	stats := make([]APIStat, 0, len(m))
	for name, agg := range m {
		reqs := agg.requests.Load()
		if reqs == 0 {
			continue
		}

		s := APIStat{
			Name:     name,
			Requests: reqs,
			Success:  agg.success.Load(),
			Failed:   agg.failed.Load(),
			Skipped:  agg.skipped.Load(),
		}
		// 成功率的分母不含跳过：跳过不是该接口的真实结果
		if done := s.Success + s.Failed; done > 0 {
			s.SuccessRate = float64(s.Success) / float64(done) * 100
		}
		s.AvgMs = float64(agg.totalNanos.Load()) / float64(reqs) / 1e6
		if mn := agg.minNanos.Load(); mn != math.MaxInt64 {
			s.MinMs = float64(mn) / 1e6
		}
		s.MaxMs = float64(agg.maxNanos.Load()) / 1e6
		ps := agg.latency.Percentiles(50, 95, 99)
		s.P50Ms, s.P95Ms, s.P99Ms = durationMs(ps[0]), durationMs(ps[1]), durationMs(ps[2])
		if seconds > 0 {
			s.QPS = float64(reqs) / seconds
		}
		stats = append(stats, s)
	}

	// 请求数多的 API 排前面，长尾接口不至于被挤到看不见
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Requests > stats[j].Requests
	})
	return stats
}
