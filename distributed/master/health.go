/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-23 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-23 10:00:00
 * @FilePath: \go-stress\distributed\master\health.go
 * @Description: Slave 健康检查
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package master

import (
	"context"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/distributed/common"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// HealthChecker 健康检查器 - 使用 syncx.PeriodicTask
type HealthChecker struct {
	pool         *SlavePool
	interval     time.Duration
	timeout      time.Duration
	maxFailures  int
	failureCount *syncx.Map[string, int32] // 使用 syncx.Map 管理失败计数
	logger       logger.ILogger
	taskManager  *syncx.PeriodicTaskManager // 使用 syncx.PeriodicTaskManager
}

// NewHealthChecker 创建健康检查器
func NewHealthChecker(pool *SlavePool, interval, timeout time.Duration, maxFailures int, log logger.ILogger) *HealthChecker {
	return &HealthChecker{
		pool:         pool,
		interval:     interval,
		timeout:      timeout,
		maxFailures:  maxFailures,
		failureCount: syncx.NewMap[string, int32](),
		logger:       log,
		taskManager:  syncx.NewPeriodicTaskManager(),
	}
}

// Start 启动健康检查 - 使用 syncx.PeriodicTask
func (hc *HealthChecker) Start(ctx context.Context) {
	// 创建周期性健康检查任务
	task := syncx.NewPeriodicTask("health-check", hc.interval, func(taskCtx context.Context) error {
		hc.checkAll()
		return nil
	}).
		SetOnError(func(name string, err error) {
			hc.logger.ErrorKV("Health check task error", "task", name, "error", err)
		}).
		SetOnStart(func(name string) {
			hc.logger.InfoKV("Health checker started", "interval", hc.interval)
		}).
		SetOnStop(func(name string) {
			hc.logger.Info("Health checker stopped")
		})

	// 添加任务并启动
	hc.taskManager.AddTask(task)
	hc.taskManager.Start()
}

// checkAll 检查所有 Slave - 使用 syncx.Parallel
func (hc *HealthChecker) checkAll() {
	slaves := hc.pool.GetAll()

	syncx.ParallelForEachSlice(slaves, func(idx int, slave *common.SlaveInfo) {
		hc.checkSlave(slave)
	})
}

// checkSlave 检查单个 Slave
func (hc *HealthChecker) checkSlave(slave *common.SlaveInfo) {
	// 检查心跳超时
	if time.Since(slave.LastHeartbeat) > hc.timeout {
		hc.handleFailure(slave)
	} else {
		hc.handleSuccess(slave)
	}
}

// handleFailure 处理检查失败
func (hc *HealthChecker) handleFailure(slave *common.SlaveInfo) {
	// 原子递增失败计数
	count, _ := hc.failureCount.Load(slave.ID)
	count++
	hc.failureCount.Store(slave.ID, count)

	if int(count) >= hc.maxFailures {
		// 标记为不健康
		if err := hc.pool.MarkUnhealthy(slave.ID); err != nil {
			hc.logger.ErrorKV("Failed to mark slave as unhealthy",
				"slave_id", slave.ID,
				"error", err)
			return
		}

		hc.logger.WarnKV("Slave marked as unhealthy",
			"slave_id", slave.ID,
			"hostname", slave.Hostname,
			"failures", count)
	}
}

// handleSuccess 处理检查成功
func (hc *HealthChecker) handleSuccess(slave *common.SlaveInfo) {
	// 检查并重置失败计数
	if count, loaded := hc.failureCount.Load(slave.ID); loaded && count > 0 {
		hc.failureCount.Delete(slave.ID)

		// 如果之前是不健康状态,恢复为健康
		if err := hc.pool.MarkHealthy(slave.ID); err == nil {
			hc.logger.InfoKV("Slave recovered to healthy",
				"slave_id", slave.ID,
				"hostname", slave.Hostname)
		}
	}
}

// SetInterval 设置检查间隔
func (hc *HealthChecker) SetInterval(interval time.Duration) {
	hc.interval = interval
}

// SetTimeout 设置超时时间
func (hc *HealthChecker) SetTimeout(timeout time.Duration) {
	hc.timeout = timeout
}

// SetMaxFailures 设置最大失败次数
func (hc *HealthChecker) SetMaxFailures(max int) {
	hc.maxFailures = max
}
