/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-25 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-25 11:57:20
 * @FilePath: \go-stress\bootstrap\master.go
 * @Description: Master 模式启动器
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	kube "github.com/kamalyes/go-kube-runtime"
	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/distributed/common"
	"github.com/kamalyes/go-stress/distributed/master"
)

// MasterOptions Master 启动选项
type MasterOptions struct {
	GRPCPort int
	HTTPPort int
	Secret   string
	Logger   logger.ILogger

	// Snowflake WorkerID（0-31）
	// 多 Master 副本部署时各副本必须不同，否则任务 ID 冲突；
	// 0 视为未设置，由 NewMaster 兜底为 1（单机行为不变）
	WorkerID int

	// Slave 数量计算配置
	WorkersPerSlave int // 每个 Slave 承担的 Worker 数量,默认 100
	MinSlaveCount   int // 最小需要的 Slave 数量,默认 1

	// Master 配置
	HeartbeatInterval time.Duration // 心跳间隔,默认 5s
	HeartbeatTimeout  time.Duration // 心跳超时,默认 15s
	MaxFailures       int           // 最大失败次数,默认 3
	TokenExpiration   time.Duration // Token 过期时间,默认 24h
	TokenIssuer       string        // Token 签发者,默认 "go-stress-master"
}

// RunMaster 运行 Master 节点
//
// 开启 GO_STRESS_LEADER_ELECT=true 且处于 in-cluster 环境时，先经 K8s
// Lease 选主再运行：多副本部署中同一时刻只有主副本提供服务，备副本待命
func RunMaster(opts MasterOptions) error {
	opts.Logger.Info("🎯 启动 Master 节点...")

	masterCfg := &common.MasterConfig{
		GRPCPort:          opts.GRPCPort,
		HTTPPort:          opts.HTTPPort,
		HeartbeatInterval: opts.HeartbeatInterval, // 由 master.go 中 mathx.IfZero 兜底为 5s
		HeartbeatTimeout:  opts.HeartbeatTimeout,  // 由 master.go 中 mathx.IfZero 兜底为 15s
		MaxFailures:       opts.MaxFailures,       // 由 master.go 中 mathx.IfNotZero 兜底为 3
		Secret:            opts.Secret,            // 由 master.go 中 mathx.IfEmpty 兜底为默认密钥
		TokenExpiration:   opts.TokenExpiration,   // 由 master.go 中 mathx.IfZero 兜底为 24h
		TokenIssuer:       opts.TokenIssuer,       // 由 master.go 中 mathx.IfEmpty 兜底为 "go-stress-master"
		WorkersPerSlave:   opts.WorkersPerSlave,   // 由 master.go 中 mathx.IfNotZero 兜底为 100
		MinSlaveCount:     opts.MinSlaveCount,     // 由 master.go 中 mathx.IfNotZero 兜底为 1
		WorkerID:          opts.WorkerID,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		opts.Logger.Warn("⚠️  收到中断信号，正在停止...")
		cancel()
	}()

	// runOnce 构建 Master 并阻塞运行直到 ctx 结束
	//
	// 选主模式下失去主身份会取消 runCtx，runOnce 随之返回错误，
	// 进程退出由 K8s 重新拉起——旧 Master 的内存态不可原地复用
	runOnce := func(runCtx context.Context) error {
		m, err := master.NewMaster(masterCfg, opts.Logger)
		if err != nil {
			return fmt.Errorf("创建 Master 失败: %w", err)
		}
		// ctx 结束（含失去主身份）时停服，让 GracefulStop 收掉 gRPC/HTTP
		go func() {
			<-runCtx.Done()
			if err := m.Stop(); err != nil && runCtx.Err() != nil {
				opts.Logger.WarnKV("停止 Master", "error", err)
			}
		}()

		if err := m.Start(runCtx); err != nil {
			return fmt.Errorf("启动 Master 失败: %w", err)
		}
		<-runCtx.Done()
		return runCtx.Err()
	}

	// K8s 多副本部署：Lease 选主，仅主副本运行
	//
	// 选主实现来自 go-kube-runtime（K8s Lease 纯标准库，k3s/k8s 通用），
	// 这里只保留 go-stress 既有的部署契约：
	//   - 开关：GO_STRESS_LEADER_ELECT=true
	//   - Lease 名：GO_STRESS_LEASE_NAME 覆盖，默认 go-stress-master
	leaseName := os.Getenv("GO_STRESS_LEASE_NAME")
	if leaseName == "" {
		leaseName = "go-stress-master"
	}
	electionOpts := kube.ElectionOptions{
		EnableEnv: "GO_STRESS_LEADER_ELECT",
		LeaseName: leaseName,
	}
	if kube.LeaderElectionEnabled(electionOpts) {
		err := kube.RunWithLeaderElection(ctx, electionOpts, runOnce, opts.Logger)
		if err != nil {
			return fmt.Errorf("选主运行结束: %w", err)
		}
		opts.Logger.Info("👋 Master 节点已停止")
		return nil
	}

	// 常规单机/裸机路径：ctx.Err() 为 nil 说明是 SIGTERM 正常退出
	if err := runOnce(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	opts.Logger.Info("👋 Master 节点已停止")
	return nil
}
