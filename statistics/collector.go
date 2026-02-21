/*
* @Author: kamalyes 501893067@qq.com
* @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 11:15:22
* @FilePath: \go-stress\statistics\collector.go
* @Description: 统计数据收集器
*
* Copyright (c) 2025 by kamalyes, All Rights Reserved.
*/
package statistics

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/idgen"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// Collector 统计收集器
type Collector struct {
	// 使用 syncx 原子类型
	totalRequests   *syncx.Uint64
	successRequests *syncx.Uint64
	failedRequests  *syncx.Uint64
	skippedRequests *syncx.Uint64 // 跳过请求计数器

	// 时长与流量统计
	//
	// 全部改为原子类型，使 Collect 整条路径无锁：此前这些字段由一把写锁保护，
	// 而每个请求都要抢它一次，那是全工具唯一的串行点
	totalDurationNanos atomic.Int64  // 耗时总和（纳秒）
	minDurationNanos   atomic.Int64  // 最小耗时（纳秒），初值为 int64 上限
	maxDurationNanos   atomic.Int64  // 最大耗时（纳秒）
	totalSizeBytes     atomic.Uint64 // 响应字节总和

	// latency 延迟分布，取代原先逐条追加的 []float64（见 LatencyHistogram 的说明）
	latency *LatencyHistogram

	// recent 最近若干次耗时，供实时图表画走势
	recent *recentDurations

	// 使用 syncx.Map 替换 map + mutex
	errors      *syncx.Map[string, uint64]
	statusCodes *syncx.Map[int, uint64]

	// 按 API 名聚合的统计（详见 api_stats.go）
	apiStats *apiAggregator

	// 统一的存储接口（支持 SQLite 和 Memory 两种实现）
	storage StorageInterface

	// ID 生成器（使用 Snowflake 算法生成全局唯一ID）
	idGenerator *idgen.SnowflakeGenerator

	// 外部上报器（用于分布式模式）
	externalReporter func(*RequestResult)
	reporterMu       *syncx.RWLock

	// 运行模式
	runMode RunMode

	// 配置信息（用于报告显示）
	protocol    string
	concurrency uint64
	totalReqs   uint64

	// 关闭标志
	closed *syncx.Bool

	// 秒级时序记录器（StartTimeSeries 后非空），详见 timeseries.go
	series     atomic.Pointer[timeSeriesRecorder]
	seriesOnce sync.Once

	// 日志器
	logger logger.ILogger
}

// NewCollector 使用存储和日志器创建收集器
func NewCollector(strg StorageInterface, log logger.ILogger) *Collector {
	c := &Collector{
		totalRequests:   syncx.NewUint64(0),
		successRequests: syncx.NewUint64(0),
		failedRequests:  syncx.NewUint64(0),
		skippedRequests: syncx.NewUint64(0),
		reporterMu:      syncx.NewRWLock(),
		latency:         NewLatencyHistogram(),
		recent:          newRecentDurations(),
		errors:          syncx.NewMap[string, uint64](),
		statusCodes:     syncx.NewMap[int, uint64](),
		apiStats:        newAPIAggregator(),
		storage:         strg,
		idGenerator:     idgen.NewSnowflakeGenerator(1, 1),
		closed:          syncx.NewBool(false),
		logger:          log,
	}
	// 最小值从 int64 上限开始，任何真实耗时都能把它压下来；
	// 原先用 time.Hour 作初值，一旦所有请求都慢于 1 小时就会报出错误的最小值
	c.minDurationNanos.Store(math.MaxInt64)
	return c
}

// Collect 收集单次请求结果
func (c *Collector) Collect(result *RequestResult) {
	if result == nil {
		c.logger.Warn("⚠️  收到空的请求结果，跳过收集")
		return
	}

	// 调用外部上报器（如果设置了）
	c.reporterMu.RLock()
	if c.externalReporter != nil {
		c.externalReporter(result)
	}
	c.reporterMu.RUnlock()

	// 原子操作，无需加锁
	c.totalRequests.Add(1)

	if result.Skipped {
		// 跳过的请求单独计数，不计入成功或失败
		c.skippedRequests.Add(1)
	} else if result.Success {
		// 只有非跳过的请求才计入成功
		c.successRequests.Add(1)
	} else {
		// 只有非跳过的请求才计入失败
		c.failedRequests.Add(1)

		// 记录错误 - 原子自增，详见 incrementCounter 的说明
		if result.Error != nil {
			incrementCounter(c.errors, result.Error.Error())
		}
	}

	// 统计状态码 - 原子自增，详见 incrementCounter 的说明
	if result.StatusCode > 0 {
		incrementCounter(c.statusCodes, result.StatusCode)
	}

	// 统计耗时与流量 - 全程原子操作，无锁
	nanos := result.Duration.Nanoseconds()
	c.totalDurationNanos.Add(nanos)
	c.latency.Record(result.Duration)
	c.recent.record(result.Duration)
	storeMin(&c.minDurationNanos, nanos)
	storeMax(&c.maxDurationNanos, nanos)
	// Size 实际来自 len(resp.Body)，恒为整数字节数，故按 uint64 累加不损失精度
	if result.Size > 0 {
		c.totalSizeBytes.Add(uint64(result.Size))
	}

	// 按 API 聚合：多接口场景下报告页需要每个接口各自的延迟与成功率
	if result.APIName != "" {
		c.apiStats.record(result.APIName, result)
	}

	// 生成唯一ID和错误消息
	result.ID = c.idGenerator.GenerateRequestID()
	if result.Error != nil {
		result.ErrorMsg = result.Error.Error()
	}

	// 直接写入存储
	c.storage.Write(result)
}

