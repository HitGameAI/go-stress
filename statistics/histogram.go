/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 18:12:07
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 18:30:55
 * @FilePath: \go-stress\statistics\histogram.go
 * @Description: 有界无锁的延迟直方图
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"math/bits"
	"sync/atomic"
	"time"
)

// 直方图的分桶参数
//
// 采用 HDR 直方图的「指数 + 线性子桶」布局：每个二进制数量级（octave）切成
// histSubCount 个等宽子桶，于是相对误差与绝对值无关——1ms 和 10s 保持同样的
// 相对精度
const (
	// histSubBits 每个数量级内的子桶位数
	histSubBits = 8
	// histSubCount 每个数量级内的子桶数
	histSubCount = 1 << histSubBits
	// histOctaves 覆盖的数量级个数；最大可记录值为 2^(histOctaves+histSubBits)-1 微秒
	// 取 34 时上限约 25 天，对压测足够
	histOctaves = 34
	// histBuckets 桶总数，决定内存占用（histBuckets×8 字节，约 70KB）
	histBuckets = histOctaves * histSubCount
	// histEffectiveSubCount 实际参与分辨的子桶数
	//
	// 注意不是 histSubCount：bucketIndex>0 时 micros>>bucketIndex 必然落在
	// [histSubCount/2, histSubCount) 这一上半区，下半区恒为空。因此有效分辨率
	// 是 1/(histSubCount/2) 而非 1/histSubCount——最初按后者写，被精度测试当场
	// 抓出实测误差是声明值的两倍
	histEffectiveSubCount = histSubCount / 2
	// histMaxRelErr 百分位的相对误差上界（1/128 ≈ 0.78%）
	histMaxRelErr = 1.0 / float64(histEffectiveSubCount)
)

// LatencyHistogram 延迟分布统计
//
// 为什么不再保留每个请求的耗时：原先用 []float64 逐条追加，有三处代价
//  1. 内存随请求数线性增长（8 字节/请求，一千万请求就是 80MB，
//     且切片扩容时还有一次两倍的瞬时峰值），最终会把 -max-memory 的
//     自动停止判定顶掉——停的是采集器自己撑出来的内存而非被测系统
//  2. 每个请求都要抢同一把写锁去 append，那是全工具唯一的串行点
//  3. 每次生成报告都要整体拷贝加排序，O(n log n) 外加一次 O(n) 分配；
//     实时报告每秒一次，请求数一多就是持续的大额开销
//
// 换成定长桶之后：内存恒为 histBuckets×8 字节（约 70KB，与请求数无关），
// 记录一次只是一条原子自增（热路径无锁），求百分位是 O(桶数) 且零分配。
// 代价是百分位带不超过 histMaxRelErr（约 0.78%）的相对误差，
// 这是主流压测工具的通行取舍
//
// 下半区子桶在 bucketIndex>0 时恒为空，故实际有近一半空桶。这是刻意的取舍：
// 换成 HDR 的紧凑布局能省一半内存，但下标计算会变得难以一眼验证，
// 而这里省下的是 35KB
type LatencyHistogram struct {
	buckets [histBuckets]atomic.Uint64
}

// NewLatencyHistogram 创建直方图
func NewLatencyHistogram() *LatencyHistogram {
	return &LatencyHistogram{}
}

// histIndex 把微秒值映射到桶号
func histIndex(micros uint64) int {
	// 低于一个数量级的值落在首个数量级内，分辨率为 1 微秒（精确）
	if micros < histSubCount {
		return int(micros)
	}

	// bucketIndex 为该值需要右移多少位才能落进子桶范围
	bucketIndex := bits.Len64(micros) - histSubBits
	if bucketIndex >= histOctaves {
		// 超出可记录范围，归入最后一个桶而非丢弃：
		// 丢弃会让百分位凭空变好，是比误差更糟的失真
		return histBuckets - 1
	}
	subIndex := micros >> uint(bucketIndex)
	return bucketIndex*histSubCount + int(subIndex)
}

// histUpperBound 返回桶所代表的上界（微秒）
//
// 取上界而非中点：这样「P99 = 5ms」始终是一句真话（99% 的请求不超过 5ms）。
// 取中点会让该陈述有可能不成立，而延迟指标宁可保守也不该乐观
func histUpperBound(idx int) uint64 {
	bucketIndex := idx / histSubCount
	subIndex := uint64(idx % histSubCount)
	width := uint64(1) << uint(bucketIndex)
	return subIndex*width + width - 1
}

// Record 记录一次耗时
func (h *LatencyHistogram) Record(d time.Duration) {
	micros := uint64(0)
	if d > 0 {
		micros = uint64(d.Microseconds())
	}
	h.buckets[histIndex(micros)].Add(1)
}

// Count 返回已记录的样本数
//
// 由桶求和而非维护独立计数器：计数器是全部 Worker 共写的同一条缓存行，
// 会成为热路径上的争用点，而 Count 只在生成报告时调用，
// O(桶数) 的求和完全够用
func (h *LatencyHistogram) Count() uint64 {
	var total uint64
	for i := range h.buckets {
		total += h.buckets[i].Load()
	}
	return total
}

// Percentiles 返回给定百分位对应的耗时
//
// 一次遍历求出全部百分位，ps 必须按升序给出。
// 遍历期间可能有并发写入，得到的是一个近似瞬时快照：
// 压测报告要的是分布形状，不需要为此停下发压去取一致性快照
func (h *LatencyHistogram) Percentiles(ps ...float64) []time.Duration {
	out := make([]time.Duration, len(ps))
	if len(ps) == 0 {
		return out
	}

	total := h.Count()
	if total == 0 {
		return out
	}

	// 每个百分位对应的样本序号（1 基）
	targets := make([]uint64, len(ps))
	for i, p := range ps {
		switch {
		case p <= 0:
			targets[i] = 1
		case p >= 100:
			targets[i] = total
		default:
			n := uint64(float64(total)*p/100 + 0.5)
			if n == 0 {
				n = 1
			}
			targets[i] = n
		}
	}

	var cumulative uint64
	next := 0
	for idx := 0; idx < histBuckets && next < len(ps); idx++ {
		c := h.buckets[idx].Load()
		if c == 0 {
			continue
		}
		cumulative += c
		for next < len(ps) && cumulative >= targets[next] {
			out[next] = time.Duration(histUpperBound(idx)) * time.Microsecond
			next++
		}
	}

	// 并发写入导致累计数不足目标时，用已见的最大值补齐
	if next > 0 && next < len(ps) {
		for i := next; i < len(ps); i++ {
			out[i] = out[next-1]
		}
	}
	return out
}
