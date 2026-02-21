/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-21 10:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-21 10:00:00
 * @FilePath: \go-stress\statistics\api_stats_test.go
 * @Description: per-API 聚合器单元测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIAggregatorStats(t *testing.T) {
	a := newAPIAggregator()

	// api-1：10 个成功请求（2ms）+ 2 个失败请求（10ms）
	for i := 0; i < 10; i++ {
		a.record("api-1", &RequestResult{Success: true, Duration: 2 * time.Millisecond})
	}
	for i := 0; i < 2; i++ {
		a.record("api-1", &RequestResult{Success: false, Duration: 10 * time.Millisecond, Error: errors.New("boom")})
	}
	// api-2：5 个成功请求（4ms）+ 1 个跳过
	for i := 0; i < 5; i++ {
		a.record("api-2", &RequestResult{Success: true, Duration: 4 * time.Millisecond})
	}
	a.record("api-2", &RequestResult{Skipped: true, Duration: time.Millisecond})

	stats := a.stats(2 * time.Second)
	require.Len(t, stats, 2)

	// 按请求数降序：api-1（12 个）在前
	assert.Equal(t, "api-1", stats[0].Name)
	assert.EqualValues(t, 12, stats[0].Requests)
	assert.EqualValues(t, 10, stats[0].Success)
	assert.EqualValues(t, 2, stats[0].Failed)
	assert.InDelta(t, 83.3333, stats[0].SuccessRate, 0.01) // 10/12
	assert.InDelta(t, 3.3333, stats[0].AvgMs, 0.001)      // (10*2+2*10)/12
	assert.EqualValues(t, 6, stats[0].QPS)               // 12 / 2 秒
	assert.GreaterOrEqual(t, stats[0].MaxMs, stats[0].MinMs)

	// 跳过不计入成功率分母
	assert.Equal(t, "api-2", stats[1].Name)
	assert.EqualValues(t, 6, stats[1].Requests)
	assert.EqualValues(t, 1, stats[1].Skipped)
	assert.InDelta(t, 100.0, stats[1].SuccessRate, 1e-9) // 5/5，跳过被剔除
	assert.EqualValues(t, 3, stats[1].QPS)               // 6 / 2 秒
}

func TestAPIAggregatorConcurrent(t *testing.T) {
	a := newAPIAggregator()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				a.record("api", &RequestResult{Success: true, Duration: time.Millisecond})
			}
		}()
	}
	wg.Wait()

	stats := a.stats(time.Second)
	require.Len(t, stats, 1)
	assert.EqualValues(t, 800, stats[0].Requests)
	assert.EqualValues(t, 800, stats[0].Success)
}

func TestAPIAggregatorEmpty(t *testing.T) {
	a := newAPIAggregator()
	assert.Nil(t, a.stats(time.Second))
}

func TestCollectAPIStats(t *testing.T) {
	// Collect 会写存储，这里给一个真实的内存存储
	c := NewCollector(storage.NewMemoryStorage("test-node", logger.NewLogger()), nil)

	for i := 0; i < 3; i++ {
		c.Collect(&RequestResult{Success: true, Duration: 2 * time.Millisecond, APIName: "login"})
	}
	// 无 API 名的请求不参与聚合，但计入全局
	c.Collect(&RequestResult{Success: true, Duration: time.Millisecond})

	report := NewReportBuilder(c).BuildReport(time.Second, false)
	require.Len(t, report.APIStats, 1)
	assert.Equal(t, "login", report.APIStats[0].Name)
	assert.EqualValues(t, 3, report.APIStats[0].Requests)
	assert.EqualValues(t, 4, report.TotalRequests)
}
