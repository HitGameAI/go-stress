/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-23 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-23 10:00:00
 * @FilePath: \go-stress\distributed\master\queue.go
 * @Description: 任务队列管理
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package master

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/distributed/common"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// ErrTaskNotFoundInRunning 任务在运行中队列里不存在（新主内存态没有它）。
// 主备切换后新主不认识旧主派发的任务，完成/失败上报据此宽容处理
var ErrTaskNotFoundInRunning = errors.New("task not found in running queue")

// TaskQueue 任务队列 - 使用分片和优化的并发访问
type TaskQueue struct {
	mu           *syncx.RWLock  // 全局锁 - 仅用于关键路径
	pendingQueue []*common.Task // 循环队列：待执行任务
	pendingHead  int            // 队列头指针
	pendingTail  int            // 队列尾指针
	pendingLen   int            // 当前队列大小
	pendingCap   int            // 队列容量

	running  *syncx.Map[string, *common.Task] // 运行中任务
	complete *syncx.Map[string, *common.Task] // 已完成任务
	failed   *syncx.Map[string, *common.Task] // 失败任务
	stopped  *syncx.Map[string, *common.Task] // 已停止任务

	allTasks     *syncx.Map[string, *common.Task] // 所有任务的统一索引 (O(1) 查询)
	completeTime map[string]time.Time             // 已完成任务的时间戳 - 加速清理
	failedTime   map[string]time.Time             // 失败任务的时间戳 - 加速清理
	historyLock  sync.Mutex                       // 保护 completeTime/failedTime
	// 状态机：每个任务维护一个独立的状态机用于验证状态转换
	stateMachines *syncx.Map[string, *syncx.StateMachine[common.TaskState]]

	// 原子统计字段
	pendingCount  int64
	runningCount  int64
	completeCount int64
	failedCount   int64
	stoppedCount  int64

	splitter TaskSplitter
	logger   logger.ILogger
}

// QueueStats 任务队列统计信息（导出结构体）
type QueueStats struct {
	Pending  int `json:"pending"`  // 待执行任务数
	Running  int `json:"running"`  // 运行中任务数
	Complete int `json:"complete"` // 已完成任务数
	Failed   int `json:"failed"`   // 失败任务数
	Stopped  int `json:"stopped"`  // 已停止任务数
}

// NewTaskQueue 创建任务队列
func NewTaskQueue(splitter TaskSplitter, log logger.ILogger) *TaskQueue {
	return &TaskQueue{
		mu:            syncx.NewRWLock(),
		pendingQueue:  make([]*common.Task, 10000), // 初始化为正确长度而不是容量
		pendingCap:    10000,
		running:       syncx.NewMap[string, *common.Task](),
		complete:      syncx.NewMap[string, *common.Task](),
		failed:        syncx.NewMap[string, *common.Task](),
		stopped:       syncx.NewMap[string, *common.Task](),
		allTasks:      syncx.NewMap[string, *common.Task](),
		completeTime:  make(map[string]time.Time),
		failedTime:    make(map[string]time.Time),
		stateMachines: syncx.NewMap[string, *syncx.StateMachine[common.TaskState]](),
		splitter:      splitter,
		logger:        log,
	}
}

// newTaskStateMachine 为任务创建状态机
func (tq *TaskQueue) newTaskStateMachine() *syncx.StateMachine[common.TaskState] {
	// 启用历史记录追踪，最多保留100条历史记录
	sm := syncx.NewStateMachine(common.TaskStatePending, syncx.WithTrackHistory[common.TaskState](100))

	// 配置允许的状态转换
	sm.AllowTransition(common.TaskStatePending, common.TaskStateRunning)
	sm.AllowTransition(common.TaskStatePending, common.TaskStateStopped)
	sm.AllowTransition(common.TaskStateRunning, common.TaskStateCompleted)
	sm.AllowTransition(common.TaskStateRunning, common.TaskStateFailed)
	sm.AllowTransition(common.TaskStateRunning, common.TaskStateStopped)

	return sm
}

