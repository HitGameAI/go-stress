/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-23 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-23 10:00:00
 * @FilePath: \go-stress\distributed\master\master.go
 * @Description: Master 主控制器
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package master

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/distributed/common"
	pb "github.com/kamalyes/go-stress/distributed/proto"
	"github.com/kamalyes/go-toolbox/pkg/errorx"
	"github.com/kamalyes/go-toolbox/pkg/idgen"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MasterStats Master 统计信息（导出结构体）
// Master 只负责分发任务，不维护任务完成/失败状态
type MasterStats struct {
	Running      bool      `json:"running"`       // Master 是否运行中
	SlaveCount   int       `json:"slave_count"`   // Slave 数量
	TaskPending  int       `json:"task_pending"`  // 待执行任务数（来自 TaskQueue）
	TaskRunning  int       `json:"task_running"`  // 运行中任务数（来自 TaskQueue）
	TaskStopped  int       `json:"task_stopped"`  // 已停止任务数（来自 TaskQueue）
	TaskComplete int       `json:"task_complete"` // 已完成任务数（来自 Slave 报告）
	TaskFailed   int       `json:"task_failed"`   // 失败任务数（来自 Slave 报告）
	Timestamp    time.Time `json:"timestamp"`     // 统计时间戳
}

// Master Master 节点主控制器
type Master struct {
	config       *common.MasterConfig
	slavePool    *SlavePool
	taskQueue    *TaskQueue
	collector    *StatsCollector
	grpcServer   *grpc.Server
	httpServer   *HTTPServer                               // HTTP 服务器（分布式管理）
	slaveClients *syncx.Map[string, pb.SlaveServiceClient] // 使用 syncx.Map 管理 slave_id -> client
	idGenerator  *idgen.SnowflakeGenerator                 // ID 生成器
	logger       logger.ILogger
	running      *syncx.Bool // 使用 syncx.Bool
	cancelFunc   context.CancelFunc
}

// NewMaster 创建 Master 实例
func NewMaster(config *common.MasterConfig, log logger.ILogger) (*Master, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// 使用 mathx 工具函数对配置做兜底处理
	config.HeartbeatInterval = mathx.IfEmpty(config.HeartbeatInterval, 5*time.Second)
	config.HeartbeatTimeout = mathx.IfEmpty(config.HeartbeatTimeout, 15*time.Second)
	config.MaxFailures = mathx.IfNotZero(config.MaxFailures, 3)
	config.Secret = mathx.IfEmpty(config.Secret, "go-stress-secret-key")
	config.TokenExpiration = mathx.IfEmpty(config.TokenExpiration, 24*time.Hour)
	config.TokenIssuer = mathx.IfEmpty(config.TokenIssuer, "go-stress-master")
	config.WorkersPerSlave = mathx.IfNotZero(config.WorkersPerSlave, 100)
	config.MinSlaveCount = mathx.IfNotZero(config.MinSlaveCount, 1)

	// 创建 Slave 选择器
	selector := GetSelector(common.SelectStrategyLeastLoaded, nil)

	// 创建任务分片器
	splitter := GetSplitter(common.SplitStrategyEqual)

	master := &Master{
		config:       config,
		slavePool:    NewSlavePool(selector, config.HeartbeatInterval, config.HeartbeatTimeout, config.MaxFailures, log),
		taskQueue:    NewTaskQueue(splitter, log),
		collector:    NewStatsCollector(1000, log),
		slaveClients: syncx.NewMap[string, pb.SlaveServiceClient](),
		idGenerator:  idgen.NewSnowflakeGenerator(1, 1), // workerID=1, datacenter=1
		running:      syncx.NewBool(false),
		logger:       log,
	}

	return master, nil
}

