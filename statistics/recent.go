/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 18:20:51
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 18:31:27
 * @FilePath: \go-stress\statistics\recent.go
 * @Description: 最近耗时的无锁环形缓冲
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"sync/atomic"
	"time"
)

// recentCapacity 保留的最近样本数，与实时图表展示的点数一致
const recentCapacity = 20

// recentDurations 最近若干次耗时的环形缓冲
//
// 实时报告的走势图只需要最后 20 个点。此前它是从那个随请求数无界增长的
// []float64 末尾切出来的——为了 20 个数字而留着上千万条记录
//
// 写入无锁：序号原子自增后写对应槽位。读取时理论上可能撞上某个槽位正被覆写
// 而拿到新旧混杂的一组值，但这是条装饰性的走势线，不值得为它在每个请求上
// 加一把锁；且各槽位本身是原子读写，不存在内存不安全
type recentDurations struct {
	slots [recentCapacity]atomic.Int64
	seq   atomic.Uint64
}

func newRecentDurations() *recentDurations {
	return &recentDurations{}
}

// record 记入一次耗时
func (r *recentDurations) record(d time.Duration) {
	n := r.seq.Add(1) - 1
	r.slots[n%recentCapacity].Store(d.Nanoseconds())
}

// snapshot 返回最近的耗时，按时间先后排列（最早在前）
func (r *recentDurations) snapshot() []time.Duration {
	total := r.seq.Load()
	if total == 0 {
		return nil
	}

	n := uint64(recentCapacity)
	if total < n {
		n = total
	}

	out := make([]time.Duration, 0, n)
	// 从最早的那个仍在缓冲内的样本开始，按顺序读到最新
	for i := total - n; i < total; i++ {
		out = append(out, time.Duration(r.slots[i%recentCapacity].Load()))
	}
	return out
}