// getTaskStateMachine 获取或创建任务的状态机
func (tq *TaskQueue) getTaskStateMachine(taskID string) *syncx.StateMachine[common.TaskState] {
	sm, exists := tq.stateMachines.Load(taskID)
	if exists {
		return sm
	}

	newSM := tq.newTaskStateMachine()
	actualSM, _ := tq.stateMachines.LoadOrStore(taskID, newSM)
	return actualSM
}

// Submit 提交任务
func (tq *TaskQueue) Submit(task *common.Task) error {
	return syncx.WithLockReturnValue(tq.mu, func() error {
		task.State = common.TaskStatePending
		task.CreatedAt = time.Now()

		// 创建状态机（确保在提交时就创建）
		tq.getTaskStateMachine(task.ID)

		// 循环队列：容量不足时扩容
		if tq.pendingLen >= tq.pendingCap {
			newCap := tq.pendingCap * 2
			newQueue := make([]*common.Task, newCap)
			copy(newQueue, tq.pendingQueue[:tq.pendingLen])
			tq.pendingQueue = newQueue
			tq.pendingCap = newCap
			tq.pendingHead = 0
			tq.pendingTail = tq.pendingLen
		}

		tq.pendingQueue[tq.pendingTail] = task
		tq.pendingTail = (tq.pendingTail + 1) % tq.pendingCap
		tq.pendingLen++
		tq.allTasks.Store(task.ID, task)
		atomic.AddInt64(&tq.pendingCount, 1)

		tq.logger.InfoKV("Task submitted", "task_id", task.ID, "workers", task.TotalWorkers)
		return nil
	})
}

// GetPending 获取待处理任务（快速返回列表副本）
func (tq *TaskQueue) GetPending() []*common.Task {
	return syncx.WithRLockReturnValue(tq.mu, func() []*common.Task {
		tasks := make([]*common.Task, 0, tq.pendingLen)
		for i := 0; i < tq.pendingLen; i++ {
			idx := (tq.pendingHead + i) % tq.pendingCap
			tasks = append(tasks, tq.pendingQueue[idx])
		}
		return tasks
	})
}

// PopPending 弹出第一个待处理任务 (O(1) 操作)
func (tq *TaskQueue) PopPending() *common.Task {
	return syncx.WithLockReturnValue(tq.mu, func() *common.Task {
		if tq.pendingLen == 0 {
			return nil
		}

		task := tq.pendingQueue[tq.pendingHead]
		tq.pendingQueue[tq.pendingHead] = nil // 释放引用，便于 GC
		tq.pendingHead = (tq.pendingHead + 1) % tq.pendingCap
		tq.pendingLen--
		atomic.AddInt64(&tq.pendingCount, -1)

		return task
	})
}

// isInTerminalState 检查任务是否已经处于终态
func (tq *TaskQueue) isInTerminalState(taskID string) (bool, common.TaskState) {
	if _, ok := tq.complete.Load(taskID); ok {
		return true, common.TaskStateCompleted
	}
	if _, ok := tq.failed.Load(taskID); ok {
		return true, common.TaskStateFailed
	}
	if _, ok := tq.stopped.Load(taskID); ok {
		return true, common.TaskStateStopped
	}
	return false, common.TaskStatePending
}

