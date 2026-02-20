/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-20 09:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-20 09:00:00
 * @FilePath: \go-stress\statistics\timeseries_test.go
 * @Description: 时序聚合器单元测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCollector 构造仅用于统计逻辑测试的收集器（无存储、无日志）
func newTestCollector() *Collector {
	return NewCollector(nil, nil)
}

func TestTimeSeriesRecorderTick(t *testing.T) {
	c := newTestCollector()
	rec := newTimeSeriesRecorder(c)

	// 模拟第一个窗口内产生的 10 个成功请求，每个 2ms
	for i := 0; i < 10; i++ {
		c.latency.Record(2 * time.Millisecond)
		c.totalRequests.Add(1)
		c.successRequests.Add(1)
	}
	c.totalDurationNanos.Add(int64(10 * 2 * time.Millisecond))

	rec.tick(time.Now())

	data := rec.data()
	require.NotNil(t, data)
	require.Len(t, data.Points, 1)

	p := data.Points[0]
	assert.EqualValues(t, 10, p.Req)
	assert.EqualValues(t, 0, p.Err)
	assert.InDelta(t, 10.0, p.QPS, 1e-9)   // 10 请求 / 1 秒
	assert.InDelta(t, 2.0, p.Avg, 0.001)   // 平均 2ms
	assert.Greater(t, p.P50, 0.0)          // 百分位取桶上界，≥ 真实值
	assert.InDelta(t, p.P50, 2.0, 0.3)     // 全部 2ms 时 P50 落在 2ms 附近桶
	assert.GreaterOrEqual(t, p.P99, p.P50) // 百分位单调不减

	// 第二个窗口无新请求：产生一个全零点，且不污染基线
	rec.tick(time.Now().Add(time.Second))
	data = rec.data()
	require.Len(t, data.Points, 2)
	assert.EqualValues(t, 0, data.Points[1].Req)
	assert.EqualValues(t, 0, data.Points[1].QPS)

	// 第三个窗口再来 5 个请求：差分基线正确，只统计增量
	for i := 0; i < 5; i++ {
		c.latency.Record(3 * time.Millisecond)
		c.totalRequests.Add(1)
		c.successRequests.Add(1)
	}
	c.totalDurationNanos.Add(int64(5 * 3 * time.Millisecond))

	rec.tick(time.Now().Add(2 * time.Second))
	data = rec.data()
	require.Len(t, data.Points, 3)
	assert.EqualValues(t, 5, data.Points[2].Req)
	assert.InDelta(t, 5.0, data.Points[2].QPS, 1e-9)
	assert.InDelta(t, 3.0, data.Points[2].Avg, 0.001)
}

func TestMergeTimePoints(t *testing.T) {
	a := TimeSeriesPoint{Req: 10, QPS: 10, Avg: 2, P50: 1, P95: 3, P99: 5}
	b := TimeSeriesPoint{Req: 30, QPS: 30, Avg: 4, P50: 2, P95: 6, P99: 9}

	m := mergeTimePoints(a, b, 2)
	assert.EqualValues(t, 40, m.Req)
	assert.InDelta(t, 20.0, m.QPS, 1e-9) // 40 / 2 秒
	assert.InDelta(t, 3.5, m.Avg, 1e-9)  // 按请求数加权
	assert.InDelta(t, 2.0, m.P50, 1e-9)  // 百分位取较大值，不低报
	assert.InDelta(t, 6.0, m.P95, 1e-9)
	assert.InDelta(t, 9.0, m.P99, 1e-9)
}

func TestTimeSeriesRecorderDownsample(t *testing.T) {
	r := &timeSeriesRecorder{interval: 1}
	r.points = make([]TimeSeriesPoint, 5)
	for i := range r.points {
		r.points[i] = TimeSeriesPoint{Req: 1, QPS: 1, Avg: 1, P99: 1}
	}

	r.downsampleLocked()

	// 5 个点两两合并后剩 3 个（2 个合并点 + 1 个未配对的尾巴），窗口翻倍
	assert.Len(t, r.points, 3)
	assert.EqualValues(t, 2, r.interval)
	assert.EqualValues(t, 2, r.points[0].Req)
	assert.EqualValues(t, 1, r.points[2].Req) // 尾巴点原样保留
}

func TestTimeSeriesStopIsIdempotent(t *testing.T) {
	c := newTestCollector()
	rec := newTimeSeriesRecorder(c)

	rec.stop() // 未启动时 stop 不应 panic
	rec.stop() // 重复 stop 幂等
	assert.NotNil(t, rec)
}

func TestCollectorTimeSeriesWithoutStart(t *testing.T) {
	c := newTestCollector()

	// 未启动记录时，时序数据为 nil 而非空指针
	assert.Nil(t, c.TimeSeries())
	assert.NotPanics(t, func() { c.StopTimeSeries() })
}

func TestPercentilesFromSnapshotMatchesPercentiles(t *testing.T) {
	h := NewLatencyHistogram()
	for i := 0; i < 100; i++ {
		h.Record(time.Duration(i+1) * time.Millisecond)
	}

	snap := h.snapshotInto(nil)
	got := percentilesFromSnapshot(snap, 50, 90, 95, 99)
	want := h.Percentiles(50, 90, 95, 99)

	// 快照版算法与原方法在同一份直方图上结果一致
	for i := range got {
		assert.Equal(t, want[i], got[i])
	}

	// 快照缓冲可复用：再次写入不重新分配、结果一致
	snap2 := h.snapshotInto(snap)
	assert.Equal(t, cap(snap), cap(snap2))
	got2 := percentilesFromSnapshot(snap2, 50, 90, 95, 99)
	for i := range got2 {
		assert.Equal(t, want[i], got2[i])
	}
}