// Start 启动 Master 服务
func (m *Master) Start(ctx context.Context) error {
	if !m.running.CAS(false, true) {
		return fmt.Errorf("master is already running")
	}

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel

	// 启动各个组件
	go m.slavePool.StartHealthCheck(ctx)
	go m.collector.Start(ctx)
	go m.taskQueue.StartCleaner(ctx, 10*time.Minute, 1*time.Hour)

	// 启动 gRPC 服务器
	if err := m.startGRPCServer(); err != nil {
		return fmt.Errorf("failed to start gRPC server: %w", err)
	}

	// 启动 HTTP 服务器（分布式管理）
	if m.config.HTTPPort > 0 {
		m.httpServer = NewHTTPServer(m, m.config.HTTPPort, m.logger)
		if err := m.httpServer.Start(); err != nil {
			return fmt.Errorf("failed to start HTTP server: %w", err)
		}
	}

	m.logger.InfoKV("Master started successfully",
		"grpc_port", m.config.GRPCPort,
		"http_port", m.config.HTTPPort)

	return nil
}

// Stop 停止 Master 服务
func (m *Master) Stop() error {
	if !m.running.CAS(true, false) {
		return fmt.Errorf("master is not running")
	}

	m.logger.Info("Stopping master...")

	// 停止所有组件
	if m.cancelFunc != nil {
		m.cancelFunc()
	}

	// 停止 HTTP 服务器
	if m.httpServer != nil {
		if err := m.httpServer.Stop(); err != nil {
			m.logger.ErrorKV("Failed to stop HTTP server", "error", err)
		}
	}

	// 停止 gRPC 服务器
	if m.grpcServer != nil {
		m.grpcServer.GracefulStop()
	}

	m.logger.Info("Master stopped")
	return nil
}

// startGRPCServer 启动 gRPC 服务器
func (m *Master) startGRPCServer() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", m.config.GRPCPort))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	// 创建 gRPC 服务器
	m.grpcServer = grpc.NewServer()

	// 注册服务
	masterService := NewMasterServiceServer(m, m.logger)
	pb.RegisterMasterServiceServer(m.grpcServer, masterService)

	// 启动服务器
	go func() {
		m.logger.InfoKV("gRPC server listening", "port", m.config.GRPCPort)
		if err := m.grpcServer.Serve(lis); err != nil {
			m.logger.ErrorKV("gRPC server error", "error", err)
		}
	}()

	return nil
}

// SubmitConfig 提交配置（推荐使用，直接接受 config.Config）
func (m *Master) SubmitConfig(cfg *config.Config) (string, error) {
	// 序列化配置
	configData, err := json.Marshal(cfg)
	if err != nil {
		return "", errorx.WrapError("failed to marshal config", err)
	}

	// 转换为 Task
	task := &common.Task{
		ID:           m.idGenerator.GenerateRequestID(),
		Protocol:     string(cfg.Protocol),
		Target:       cfg.URL,
		TotalWorkers: int(cfg.Concurrency),
		Duration:     int(cfg.Requests),
		ConfigData:   configData,
	}

	if err := m.SubmitTask(task); err != nil {
		return "", err
	}

	return task.ID, nil
}

// SubmitTask 提交任务（不立即执行,等待手动启动）
func (m *Master) SubmitTask(task *common.Task) error {
	return m.SubmitTaskWithOptions(task, false)
}

// SubmitTaskWithOptions 提交任务并指定是否自动执行
func (m *Master) SubmitTaskWithOptions(task *common.Task, autoStart bool) error {
	// 使用 mathx.IfEmpty 确保有 task.ID
	task.ID = mathx.IfEmpty(task.ID, m.idGenerator.GenerateRequestID())

	// 提交到任务队列
	if err := m.taskQueue.Submit(task); err != nil {
		return errorx.WrapError("failed to submit task", err)
	}

	// 根据参数决定是否立即执行
	if autoStart {
		go m.processTask(task)
	} else {
		m.logger.InfoKV("Task submitted, waiting for manual start",
			"task_id", task.ID,
			"required_slaves", m.getRequiredSlaveCount(task),
			"available_slaves", m.slavePool.Count())
	}

	return nil
}

