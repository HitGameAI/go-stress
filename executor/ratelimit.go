/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:38:26
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:55:03
 * @FilePath: \go-stress\executor\ratelimit.go
 * @Description: 目标速率限流（定速压测）
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"sync"
	"time"
)

// RateLimiter 把全部 Worker 的发压速率约束到目标 QPS
//
// 为什么需要它：不限速的压测是「有多快打多快」，施加的压力取决于被测系统的
// 响应速度——系统一变慢，压力自己就降下来了，于是永远测不出「在固定 100 QPS
// 下延迟如何」。定速（开环）是容量规划与 SLO 验证的基本手段，也是 JMeter 的
// ConstantThroughputTimer 所表达的语义
//
// 实现取匀速发牌而非令牌桶：桶会允许攒够的令牌一次性放行，造成尖刺，
// 而压测要的是平稳的到达率。每张牌的时刻由序号推算（start + n*interval），
// 不做增量累加，避免误差逐次累积
type RateLimiter struct {
	interval time.Duration // 相邻两次放行的间隔

	mu   sync.Mutex
	next time.Time // 下一次允许发出请求的时刻
}

// NewRateLimiter 创建限流器，qps <= 0 时返回 nil 表示不限速
func NewRateLimiter(qps float64) *RateLimiter {
	if qps <= 0 {
		return nil
	}
	return &RateLimiter{
		interval: time.Duration(float64(time.Second) / qps),
	}
}

// QPS 返回配置的目标速率
func (r *RateLimiter) QPS() float64 {
	if r == nil {
		return 0
	}
	return float64(time.Second) / float64(r.interval)
}

// Wait 阻塞到允许发出下一个请求为止
//
// 返回 false 表示等待期间 ctx 已取消。nil 接收者直接放行，
// 使调用方无需在热路径上判空
func (r *RateLimiter) Wait(ctx context.Context) bool {
	if r == nil {
		return ctx.Err() == nil
	}

	now := time.Now()

	r.mu.Lock()
	// 首次调用以当下为起点
	if r.next.IsZero() {
		r.next = now
	}
	// 领到本次的发牌时刻，并把游标推到下一格
	//
	// 即使已经落后（next 远早于 now）也照常推进而不跳到 now：
	// 落后说明此前有请求被阻塞耽搁，补发这部分才能让长期平均速率回到目标值
	slot := r.next
	r.next = r.next.Add(r.interval)
	// 落后太多时重新对齐，否则一次长时间卡顿后会拼命补发造成尖刺
	if gap := now.Sub(r.next); gap > r.interval*10 {
		r.next = now
	}
	r.mu.Unlock()

	delay := slot.Sub(now)
	if delay <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
