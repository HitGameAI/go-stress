/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:39:11
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:56:20
 * @FilePath: \go-stress\executor\ratelimit_test.go
 * @Description: 速率限流测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRateLimiter(t *testing.T) {
	t.Run("非正速率表示不限速", func(t *testing.T) {
		assert.Nil(t, NewRateLimiter(0))
		assert.Nil(t, NewRateLimiter(-1))
	})

	t.Run("nil 接收者直接放行", func(t *testing.T) {
		var r *RateLimiter
		assert.Zero(t, r.QPS())
		assert.True(t, r.Wait(context.Background()), "未配置限速时不应阻塞")
	})

	t.Run("QPS 可还原", func(t *testing.T) {
		assert.InDelta(t, 200.0, NewRateLimiter(200).QPS(), 0.01)
	})
}

// TestRateLimiterPacesAcrossWorkers 多个并发共享同一速率
//
// 限流器必须是全局的：若每个 Worker 各限 100 QPS，10 个并发就会打出 1000 QPS，
// 定速压测的前提就不成立了
func TestRateLimiterPacesAcrossWorkers(t *testing.T) {
	const (
		qps     = 200.0
		workers = 8
		total   = 160 // 期望耗时 160/200 = 800ms
	)

	limiter := NewRateLimiter(qps)
	ctx := context.Background()

	var wg sync.WaitGroup
	each := total / workers
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				require.True(t, limiter.Wait(ctx))
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	expected := time.Duration(float64(total) / qps * float64(time.Second))
	// 首个请求立即放行，故实际约为 (total-1)/qps；放宽到 ±35% 以容纳调度抖动
	assert.InDelta(t, float64(expected), float64(elapsed), float64(expected)*0.35,
		"%d 个请求按 %.0f QPS 应耗时约 %s，实际 %s", total, qps, expected, elapsed)
}

// TestRateLimiterSmoothNotBursty 放行应当匀速，而非攒够一批齐发
//
// 令牌桶会让积攒的令牌一次性放行，造成到达率尖刺；定速压测要的是平稳到达
func TestRateLimiterSmoothNotBursty(t *testing.T) {
	limiter := NewRateLimiter(100) // 每 10ms 一个
	ctx := context.Background()

	// 先空转一段时间，令牌桶实现会在此期间"攒票"
	time.Sleep(120 * time.Millisecond)

	start := time.Now()
	for i := 0; i < 10; i++ {
		require.True(t, limiter.Wait(ctx))
	}
	elapsed := time.Since(start)

	// 匀速实现下这 10 个请求仍需约 90ms；若是令牌桶，攒下的票会让它瞬间放完
	assert.Greater(t, elapsed, 50*time.Millisecond,
		"静默期后不应允许突发齐发，实际 %s", elapsed)
}

// TestRateLimiterRespectsContext 等待必须可被取消
func TestRateLimiterRespectsContext(t *testing.T) {
	limiter := NewRateLimiter(0.2) // 每 5 秒一个
	ctx, cancel := context.WithCancel(context.Background())

	require.True(t, limiter.Wait(ctx), "首个请求应立即放行")

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	ok := limiter.Wait(ctx)
	elapsed := time.Since(start)

	assert.False(t, ok, "ctx 取消后应返回 false")
	assert.Less(t, elapsed, time.Second, "应立刻返回而非等满 5 秒，实际 %s", elapsed)
}

// TestRateLimiterRealignsAfterStall 长时间卡顿后不应拼命补发
func TestRateLimiterRealignsAfterStall(t *testing.T) {
	limiter := NewRateLimiter(1000) // 每 1ms 一个
	ctx := context.Background()

	require.True(t, limiter.Wait(ctx))
	// 模拟一次长卡顿：此时游标已落后约 200 格
	time.Sleep(200 * time.Millisecond)

	// 落后超过 10 格时应重新对齐，于是后续请求仍按 1ms 的间隔走，
	// 而不是先无延迟地补发 200 个
	start := time.Now()
	for i := 0; i < 50; i++ {
		require.True(t, limiter.Wait(ctx))
	}
	elapsed := time.Since(start)

	assert.Greater(t, elapsed, 20*time.Millisecond,
		"卡顿后应重新对齐并维持间隔，而非无延迟补发，实际 %s", elapsed)
}
