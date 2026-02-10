/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 16:58:03
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:16:10
 * @FilePath: \go-stress\executor\thinktime.go
 * @Description: 思考时间的等待实现
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/kamalyes/go-stress/config"
)

// thinkTimer 按配置计算并执行请求之间的等待
//
// 每个 Worker 独占一个实例，内含一个本地随机源：math/rand/v2 的全局函数虽已无锁，
// 但仍走 per-M 的 chacha8 状态；此处每请求最多取一个随机数，用本地 PCG 更直接，
// 也便于测试注入确定的种子
type thinkTimer struct {
	rng *rand.Rand
}

func newThinkTimer() *thinkTimer {
	return &thinkTimer{
		// 种子取自全局源，各 Worker 的序列因此互不相同
		rng: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
}

// wait 按 cfg 等待相应时长，返回 false 表示等待期间 ctx 已取消
//
// 必须响应 ctx：带 3 秒思考时间的压测若用裸 time.Sleep，Ctrl-C 之后最坏要等
// 满一个思考周期才退出，并发高时表现为"按了中断却迟迟不停"
func (t *thinkTimer) wait(ctx context.Context, cfg *config.ThinkTimeConfig) bool {
	d := t.duration(cfg)
	if d <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// duration 计算本次应等待的时长
func (t *thinkTimer) duration(cfg *config.ThinkTimeConfig) time.Duration {
	if cfg.IsZero() {
		return 0
	}

	switch cfg.Type {
	case config.ThinkTimeUniform:
		if cfg.Range <= 0 {
			return cfg.Delay
		}
		return cfg.Delay + time.Duration(t.rng.Int64N(int64(cfg.Range)))

	case config.ThinkTimeGaussian:
		if cfg.Deviation <= 0 {
			return cfg.Delay
		}
		d := cfg.Delay + time.Duration(t.rng.NormFloat64()*float64(cfg.Deviation))
		// 正态分布有一侧尾部为负，截断为 0 而非取绝对值：
		// 取绝对值会把负尾折回正侧，使实际均值高于配置的 Delay
		if d < 0 {
			return 0
		}
		return d

	default: // config.ThinkTimeConstant 及未识别的取值
		return cfg.Delay
	}
}
