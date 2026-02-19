/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-23 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-24 00:21:29
 * @FilePath: \go-stress\distributed\master\service.go
 * @Description: Master gRPC 服务实现
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package master

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/distributed/common"
	pb "github.com/kamalyes/go-stress/distributed/proto"
	"github.com/kamalyes/go-toolbox/pkg/sign"
)

// MasterServiceServer Master 服务实现
type MasterServiceServer struct {
	pb.UnimplementedMasterServiceServer
	master *Master
	logger logger.ILogger
}

// NewMasterServiceServer 创建 Master 服务
func NewMasterServiceServer(master *Master, log logger.ILogger) *MasterServiceServer {
	return &MasterServiceServer{
		master: master,
		logger: log,
	}
}

// RegisterSlave 注册 Slave
func (s *MasterServiceServer) RegisterSlave(ctx context.Context, req *pb.SlaveInfo) (*pb.RegisterResponse, error) {
	s.logger.InfoContextKV(ctx, "Slave registering",
		"slave_id", req.SlaveId,
		"hostname", req.Hostname,
		"ip", req.Ip)

	// 转换为内部类型
	slaveInfo := &common.SlaveInfo{
		ID:            req.SlaveId,
		Hostname:      req.Hostname,
		IP:            req.Ip,
		GRPCPort:      req.GrpcPort,
		CPUCores:      int(req.CpuCores),
		Memory:        req.Memory,
		Version:       req.Version,
		Region:        req.Region,
		Labels:        req.Labels,
		State:         common.SlaveStateIdle,
		RegisteredAt:  time.Now(),
		LastHeartbeat: time.Now(),
	}

	// 注册到池中
	if err := s.master.GetSlavePool().Register(slaveInfo); err != nil {
		s.logger.ErrorContextKV(ctx, "Failed to register slave",
			"slave_id", req.SlaveId,
			"error", err)
		return &pb.RegisterResponse{
			Success: false,
			Message: err.Error(),
		}, err
	}

	s.logger.InfoContextKV(ctx, "Slave registered successfully", "slave_id", req.SlaveId)

	return &pb.RegisterResponse{
		Success:           true,
		Message:           "Registered successfully",
		Token:             s.generateToken(req.SlaveId),
		HeartbeatInterval: 5, // 5 seconds
	}, nil
}

// Heartbeat 心跳处理
func (s *MasterServiceServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	// 更新心跳时间
	if err := s.master.slavePool.UpdateHeartbeat(req.SlaveId); err != nil {
		return &pb.HeartbeatResponse{
			Ok:      false,
			Message: err.Error(),
		}, nil
	}

	// 更新状态
	if req.Status != nil {
		state := protoStateToCommonState(req.Status.State)
		s.master.slavePool.UpdateState(req.SlaveId, state)
	}

	return &pb.HeartbeatResponse{
		Ok:      true,
		Message: "OK",
	}, nil
}

// ReportStats 接收统计数据流
func (s *MasterServiceServer) ReportStats(stream pb.MasterService_ReportStatsServer) error {
	for {
		stats, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.ReportResponse{
				Received: true,
				Message:  "Stats received",
			})
		}
		if err != nil {
			s.logger.ErrorContextKV(stream.Context(), "Error receiving stats", "error", err)
			return err
		}

		// 转换并收集统计数据
		slaveStats := &common.SlaveStats{
			SlaveID:         stats.SlaveId,
			TaskID:          stats.TaskId,
			TotalRequests:   stats.TotalRequests,
			SuccessRequests: stats.SuccessRequests,
			FailedRequests:  stats.FailedRequests,
			AvgLatency:      stats.AvgLatency,
			P95Latency:      stats.P95Latency,
			P99Latency:      stats.P99Latency,
			QPS:             stats.Qps,
			StatusCodes:     convertStatusCodes(stats.StatusCodes),
		}

		// 计算成功率
		if slaveStats.TotalRequests > 0 {
			slaveStats.SuccessRate = float64(slaveStats.SuccessRequests) / float64(slaveStats.TotalRequests) * 100
		}

		s.master.collector.Collect(slaveStats)
	}
}

