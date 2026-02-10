/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 18:15:33
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 18:32:19
 * @FilePath: \go-stress\statistics\histogram_test.go
 * @Description: 延迟直方图测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxRelErr 分桶带来的相对误差上界，取自实现本身声明的值
// 不在测试里另算一遍：那样只会验证两处算法一致，而非验证实际行为达标
const maxRelErr = histMaxRelErr

// TestHistIndexMonotonicAndBounded 下标映射必须单调且落在界内
func TestHistIndexMonotonicAndBounded(t *testing.T) {
	prev := -1
	for _, v := range []uint64{0, 1, 63, 127, 128, 129, 255, 256, 1000, 1 << 20, 1 << 30, 1 << 40} {
		idx := histIndex(v)
		require.GreaterOrEqual(t, idx, 0, "v=%d", v)
		require.Less(t, idx, histBuckets, "v=%d", v)
		assert.GreaterOrEqual(t, idx, prev, "下标须随取值单调不减，v=%d", v)
		prev = idx
	}
}

// TestHistUpperBoundNeverUnderstates 桶上界不得低于落入该桶的值
//
// 「P99 = 5ms」必须是一句真话；取中点会让这句话有可能不成立
func TestHistUpperBoundNeverUnderstates(t *testing.T) {
	for v := uint64(0); v < 4096; v++ {
		ub := histUpperBound(histIndex(v))
		require.GreaterOrEqual(t, ub, v, "v=%d 的桶上界 %d 低于自身", v, ub)
	}
	// 稀疏抽查大值
	for _, v := range []uint64{10_000, 123_456, 1 << 20, 3 << 24, 1 << 33} {
		ub := histUpperBound(histIndex(v))
		require.GreaterOrEqual(t, ub, v, "v=%d 的桶上界 %d 低于自身", v, ub)
	}
}

// TestHistRelativeError 相对误差须在 1/histSubCount 以内
func TestHistRelativeError(t *testing.T) {
	for _, v := range []uint64{128, 200, 999, 10_000, 250_000, 1 << 20, 1 << 25, 1 << 30} {
		ub := histUpperBound(histIndex(v))
		relErr := float64(ub-v) / float64(v)
		assert.LessOrEqual(t, relErr, maxRelErr,
			"v=%d → 上界 %d，相对误差 %.4f 超过上界 %.4f", v, ub, relErr, maxRelErr)
	}
	t.Run("低值区精确", func(t *testing.T) {
		for v := uint64(0); v < histSubCount; v++ {
			assert.Equal(t, v, histUpperBound(histIndex(v)), "小于一个数量级的值应精确记录")
		}
	})
}

// TestHistogramPercentilesVsExact 分桶百分位须与精确百分位吻合到误差上界内
//
// 这是替换 []float64 的核心前提：若百分位偏差超出声明范围，
// 省下的内存与锁就是用报告的可信度换来的
func TestHistogramPercentilesVsExact(t *testing.T) {
	cases := []struct {
		name string
		gen  func(i int) time.Duration
		n    int
	}{
		{"均匀分布 1~50ms", func(i int) time.Duration {
			return time.Duration(rand.IntN(50_000)+1) * time.Microsecond
		}, 200_000},
		{"长尾：多数 1ms 少数 2s", func(i int) time.Duration {
			if i%1000 == 0 {
				return 2 * time.Second
			}
			return time.Duration(rand.IntN(1000)+500) * time.Microsecond
		}, 200_000},
		{"全部相同", func(i int) time.Duration {
			return 7 * time.Millisecond
		}, 10_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewLatencyHistogram()
			exact := make([]time.Duration, 0, tc.n)
			for i := 0; i < tc.n; i++ {
				d := tc.gen(i)
				h.Record(d)
				exact = append(exact, d)
			}
			require.Equal(t, uint64(tc.n), h.Count())

			sort.Slice(exact, func(a, b int) bool { return exact[a] < exact[b] })
			ps := []float64{50, 90, 95, 99}
			got := h.Percentiles(ps...)

			for i, p := range ps {
				// 与分桶一致的取法：第 ceil(n*p/100) 个样本（1 基）
				n := int(float64(tc.n)*p/100 + 0.5)
				if n < 1 {
					n = 1
				}
				want := exact[n-1]

				relErr := float64(got[i]-want) / float64(want)
				assert.GreaterOrEqual(t, got[i], want,
					"P%.0f 不得低于精确值（精确 %s，分桶 %s）", p, want, got[i])
				assert.LessOrEqual(t, relErr, maxRelErr*2,
					"P%.0f 相对误差 %.4f 过大（精确 %s，分桶 %s）", p, relErr, want, got[i])
			}
		})
	}
}