// GenerateTaskID 生成任务ID（对外暴露）
func (m *Master) GenerateTaskID() string {
	return m.idGenerator.GenerateRequestID()
}

// TaskStartOptions 任务启动选项
type TaskStartOptions struct {
	SlaveIDs    []string // 指定 Slave ID 列表（可选）
	SlaveRegion string   // 指定区域（可选）
}

// StartTaskWithOptions 启动指定任务（支持 Slave 过滤）
func (m *Master) StartTaskWithOptions(taskID string, options *TaskStartOptions) error {
	task, exists := m.taskQueue.Get(taskID)
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if task.State != common.TaskStatePending {
		return fmt.Errorf("task %s is not in pending state (current: %s)", taskID, task.State)
	}

	// 根据选项过滤 Slave
	var slaves []*common.SlaveInfo
	if options != nil {
		slaves = m.filterSlaves(options)
		if len(slaves) == 0 {
			return fmt.Errorf("no slaves match the filter criteria")
		}
	} else {
		// 使用默认策略选择 Slave
		required := m.getRequiredSlaveCount(task)
		slaves = m.slavePool.Select(required)
		if len(slaves) == 0 {
			return fmt.Errorf("no available slaves")
		}
	}

	m.logger.InfoKV("Task starting",
		"task_id", taskID,
		"slave_count", len(slaves),
		"slave_ids", m.getSlaveIDs(slaves))

	// 异步执行任务（传入过滤后的 Slave 列表）
	go m.processTaskWithSlaves(task, slaves)

	return nil
}

// filterSlaves 根据选项过滤 Slave
func (m *Master) filterSlaves(options *TaskStartOptions) []*common.SlaveInfo {
	allSlaves := m.slavePool.GetAllSlaves()
	var filtered []*common.SlaveInfo

	m.logger.DebugKV("Filtering slaves",
		"total_slaves", len(allSlaves),
		"requested_ids", options.SlaveIDs,
		"requested_region", options.SlaveRegion)

	// 如果指定了 Slave ID 列表
	if len(options.SlaveIDs) > 0 {
		slaveIDSet := make(map[string]bool)
		for _, id := range options.SlaveIDs {
			slaveIDSet[id] = true
		}

		for _, slave := range allSlaves {
			m.logger.DebugKV("Checking slave",
				"slave_id", slave.ID,
				"state", slave.State,
				"requested", slaveIDSet[slave.ID])

			if slaveIDSet[slave.ID] && slave.State == common.SlaveStateIdle {
				filtered = append(filtered, slave)
			}
		}

		m.logger.DebugKV("Filtered slaves by ID",
			"requested_count", len(options.SlaveIDs),
			"matched_count", len(filtered))
		return filtered
	}

	// 如果指定了区域
	if options.SlaveRegion != "" {
		for _, slave := range allSlaves {
			if slave.Region == options.SlaveRegion && slave.State == common.SlaveStateIdle {
				filtered = append(filtered, slave)
			}
		}
		return filtered
	}

	// 没有过滤条件，返回所有空闲 Slave
	for _, slave := range allSlaves {
		if slave.State == common.SlaveStateIdle {
			filtered = append(filtered, slave)
		}
	}
	return filtered
}

// getSlaveIDs 提取 Slave ID 列表
func (m *Master) getSlaveIDs(slaves []*common.SlaveInfo) []string {
	ids := make([]string, len(slaves))
	for i, slave := range slaves {
		ids[i] = slave.ID
	}
	return ids
}

// StartAllPendingTasks 启动所有待执行的任务
func (m *Master) StartAllPendingTasks() (started []string, failed map[string]string) {
	pendingTasks := m.taskQueue.GetPending()
	started = make([]string, 0)
	failed = make(map[string]string)

	for _, task := range pendingTasks {
		if err := m.StartTaskWithOptions(task.ID, nil); err != nil {
			failed[task.ID] = err.Error()
			m.logger.WarnKV("Failed to start task", "task_id", task.ID, "error", err)
		} else {
			started = append(started, task.ID)
		}
	}

	return started, failed
}