// incrementCounter 对 syncx.Map 中的计数器做原子自增
//
// 不能写成 LoadOrStore + Store：那是读-改-写三步，两个 goroutine 同时读到同一个旧值
// 时，后写的会覆盖先写的，计数被吞。压测恰恰是高并发场景，越是并发高、错误集中，
// 吞掉的越多，最终表现为"失败请求 1000 条，但错误表里次数之和只有几百"的自相矛盾报告
//
// syncx.Map.Update 同样是 Load + Store，不具备原子性，故此处用 CAS 重试：
// sync.Map 的 LoadOrStore 与 CompareAndSwap 都是原子的，组合起来即可无锁完成自增
func incrementCounter[K comparable](m *syncx.Map[K, uint64], key K) {
	for {
		old, loaded := m.LoadOrStore(key, 1)
		if !loaded {
			return // 本次调用完成了首次插入，计数已是 1
		}
		if m.CompareAndSwap(key, old, old+1) {
			return
		}
		// CAS 失败说明期间有并发写入，重读当前值后重试
	}
}

// GetMetrics 获取实时指标
func (c *Collector) GetMetrics() *Metrics {
	return &Metrics{
		TotalRequests:   c.totalRequests.Load(),
		SuccessRequests: c.successRequests.Load(),
		FailedRequests:  c.failedRequests.Load(),
	}
}

// GetSnapshot 获取统计快照
func (c *Collector) GetSnapshot() *Snapshot {
	totalReqs := c.totalRequests.Load()

	snapshot := &Snapshot{
		TotalRequests:   totalReqs,
		SuccessRequests: c.successRequests.Load(),
		FailedRequests:  c.failedRequests.Load(),
		MinLatency:      c.MinLatency(),
		MaxLatency:      c.MaxLatency(),
		TotalSize:       float64(c.totalSizeBytes.Load()),
	}

	if totalReqs > 0 {
		snapshot.AvgLatency = time.Duration(c.totalDurationNanos.Load() / int64(totalReqs))
	}

	return snapshot
}

// MinLatency 返回最小耗时，无样本时为 0
func (c *Collector) MinLatency() time.Duration {
	v := c.minDurationNanos.Load()
	if v == math.MaxInt64 {
		return 0
	}
	return time.Duration(v)
}

// MaxLatency 返回最大耗时
func (c *Collector) MaxLatency() time.Duration {
	return time.Duration(c.maxDurationNanos.Load())
}

// storeMin 以 CAS 重试把 v 写入 dst（仅当更小）
//
// 不能写成「读-比较-写」三步：两个 goroutine 同时读到同一旧值时，
// 后写的会覆盖先写的，最小值被吞
func storeMin(dst *atomic.Int64, v int64) {
	for {
		old := dst.Load()
		if v >= old || dst.CompareAndSwap(old, v) {
			return
		}
	}
}

// storeMax 以 CAS 重试把 v 写入 dst（仅当更大）
func storeMax(dst *atomic.Int64, v int64) {
	for {
		old := dst.Load()
		if v <= old || dst.CompareAndSwap(old, v) {
			return
		}
	}
}

// GetStatusCodes 获取状态码统计
func (c *Collector) GetStatusCodes() map[int]uint64 {
	return c.statusCodes.ToMap()
}

// GetRequestDetails 获取请求明细（支持分页和筛选）
func (c *Collector) GetRequestDetails(offset, limit int, statusFilter StatusFilter, nodeID, taskID string) []*RequestResult {
	// 即使 Collector 已关闭，依然允许读取已存储的数据
	if c.storage == nil {
		c.logger.Warn("⚠️  存储未初始化")
		return []*RequestResult{}
	}

	details, err := c.storage.Query(offset, limit, statusFilter, nodeID, taskID)
	if err == nil {
		return details
	}

	// 记录错误（除非已关闭）
	if !c.closed.Load() {
		c.logger.Warnf("⚠️  从存储读取失败: %v", err)
	}

	// 降级：返回空切片
	return []*RequestResult{}
}