// MoveToRunning 将任务移动到运行中
func (tq *TaskQueue) MoveToRunning(taskID string) error {
	return syncx.WithLockReturnValue(tq.mu, func() error {
		// 检查终态
		if isTerminal, state := tq.isInTerminalState(taskID); isTerminal {
			return fmt.Errorf("task %s is already %s, cannot move to running", taskID, state)
		}

		// 从 pending 队列中查找
		var taskIdx int = -1
		for i := 0; i < tq.pendingLen; i++ {
			idx := (tq.pendingHead + i) % tq.pendingCap
			if tq.pendingQueue[idx].ID == taskID {
				taskIdx = i
				break
			}
		}

		if taskIdx == -1 {
			return fmt.Errorf("task %s not found in pending queue", taskID)
		}

		// 移除任务
		realIdx := (tq.pendingHead + taskIdx) % tq.pendingCap
		task := tq.pendingQueue[realIdx]

		// 记录开始时间（在状态转换之前）
		startedAt := time.Now()

		// 验证状态转换（使用状态机）
		if err := tq.getTaskStateMachine(taskID).TransitionTo(common.TaskStateRunning); err != nil {
			return fmt.Errorf("invalid state transition for task %s: %w", taskID, err)
		}

		// 处理循环队列中的移除
		if taskIdx < tq.pendingLen-1 {
			// 需要移除中间的元素，创建新的队列
			newQueue := make([]*common.Task, tq.pendingLen-1)
			newIdx := 0
			for i := 0; i < tq.pendingLen; i++ {
				if i != taskIdx {
					idx := (tq.pendingHead + i) % tq.pendingCap
					newQueue[newIdx] = tq.pendingQueue[idx]
					newIdx++
				}
			}
			tq.pendingQueue = newQueue
			tq.pendingHead = 0
			tq.pendingTail = tq.pendingLen - 1
			tq.pendingCap = len(newQueue)
		} else {
			// 移除末尾，直接回退尾指针
			tq.pendingTail = (tq.pendingTail - 1 + tq.pendingCap) % tq.pendingCap
		}
		tq.pendingLen--
		atomic.AddInt64(&tq.pendingCount, -1)

		// 移动到运行中
		task.State = common.TaskStateRunning
		task.StartedAt = startedAt
		tq.running.Store(taskID, task)
		atomic.AddInt64(&tq.runningCount, 1)

		tq.logger.InfoKV("Task started", "task_id", taskID)
		return nil
	})
}

// MoveToComplete 将任务移动到完成
func (tq *TaskQueue) MoveToComplete(taskID string) error {
	task, ok := tq.running.Load(taskID)
	if !ok {
		return fmt.Errorf("%w: task %s", ErrTaskNotFoundInRunning, taskID)
	}

	// 检查终态（不需要锁，因为 syncx.Map 是并发安全的）
	if _, ok := tq.complete.Load(taskID); ok {
		tq.logger.WarnKV("Task is already completed", "task_id", taskID)
		return nil
	}
	if _, ok := tq.failed.Load(taskID); ok {
		return fmt.Errorf("task %s is already failed, cannot mark as completed", taskID)
	}
	if _, ok := tq.stopped.Load(taskID); ok {
		return fmt.Errorf("task %s is already stopped, cannot mark as complete", taskID)
	}

	// 记录完成时间（在状态转换之前）
	completedAt := time.Now()

	// 验证状态转换（使用状态机）
	if err := tq.getTaskStateMachine(taskID).TransitionTo(common.TaskStateCompleted); err != nil {
		return fmt.Errorf("invalid state transition for task %s: %w", taskID, err)
	}

	t := task
	t.State = common.TaskStateCompleted
	t.CompletedAt = completedAt
	tq.complete.Store(taskID, t)
	tq.running.Delete(taskID)
	atomic.AddInt64(&tq.runningCount, -1)
	atomic.AddInt64(&tq.completeCount, 1)

	// 记录完成时间戳用于清理
	tq.historyLock.Lock()
	tq.completeTime[taskID] = t.CompletedAt
	tq.historyLock.Unlock()

	tq.logger.InfoKV("Task completed",
		"task_id", taskID,
		"duration", t.CompletedAt.Sub(t.StartedAt))
	return nil
}