// processTask 处理任务（使用默认 Slave 选择策略）
func (m *Master) processTask(task *common.Task) {
	requiredCount := m.getRequiredSlaveCount(task)
	slaves := m.slavePool.Select(requiredCount)
	m.processTaskWithSlaves(task, slaves)
}

// processTaskWithSlaves 处理任务（使用指定的 Slave 列表）
func (m *Master) processTaskWithSlaves(task *common.Task, slaves []*common.SlaveInfo) {
	m.logger.InfoKV("Processing task",
		"task_id", task.ID,
		"slave_count", len(slaves),
		"total_workers", task.TotalWorkers)

	// 检查 Slave 列表
	if len(slaves) == 0 {
		m.logger.ErrorKV("No available slaves", "task_id", task.ID)
		m.taskQueue.MoveToFailed(task.ID, "no available slaves")
		return
	}

	// 分片任务
	subTasks, err := m.taskQueue.Split(task, slaves)
	if err != nil {
		errMsg := errorx.WrapError("failed to split task", err).Error()
		m.logger.ErrorKV(errMsg, "task_id", task.ID)
		m.taskQueue.MoveToFailed(task.ID, errMsg)
		return
	}

	// 更新任务状态
	task.AssignedSlaves = make([]string, len(slaves))
	for i, slave := range slaves {
		task.AssignedSlaves[i] = slave.ID
	}

	m.logger.InfoKV("Task assigned slaves updated",
		"task_id", task.ID,
		"assigned_slaves", task.AssignedSlaves)

	m.taskQueue.MoveToRunning(task.ID)

	// 使用 syncx.Parallel 并发分发子任务到各个 Slave
	successCount := syncx.NewInt32(0)

	syncx.NewParallelSliceExecutor[*common.SubTask, bool](subTasks).
		OnSuccess(func(idx int, st *common.SubTask, result bool) {
			successCount.Add(1)
			m.slavePool.AddTask(st.SlaveID, st.TaskID)
		}).
		OnError(func(idx int, st *common.SubTask, err error) {
			m.logger.ErrorKV("Failed to dispatch subtask",
				"task_id", st.TaskID,
				"subtask_id", st.SubTaskID,
				"slave_id", st.SlaveID,
				"error", err)
		}).
		Execute(func(idx int, st *common.SubTask) (bool, error) {
			err := m.dispatchSubTask(st)
			return err == nil, err
		})

	success := int(successCount.Load())
	if success == 0 {
		m.logger.ErrorKV("All subtasks failed", "task_id", task.ID)
		m.taskQueue.MoveToFailed(task.ID, "all subtasks failed")
	} else if success < len(subTasks) {
		m.logger.WarnKV("Some subtasks failed",
			"task_id", task.ID,
			"success", success,
			"total", len(subTasks))
	}

	m.logger.InfoKV("Task distributed",
		"task_id", task.ID,
		"slave_count", len(slaves),
		"subtask_count", len(subTasks),
		"success", success)
}

// getRequiredSlaveCount 计算所需的 Slave 数量
func (m *Master) getRequiredSlaveCount(task *common.Task) int {
	// 配置值已在 NewMaster 中使用 mathx 工具函数做了兜底处理,直接使用
	count := mathx.Max(task.TotalWorkers/m.config.WorkersPerSlave, m.config.MinSlaveCount) // 最多使用所有可用的 Slave
	available := m.slavePool.Count()
	if count > available {
		count = available
	}

	return count
}

// GetSlavePool 获取 Slave 池
func (m *Master) GetSlavePool() *SlavePool {
	return m.slavePool
}

// GetTaskQueue 获取任务队列
func (m *Master) GetTaskQueue() *TaskQueue {
	return m.taskQueue
}

