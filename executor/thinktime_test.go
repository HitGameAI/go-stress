/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:06:19
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:19:02
 * @FilePath: \go-stress\executor\thinktime_test.go
 * @Description: 思考时间测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"testing"
	"time"

	"github.com/kamalyes/go-stress/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThinkTimerDuration(t *testing.T) {
	tt := newThinkTimer()

	t.Run("未配置时为零", func(t *testing.T) {
		assert.Zero(t, tt.duration(nil))
		assert.Zero(t, tt.duration((&config.ThinkTimeConfig{}).Normalize()))
	})

	t.Run("constant 恒为 Delay", func(t *testing.T) {
		cfg := (&config.ThinkTimeConfig{Delay: 50 * time.Millisecond}).Normalize()
		for i := 0; i < 100; i++ {
			assert.Equal(t, 50*time.Millisecond, tt.duration(cfg))
		}
	})

	t.Run("uniform 落在 [Delay, Delay+Range)", func(t *testing.T) {
		cfg := (&config.ThinkTimeConfig{
			Type:  config.ThinkTimeUniform,
			Delay: 10 * time.Millisecond,
			Range: 20 * time.Millisecond,
		}).Normalize()

		sawAbove := false
		for i := 0; i < 1000; i++ {
			d := tt.duration(cfg)
			require.GreaterOrEqual(t, d, 10*time.Millisecond)
			require.Less(t, d, 30*time.Millisecond)
			if d > 10*time.Millisecond {
				sawAbove = true
			}
		}
		assert.True(t, sawAbove, "随机区间应当真的被用到，而非恒等于下界")
	})

	t.Run("gaussian 不为负且均值接近 Delay", func(t *testing.T) {
		cfg := (&config.ThinkTimeConfig{
			Type:      config.ThinkTimeGaussian,
			Delay:     100 * time.Millisecond,
			Deviation: 10 * time.Millisecond,
		}).Normalize()

		var total time.Duration
		const n = 5000
		for i := 0; i < n; i++ {
			d := tt.duration(cfg)
			require.GreaterOrEqual(t, d, time.Duration(0), "时长不得为负")
			total += d
		}
		mean := total / n
		// 偏差 10ms、样本 5000，均值应稳定落在 100ms 附近
		assert.InDelta(t, float64(100*time.Millisecond), float64(mean),
			float64(3*time.Millisecond), "均值应接近配置的 Delay")
	})

	t.Run("缺少随机参数时退化为 constant", func(t *testing.T) {
		u := (&config.ThinkTimeConfig{Type: config.ThinkTimeUniform, Delay: 7 * time.Millisecond}).Normalize()
		assert.Equal(t, 7*time.Millisecond, tt.duration(u))
		g := (&config.ThinkTimeConfig{Type: config.ThinkTimeGaussian, Delay: 7 * time.Millisecond}).Normalize()
		assert.Equal(t, 7*time.Millisecond, tt.duration(g))
	})
}

// TestThinkTimerWaitRespectsContext 等待必须可被 ctx 中断
//
// 用裸 time.Sleep 的话，带数秒思考时间的压测在 Ctrl-C 之后最坏要等满一个思考
// 周期才退出，并发高时表现为「按了中断却迟迟不停」
func TestThinkTimerWaitRespectsContext(t *testing.T) {
	tt := newThinkTimer()
	cfg := (&config.ThinkTimeConfig{Delay: 10 * time.Second}).Normalize()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	ok := tt.wait(ctx, cfg)
	elapsed := time.Since(start)

	assert.False(t, ok, "ctx 取消后应返回 false")
	assert.Less(t, elapsed, 2*time.Second, "应在取消时立刻返回，而非睡满 10 秒")
}

func TestThinkTimerWaitCompletes(t *testing.T) {
	tt := newThinkTimer()
	cfg := (&config.ThinkTimeConfig{Delay: 20 * time.Millisecond}).Normalize()

	start := time.Now()
	ok := tt.wait(context.Background(), cfg)
	elapsed := time.Since(start)

	assert.True(t, ok)
	assert.GreaterOrEqual(t, elapsed, 20*time.Millisecond, "应当真的等待了配置的时长")
}