// MoveToFailed 将任务移动到失败
func (tq *TaskQueue) MoveToFailed(taskID string, reason string) error {
	task, ok := tq.running.Load(taskID)
	if !ok {
		return fmt.Errorf("%w: task %s", ErrTaskNotFoundInRunning, taskID)
	}

	// 检查终态
	if _, ok := tq.complete.Load(taskID); ok {
		return fmt.Errorf("task %s is already completed, cannot mark as failed", taskID)
	}
	if _, ok := tq.failed.Load(taskID); ok {
		tq.logger.WarnKV("Task is already failed", "task_id", taskID, "reason", reason)
		return nil
	}
	if _, ok := tq.stopped.Load(taskID); ok {
		return fmt.Errorf("task %s is already stopped, cannot mark as failed", taskID)
	}

	// 记录失败时间（在状态转换之前）
	failedAt := time.Now()

	// 验证状态转换（使用状态机）
	if err := tq.getTaskStateMachine(taskID).TransitionTo(common.TaskStateFailed); err != nil {
		return fmt.Errorf("invalid state transition for task %s: %w", taskID, err)
	}

	t := task
	t.State = common.TaskStateFailed
	t.CompletedAt = failedAt
	if t.Metadata == nil {
		t.Metadata = make(map[string]string)
	}
	t.Metadata["failure_reason"] = reason
	tq.failed.Store(taskID, t)
	tq.running.Delete(taskID)
	atomic.AddInt64(&tq.runningCount, -1)
	atomic.AddInt64(&tq.failedCount, 1)

	// 记录失败时间戳用于清理
	tq.historyLock.Lock()
	tq.failedTime[taskID] = t.CompletedAt
	tq.historyLock.Unlock()

	tq.logger.ErrorKV("Task failed", "task_id", taskID, "reason", reason)
	return nil
}

// Get 获取任务 (O(1))
func (tq *TaskQueue) Get(taskID string) (*common.Task, bool) {
	task, ok := tq.allTasks.Load(taskID)
	return task, ok
}

// GetRunning 获取所有运行中的任务
func (tq *TaskQueue) GetRunning() []*common.Task {
	var tasks []*common.Task
	tq.running.Range(func(taskID string, task *common.Task) bool {
		tasks = append(tasks, task)
		return true
	})
	return tasks
}

// GetAllTasks 获取所有任务（O(n) 复制但只遍历一次）
func (tq *TaskQueue) GetAllTasks() []*common.Task {
	var tasks []*common.Task
	tq.allTasks.Range(func(taskID string, task *common.Task) bool {
		tasks = append(tasks, task)
		return true
	})
	return tasks
}

// Split 分片任务
func (tq *TaskQueue) Split(task *common.Task, slaves []*common.SlaveInfo) ([]*common.SubTask, error) {
	if tq.splitter == nil {
		return nil, fmt.Errorf("task splitter not set")
	}
	return tq.splitter.Split(task, slaves)
}