// GetCollector 获取统计收集器
func (m *Master) GetCollector() *StatsCollector {
	return m.collector
}

// GetStats 获取系统状态
// 任务队列状态（Pending/Running/Stopped）来自 TaskQueue
// 任务完成/失败状态来自 StatsCollector（Slave 报告）
func (m *Master) GetStats() *MasterStats {
	queueStats := m.taskQueue.Stats()
	collectorStats := m.collector.GetStats() // 获取收集器的统计信息

	return &MasterStats{
		Running:      m.running.Load(),
		SlaveCount:   m.slavePool.Count(),
		TaskPending:  queueStats.Pending,
		TaskRunning:  queueStats.Running,
		TaskStopped:  queueStats.Stopped,
		TaskComplete: collectorStats.Complete, // 从收集器获取完成数
		TaskFailed:   collectorStats.Failed,   // 从收集器获取失败数
		Timestamp:    time.Now(),
	}
}

// dispatchSubTask 分发子任务到指定 Slave
func (m *Master) dispatchSubTask(subTask *common.SubTask) error {
	// 获取 Slave 客户端
	client, err := m.GetSlaveClient(subTask.SlaveID)
	if err != nil {
		return fmt.Errorf("failed to get slave client: %w", err)
	}

	// 构建任务配置
	taskConfig := &pb.TaskConfig{
		TaskId:         subTask.TaskID,
		WorkerCount:    int32(subTask.WorkerCount),
		ConfigData:     subTask.Config,
		ReportInterval: 1, // 默认 1 秒上报一次
	}

	// 调用 Slave 的 ExecuteTask 方法
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ExecuteTask(ctx, taskConfig)
	if err != nil {
		return fmt.Errorf("failed to execute task on slave: %w", err)
	}

	if !resp.Accepted {
		return fmt.Errorf("task rejected by slave: %s", resp.Message)
	}

	m.logger.InfoKV("Subtask dispatched",
		"task_id", subTask.TaskID,
		"subtask_id", subTask.SubTaskID,
		"slave_id", subTask.SlaveID,
		"workers", subTask.WorkerCount)

	return nil
}

// GetSlaveClient 获取或创建 Slave 客户端 - 使用 syncx.Map
func (m *Master) GetSlaveClient(slaveID string) (pb.SlaveServiceClient, error) {
	// 从 Map 中加载
	client, exists := m.slaveClients.Load(slaveID)
	if exists {
		return client, nil
	}

	// 获取 Slave 信息
	slave, exists := m.slavePool.Get(slaveID)
	if !exists {
		return nil, fmt.Errorf("slave %s not found", slaveID)
	}

	// 创建 gRPC 连接
	addr := fmt.Sprintf("%s:%d", slave.IP, slave.GRPCPort)
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create slave client: %w", err)
	}

	newClient := pb.NewSlaveServiceClient(conn)

	// 使用 LoadOrStore 避免并发创建
	actualClient, _ := m.slaveClients.LoadOrStore(slaveID, newClient)

	return actualClient, nil
}

// StopTask 停止任务 - 使用 syncx.Parallel
func (m *Master) StopTask(taskID string) error {
	task, exists := m.taskQueue.Get(taskID)
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	// 使用 syncx.Parallel 并发向所有分配的 Slave 发送停止指令
	syncx.NewParallelSliceExecutor[string, bool](task.AssignedSlaves).
		OnError(func(idx int, slaveID string, err error) {
			m.logger.ErrorKV("Failed to stop task on slave", "slave_id", slaveID, "error", err)
		}).
		Execute(func(idx int, slaveID string) (bool, error) {
			client, err := m.GetSlaveClient(slaveID)
			if err != nil {
				return false, err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err = client.StopTask(ctx, &pb.StopRequest{
				SlaveId: slaveID,
				TaskId:  taskID,
				Force:   false,
			})
			return err == nil, err
		})

	// 更新任务状态
	m.taskQueue.Cancel(taskID)

	return nil
}