func TestHistogramEdgeCases(t *testing.T) {
	t.Run("空直方图返回零值", func(t *testing.T) {
		h := NewLatencyHistogram()
		got := h.Percentiles(50, 99)
		assert.Equal(t, []time.Duration{0, 0}, got)
		assert.Zero(t, h.Count())
	})

	t.Run("负耗时按 0 记录", func(t *testing.T) {
		h := NewLatencyHistogram()
		h.Record(-5 * time.Millisecond)
		assert.Equal(t, uint64(1), h.Count())
		assert.Equal(t, time.Duration(0), h.Percentiles(50)[0])
	})

	t.Run("超出范围的值归入末桶而不丢弃", func(t *testing.T) {
		// 丢弃会让百分位凭空变好，是比误差更糟的失真
		h := NewLatencyHistogram()
		// time.Duration 的上限约 292 年；取接近上限的值来越过直方图的可记录范围
		h.Record(time.Duration(1 << 61))
		assert.Equal(t, uint64(1), h.Count())
		assert.Positive(t, h.Percentiles(99)[0])
	})

	t.Run("百分位边界", func(t *testing.T) {
		h := NewLatencyHistogram()
		for i := 1; i <= 100; i++ {
			h.Record(time.Duration(i) * time.Millisecond)
		}
		got := h.Percentiles(0, 100)
		assert.Positive(t, got[0])
		assert.GreaterOrEqual(t, got[1], 100*time.Millisecond)
	})
}

// TestHistogramConcurrentRecord 并发记录不得丢样本
func TestHistogramConcurrentRecord(t *testing.T) {
	const goroutines, each = 50, 2000

	h := NewLatencyHistogram()
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < each; j++ {
				h.Record(time.Duration(id*100+j%97+1) * time.Microsecond)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, uint64(goroutines*each), h.Count(), "原子自增不应丢样本")
}

// TestHistogramMemoryIsBounded 内存占用与请求数无关
func TestHistogramMemoryIsBounded(t *testing.T) {
	h := NewLatencyHistogram()
	for i := 0; i < 1_000_000; i++ {
		h.Record(time.Duration(i%500_000+1) * time.Microsecond)
	}
	assert.Equal(t, uint64(1_000_000), h.Count())
	// 结构体本身定长；此处断言的是「不存在随样本增长的字段」这一设计
	assert.Equal(t, histBuckets, len(h.buckets))
}

// BenchmarkHistogramRecord 热路径成本
func BenchmarkHistogramRecord(b *testing.B) {
	h := NewLatencyHistogram()
	d := 1234 * time.Microsecond
	b.ReportAllocs()
	for b.Loop() {
		h.Record(d)
	}
}

// BenchmarkHistogramRecordParallel 并发下的热路径成本
func BenchmarkHistogramRecordParallel(b *testing.B) {
	h := NewLatencyHistogram()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			h.Record(time.Duration(i%50_000+1) * time.Microsecond)
		}
	})
}

// BenchmarkHistogramPercentiles 求百分位的成本，应与样本数无关
func BenchmarkHistogramPercentiles(b *testing.B) {
	h := NewLatencyHistogram()
	for i := 0; i < 1_000_000; i++ {
		h.Record(time.Duration(i%100_000+1) * time.Microsecond)
	}
	b.ReportAllocs()
	for b.Loop() {
		h.Percentiles(50, 90, 95, 99)
	}
}

// BenchmarkRecordAppendSlice 改动前的记录方式：逐条追加到切片
//
// 与 BenchmarkHistogramRecord 并排看：此处的 B/op 即每个请求留下的常驻内存，
// 一千万请求就是它乘以一千万；直方图那边恒为 0，因为桶是定长的
func BenchmarkRecordAppendSlice(b *testing.B) {
	var mu sync.Mutex
	durations := make([]float64, 0, 10000)
	d := 1234 * time.Microsecond

	b.ReportAllocs()
	for b.Loop() {
		mu.Lock()
		durations = append(durations, d.Seconds())
		mu.Unlock()
	}
}

// BenchmarkRecordAppendSliceParallel 改动前的并发代价
//
// 每个请求都要抢同一把写锁，这正是原实现里全工具唯一的串行点
func BenchmarkRecordAppendSliceParallel(b *testing.B) {
	var mu sync.Mutex
	durations := make([]float64, 0, 10000)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			d := time.Duration(i%50_000+1) * time.Microsecond
			mu.Lock()
			durations = append(durations, d.Seconds())
			mu.Unlock()
		}
	})
}