// GetRequestDetailsCount 获取请求明细总数
func (c *Collector) GetRequestDetailsCount(statusFilter StatusFilter, nodeID, taskID string) int {
	// 即使 Collector 已关闭，依然允许读取已存储的数据计数
	if c.storage == nil {
		c.logger.Warn("⚠️  存储未初始化")
		return 0
	}

	count, err := c.storage.Count(statusFilter, nodeID, taskID)
	if err == nil {
		return count
	}

	// 记录错误（除非已关闭）
	if !c.closed.Load() {
		c.logger.Warnf("⚠️  统计总数失败: %v", err)
	}

	// 降级：返回0
	return 0
}

// GetRequestDetailsWithFilter 获取请求明细（支持指定 nodeID 和 taskID 过滤，用于分布式模式）
func (c *Collector) GetRequestDetailsWithFilter(offset, limit int, statusFilter StatusFilter, nodeID, taskID string) []*RequestResult {
	if c.storage == nil {
		c.logger.Warn("⚠️  存储未初始化")
		return []*RequestResult{}
	}

	details, err := c.storage.Query(offset, limit, statusFilter, nodeID, taskID)
	if err == nil {
		return details
	}

	if !c.closed.Load() {
		c.logger.Warnf("⚠️  从存储读取失败: %v", err)
	}

	return []*RequestResult{}
}

// GetRequestDetailsCountWithFilter 获取请求明细总数（支持指定 nodeID 和 taskID 过滤，用于分布式模式）
func (c *Collector) GetRequestDetailsCountWithFilter(statusFilter StatusFilter, nodeID, taskID string) int {
	if c.storage == nil {
		c.logger.Warn("⚠️  存储未初始化")
		return 0
	}

	count, err := c.storage.Count(statusFilter, nodeID, taskID)
	if err == nil {
		return count
	}

	if !c.closed.Load() {
		c.logger.Warnf("⚠️  统计总数失败: %v", err)
	}

	return 0
}

// SetExternalReporter 设置外部上报器
func (c *Collector) SetExternalReporter(reporter func(*RequestResult)) {
	c.reporterMu.Lock()
	defer c.reporterMu.Unlock()
	c.externalReporter = reporter
}

// SetRunMode 设置运行模式
func (c *Collector) SetRunMode(mode RunMode) {
	c.runMode = mode
}

// SetConfig 设置配置信息（用于报告显示）
func (c *Collector) SetConfig(protocol string, concurrency, totalReqs uint64) {
	c.protocol = protocol
	c.concurrency = concurrency
	c.totalReqs = totalReqs
}

// ClearExternalReporter 清除外部上报器
func (c *Collector) ClearExternalReporter() {
	c.reporterMu.Lock()
	defer c.reporterMu.Unlock()
	c.externalReporter = nil
}

// Close 关闭收集器，释放资源
func (c *Collector) Close() error {
	// 设置关闭标志
	c.closed.Store(true)
	c.logger.Debug("📌 Collector 已标记为关闭状态")

	if c.storage != nil {
		c.logger.Debug("📌 正在关闭存储...")
		return c.storage.Close()
	}
	return nil
}

// Snapshot 统计快照（用于实时显示）
type Snapshot struct {
	TotalRequests   uint64
	SuccessRequests uint64
	FailedRequests  uint64
	MinLatency      time.Duration
	MaxLatency      time.Duration
	AvgLatency      time.Duration
	TotalSize       float64
}

// Metrics 实时指标
type Metrics struct {
	TotalRequests   uint64
	SuccessRequests uint64
	FailedRequests  uint64
}

// StartTimeSeries 启动秒级时序记录（幂等，压测开始时调用一次）
//
// 采样以定时差分实现，热路径零新增代价；ctx 结束（如用户中断）时自动停止并补采
func (c *Collector) StartTimeSeries(ctx context.Context) {
	c.seriesOnce.Do(func() {
		rec := newTimeSeriesRecorder(c)
		c.series.Store(rec)
		go rec.run(ctx)
	})
}

// StopTimeSeries 停止时序记录并补采最后一段（幂等，报告生成前调用）
func (c *Collector) StopTimeSeries() {
	if rec := c.series.Load(); rec != nil {
		rec.stop()
	}
}

// TimeSeries 返回时序数据副本（未启动记录或尚无数据时为 nil）
func (c *Collector) TimeSeries() *TimeSeriesData {
	if rec := c.series.Load(); rec != nil {
		return rec.data()
	}
	return nil
}