// Cancel 取消任务
func (tq *TaskQueue) Cancel(taskID string) error {
	return syncx.WithLockReturnValue(tq.mu, func() error {
		// 检查任务是否已经处于终态（不允许再停止）
		if _, exists := tq.complete.Load(taskID); exists {
			return fmt.Errorf("task %s is already completed, cannot stop", taskID)
		}
		if _, exists := tq.failed.Load(taskID); exists {
			return fmt.Errorf("task %s is already failed, cannot stop", taskID)
		}
		if _, exists := tq.stopped.Load(taskID); exists {
			return fmt.Errorf("task %s is already stopped", taskID)
		}

		// 从 pending 中查找并移除
		for i := 0; i < tq.pendingLen; i++ {
			idx := (tq.pendingHead + i) % tq.pendingCap
			if tq.pendingQueue[idx].ID == taskID {
				task := tq.pendingQueue[idx]

				// 记录停止时间（在状态转换之前）
				stoppedAt := time.Now()

				// 验证状态转换
				if err := tq.getTaskStateMachine(taskID).TransitionTo(common.TaskStateStopped); err != nil {
					return fmt.Errorf("invalid state transition for task %s: %w", taskID, err)
				}

				task.State = common.TaskStateStopped
				task.CompletedAt = stoppedAt

				// 处理循环队列中的移除
				if i < tq.pendingLen-1 {
					newQueue := make([]*common.Task, tq.pendingLen-1)
					newIdx := 0
					for j := 0; j < tq.pendingLen; j++ {
						if j != i {
							jdx := (tq.pendingHead + j) % tq.pendingCap
							newQueue[newIdx] = tq.pendingQueue[jdx]
							newIdx++
						}
					}
					tq.pendingQueue = newQueue
					tq.pendingHead = 0
					tq.pendingTail = tq.pendingLen - 1
					tq.pendingCap = len(newQueue)
				} else {
					tq.pendingTail = (tq.pendingTail - 1 + tq.pendingCap) % tq.pendingCap
				}
				tq.pendingLen--
				atomic.AddInt64(&tq.pendingCount, -1)

				// 移动到 stopped 队列
				tq.stopped.Store(taskID, task)
				atomic.AddInt64(&tq.stoppedCount, 1)

				tq.logger.InfoKV("Task cancelled from pending", "task_id", taskID)
				return nil
			}
		}

		// 从 running 中移除
		if task, ok := tq.running.Load(taskID); ok {
			t := task

			// 记录停止时间（在状态转换之前）
			stoppedAt := time.Now()

			// 验证状态转换
			if err := tq.getTaskStateMachine(taskID).TransitionTo(common.TaskStateStopped); err != nil {
				return fmt.Errorf("invalid state transition for task %s: %w", taskID, err)
			}

			t.State = common.TaskStateStopped
			t.CompletedAt = stoppedAt

			// 移动到 stopped 队列
			tq.stopped.Store(taskID, t)
			tq.running.Delete(taskID)
			atomic.AddInt64(&tq.runningCount, -1)
			atomic.AddInt64(&tq.stoppedCount, 1)

			tq.logger.InfoKV("Task stopped from running", "task_id", taskID)
			return nil
		}

		return fmt.Errorf("task %s not found in pending or running", taskID)
	})
}

// Stats 获取队列统计
func (tq *TaskQueue) Stats() *QueueStats {
	return &QueueStats{
		Pending:  int(atomic.LoadInt64(&tq.pendingCount)),
		Running:  int(atomic.LoadInt64(&tq.runningCount)),
		Complete: int(atomic.LoadInt64(&tq.completeCount)),
		Failed:   int(atomic.LoadInt64(&tq.failedCount)),
		Stopped:  int(atomic.LoadInt64(&tq.stoppedCount)),
	}
}

// Clean 清理已完成的任务 - 使用时间戳索引加速
func (tq *TaskQueue) Clean(maxAge time.Duration) {
	now := time.Now()
	deadline := now.Add(-maxAge)

	tq.historyLock.Lock()

	// 清理已完成任务
	var toDeleteComplete []string
	for id, completedAt := range tq.completeTime {
		if !completedAt.After(deadline) {
			tq.complete.Delete(id)
			tq.allTasks.Delete(id)
			toDeleteComplete = append(toDeleteComplete, id)
			atomic.AddInt64(&tq.completeCount, -1) // 更新计数器
		}
	}
	for _, id := range toDeleteComplete {
		delete(tq.completeTime, id)
	}

	// 清理已失败任务
	var toDeleteFailed []string
	for id, completedAt := range tq.failedTime {
		if !completedAt.After(deadline) {
			tq.failed.Delete(id)
			tq.allTasks.Delete(id)
			toDeleteFailed = append(toDeleteFailed, id)
			atomic.AddInt64(&tq.failedCount, -1) // 更新计数器
		}
	}
	for _, id := range toDeleteFailed {
		delete(tq.failedTime, id)
	}

	tq.historyLock.Unlock()
}

// StartCleaner 启动清理器 - 使用周期性任务管理器
func (tq *TaskQueue) StartCleaner(ctx context.Context, interval, maxAge time.Duration) {
	ptm := syncx.NewPeriodicTaskManager()

	// 创建清理任务
	cleanTask := syncx.NewPeriodicTask("queue-cleaner", interval, func(taskCtx context.Context) error {
		tq.Clean(maxAge)
		return nil
	})

	// 添加任务到管理器
	ptm.AddTask(cleanTask)

	// 启动任务管理器（使用传入的 context）
	ptm.StartWithContext(ctx)
}