// ReportTaskCompletion 接收任务完成通知
func (s *MasterServiceServer) ReportTaskCompletion(ctx context.Context, req *pb.TaskCompletionRequest) (*pb.TaskCompletionResponse, error) {
	s.logger.InfoContextKV(ctx, "Received task completion notification",
		"task_id", req.TaskId,
		"slave_id", req.SlaveId,
		"success", req.Success)

	// 更新任务状态
	if req.Success {
		if err := s.master.taskQueue.MoveToComplete(req.TaskId); err != nil {
			// 主备切换后的孤儿任务：新主内存态没有它，但统计已经聚合器归档，
			// 宽容确认，避免 Slave 反复收到失败回执
			if errors.Is(err, ErrTaskNotFoundInRunning) {
				s.logger.WarnContextKV(ctx, "Task unknown to this master (likely after failover), acknowledged",
					"task_id", req.TaskId)
				return &pb.TaskCompletionResponse{
					Acknowledged: true,
					Message:      "Task unknown to this master (likely after failover), stats already archived",
				}, nil
			}
			s.logger.ErrorContextKV(ctx, "Failed to move task to complete", "task_id", req.TaskId, "error", err)
			return &pb.TaskCompletionResponse{
				Acknowledged: false,
				Message:      fmt.Sprintf("Failed to update task state: %v", err),
			}, nil
		}
	} else {
		reason := req.ErrorMessage
		if reason == "" {
			reason = "Task failed without error message"
		}
		if err := s.master.taskQueue.MoveToFailed(req.TaskId, reason); err != nil {
			// 同上：主备切换后的孤儿任务，宽容确认
			if errors.Is(err, ErrTaskNotFoundInRunning) {
				s.logger.WarnContextKV(ctx, "Task unknown to this master (likely after failover), acknowledged",
					"task_id", req.TaskId)
				return &pb.TaskCompletionResponse{
					Acknowledged: true,
					Message:      "Task unknown to this master (likely after failover), stats already archived",
				}, nil
			}
			s.logger.ErrorContextKV(ctx, "Failed to move task to failed", "task_id", req.TaskId, "error", err)
			return &pb.TaskCompletionResponse{
				Acknowledged: false,
				Message:      fmt.Sprintf("Failed to update task state: %v", err),
			}, nil
		}
	}

	return &pb.TaskCompletionResponse{
		Acknowledged: true,
		Message:      "Task completion acknowledged",
	}, nil
}

// UnregisterSlave 注销 Slave
func (s *MasterServiceServer) UnregisterSlave(ctx context.Context, req *pb.UnregisterRequest) (*pb.UnregisterResponse, error) {
	s.logger.InfoContextKV(ctx, "Slave unregistering",
		"slave_id", req.SlaveId,
		"reason", req.Reason)

	if err := s.master.slavePool.Unregister(req.SlaveId); err != nil {
		return &pb.UnregisterResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	return &pb.UnregisterResponse{
		Success: true,
		Message: "Unregistered successfully",
	}, nil
}

// generateToken 生成认证 token
// 使用 HMAC-SHA256 签名算法生成带过期时间的 token
func (s *MasterServiceServer) generateToken(slaveID string) string {
	// 使用 sign 模块创建签名客户端
	type TokenPayload struct {
		SlaveID string `json:"slave_id"`
	}

	// 从配置读取密钥和过期时间
	secretKey := s.master.config.Secret
	if secretKey == "" {
		secretKey = "go-stress-master-default-secret-key" // 默认密钥
	}

	tokenExpiration := s.master.config.TokenExpiration
	if tokenExpiration == 0 {
		tokenExpiration = 24 * time.Hour // 默认 24 小时
	}

	tokenIssuer := s.master.config.TokenIssuer
	if tokenIssuer == "" {
		tokenIssuer = "go-stress-master" // 默认签发人
	}

	client := sign.NewSignerClient[TokenPayload]().
		WithSecretKey([]byte(secretKey)).
		WithExpiration(tokenExpiration).
		WithIssuer(tokenIssuer)

	// 设置算法为 HMAC-SHA256
	if _, err := client.WithAlgorithm(sign.AlgorithmSHA256); err != nil {
		// 降级为简单格式
		return fmt.Sprintf("token-%s-%d", slaveID, time.Now().Unix())
	}

	// 生成 token
	token, err := client.Create(TokenPayload{SlaveID: slaveID})
	if err != nil {
		// 降级为简单格式
		return fmt.Sprintf("token-%s-%d", slaveID, time.Now().Unix())
	}

	return token
}

// protoStateToCommonState 转换状态
func protoStateToCommonState(state pb.AgentState) common.SlaveState {
	switch state {
	case pb.AgentState_AGENT_STATE_IDLE:
		return common.SlaveStateIdle
	case pb.AgentState_AGENT_STATE_RUNNING:
		return common.SlaveStateRunning
	case pb.AgentState_AGENT_STATE_STOPPING:
		return common.SlaveStateStopping
	case pb.AgentState_AGENT_STATE_ERROR:
		return common.SlaveStateError
	case pb.AgentState_AGENT_STATE_OFFLINE:
		return common.SlaveStateOffline
	default:
		return common.SlaveStateIdle
	}
}

// convertStatusCodes 转换状态码
func convertStatusCodes(codes map[string]int64) map[int]int64 {
	result := make(map[int]int64)
	for k, v := range codes {
		var code int
		fmt.Sscanf(k, "%d", &code)
		result[code] = v
	}
	return result
}
