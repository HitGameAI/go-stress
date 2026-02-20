/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-20 09:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-20 09:00:00
 * @FilePath: \go-stress\statistics\timeseries.go
 * @Description: 秒级时序聚合器——报告页时间轴曲线的数据源
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// timeSeriesMaxPoints 时序缓冲的点数上限；到达上限后自动两两合并、窗口翻倍，
// 内存恒为 O(timeSeriesMaxPoints)，小时级、天级的压测也不会膨胀
const timeSeriesMaxPoints = 3600

// TimeSeriesPoint 单个采样窗口的指标
type TimeSeriesPoint struct {
	T   int64   `json:"t"`   // 窗口起点（Unix 秒）
	Req uint64  `json:"req"` // 窗口内完成请求数
	Err uint64  `json:"err"` // 窗口内失败请求数
	QPS float64 `json:"qps"` // 窗口内平均 QPS = Req / 窗口秒数
	Avg float64 `json:"avg"` // 窗口内平均耗时（毫秒）
	P50 float64 `json:"p50"` // 窗口内 P50（毫秒）
	P95 float64 `json:"p95"` // 窗口内 P95（毫秒）
	P99 float64 `json:"p99"` // 窗口内 P99（毫秒）
}

// TimeSeriesData 时序数据（副本形式交给报告层序列化）
type TimeSeriesData struct {
	IntervalSec int64             `json:"interval_sec"` // 当前采样窗口的秒数（降采样后翻倍）
	Points      []TimeSeriesPoint `json:"points"`
}

// timeSeriesRecorder 秒级时序记录器
//
// 采集方式是「定时差分」而非在热路径上逐请求记账：每秒把 Collector 的原子
// 计数器与直方图快照同上一秒做差分，得到该秒的增量分布，据此算出该秒的
// QPS 与延迟百分位。热路径零新增代价，采样只发生在唯一的 ticker 协程里
type timeSeriesRecorder struct {
	collector *Collector
	mu        *syncx.RWLock
	points    []TimeSeriesPoint
	interval  int64 // 当前采样窗口的秒数

	// 差分用的三个定长缓冲，构造时一次分配，稳态零分配
	base []uint64 // 上一窗口的全量快照（差分基线）
	cur  []uint64 // 本窗口的全量快照
	diff []uint64 // 本窗口的增量分布（= cur - base）

	// 差分基线：上一窗口的计数器值
	lastTotal  uint64
	lastFailed uint64
	lastNanos  int64

	started  atomic.Bool
	stopOnce sync.Once
	stopped  chan struct{}
}

// newTimeSeriesRecorder 创建记录器并取当下状态作差分基线
//
// 基线在构造期取好而非首次 tick 再取：压测早已开始时（如 Slave 复用
// Collector 的场景）延迟接入也不至于把历史全量算进第一个窗口
func newTimeSeriesRecorder(c *Collector) *timeSeriesRecorder {
	r := &timeSeriesRecorder{
		collector: c,
		mu:        syncx.NewRWLock(),
		interval:  1,
		stopped:   make(chan struct{}),
	}
	r.base = c.latency.snapshotInto(nil)
	r.lastTotal = c.totalRequests.Load()
	r.lastFailed = c.failedRequests.Load()
	r.lastNanos = c.totalDurationNanos.Load()
	return r
}

// run 每秒采样一次，直到 stop 被调用或外部 ctx 结束
func (r *timeSeriesRecorder) run(ctx context.Context) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case t := <-ticker.C:
			r.tick(t)
		case <-r.stopped:
			r.tick(time.Now()) // 补采压测结束后不足一秒的尾巴
			return
		case <-ctx.Done():
			r.tick(time.Now()) // 用户中断同样补采，曲线保留中断时刻的形态
			return
		}
	}
}

// tick 采样一次：差分出本窗口的增量并落成一个点
func (r *timeSeriesRecorder) tick(now time.Time) {
	c := r.collector
	total := c.totalRequests.Load()
	failed := c.failedRequests.Load()
	nanos := c.totalDurationNanos.Load()

	// 快照当前全量直方图，与基线做差得到本窗口的增量分布
	cur := c.latency.snapshotInto(r.cur)
	for i := range cur {
		if cur[i] >= r.base[i] {
			r.diff[i] = cur[i] - r.base[i]
		} else {
			// 桶计数只会递增，读到回退只可能是撞上了并发写，按 0 处理
			r.diff[i] = 0
		}
	}

	p := TimeSeriesPoint{
		T:   now.Unix() - r.interval,
		Req: total - r.lastTotal,
		Err: failed - r.lastFailed,
	}
	if p.Req > 0 {
		p.QPS = float64(p.Req) / float64(r.interval)
		p.Avg = float64(nanos-r.lastNanos) / float64(p.Req) / 1e6
		ps := percentilesFromSnapshot(r.diff, 50, 95, 99)
		p.P50, p.P95, p.P99 = durationMs(ps[0]), durationMs(ps[1]), durationMs(ps[2])
	}

	r.appendPoint(p)

	// 滚动基线：本窗口快照成为下一窗口的差分起点，两个缓冲互换复用
	r.base, r.cur = cur, r.base
	r.lastTotal, r.lastFailed, r.lastNanos = total, failed, nanos
}

// appendPoint 追加一个采样点，点数达到上限时降采样
func (r *timeSeriesRecorder) appendPoint(p TimeSeriesPoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.points = append(r.points, p)
	if len(r.points) >= timeSeriesMaxPoints {
		r.downsampleLocked()
	}
}

// downsampleLocked 点数达到上限时两两合并、窗口翻倍
//
// 百分位取两窗口的较大值：合并窗口的真实百分位介于两者之间，
// 这与直方图取桶上界同理——宁可保守也不低报
func (r *timeSeriesRecorder) downsampleLocked() {
	n := len(r.points)
	half := n / 2
	for i := 0; i < half; i++ {
		r.points[i] = mergeTimePoints(r.points[2*i], r.points[2*i+1], r.interval*2)
	}
	// 奇数个点时保留最后一个未配对的点，它将在下一轮与新点配对
	if n%2 == 1 {
		r.points[half] = r.points[n-1]
		half++
	}
	r.points = r.points[:half]
	r.interval *= 2
}

// mergeTimePoints 合并两个相邻采样点（interval 为合并后的窗口秒数）
func mergeTimePoints(a, b TimeSeriesPoint, interval int64) TimeSeriesPoint {
	m := TimeSeriesPoint{
		T:   a.T,
		Req: a.Req + b.Req,
		Err: a.Err + b.Err,
	}
	m.QPS = float64(m.Req) / float64(interval)
	if m.Req > 0 {
		// 平均耗时按请求数加权；百分位取较大值（理由见 downsampleLocked）
		m.Avg = (a.Avg*float64(a.Req) + b.Avg*float64(b.Req)) / float64(m.Req)
	}
	m.P50 = math.Max(a.P50, b.P50)
	m.P95 = math.Max(a.P95, b.P95)
	m.P99 = math.Max(a.P99, b.P99)
	return m
}

// stop 停止采样（幂等）
func (r *timeSeriesRecorder) stop() {
	r.stopOnce.Do(func() { close(r.stopped) })
}

// data 返回时序数据副本（无数据时为 nil）
func (r *timeSeriesRecorder) data() *TimeSeriesData {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.points) == 0 {
		return nil
	}
	points := make([]TimeSeriesPoint, len(r.points))
	copy(points, r.points)
	return &TimeSeriesData{IntervalSec: r.interval, Points: points}
}

// durationMs 把耗时转成毫秒浮点数（报告 JSON 的统一口径）
func durationMs(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1e6
}
