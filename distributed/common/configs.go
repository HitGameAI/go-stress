/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-23 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-24 00:02:15
 * @FilePath: \go-stress\distributed\common\configs.go
 * @Description: 配置结构体定义
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package common

import (
	"time"
)

// DistributedConfig 分布式配置
type DistributedConfig struct {
	MasterAddr     string         `json:"master_addr" yaml:"master_addr"`
	SlaveCount     int            `json:"slave_count" yaml:"slave_count"`
	SlaveSelector  SelectStrategy `json:"slave_selector" yaml:"slave_selector"`
	TaskSplitter   SplitStrategy  `json:"task_splitter" yaml:"task_splitter"`
	SlaveFilter    *SlaveFilter   `json:"slave_filter" yaml:"slave_filter"` // Slave 筛选条件
	EnableTLS      bool           `json:"enable_tls" yaml:"enable_tls"`
	CertFile       string         `json:"cert_file" yaml:"cert_file"`
	KeyFile        string         `json:"key_file" yaml:"key_file"`
	ReportInterval int            `json:"report_interval" yaml:"report_interval"`
	BufferSize     int            `json:"buffer_size" yaml:"buffer_size"`
}

// MasterConfig Master 配置
type MasterConfig struct {
	GRPCPort          int           `json:"grpc_port" yaml:"grpc_port"`
	HTTPPort          int           `json:"http_port" yaml:"http_port"`
	HeartbeatInterval time.Duration `json:"heartbeat_interval" yaml:"heartbeat_interval"`
	HeartbeatTimeout  time.Duration `json:"heartbeat_timeout" yaml:"heartbeat_timeout"`
	MaxFailures       int           `json:"max_failures" yaml:"max_failures"`
	SlaveFilter       *SlaveFilter  `json:"slave_filter" yaml:"slave_filter"` // Slave 筛选条件
	EnableTLS         bool          `json:"enable_tls" yaml:"enable_tls"`
	CertFile          string        `json:"cert_file" yaml:"cert_file"`
	KeyFile           string        `json:"key_file" yaml:"key_file"`
	Secret            string        `json:"secret" yaml:"secret"`                     // Token 签名密钥
	TokenExpiration   time.Duration `json:"token_expiration" yaml:"token_expiration"` // Token 过期时间
	TokenIssuer       string        `json:"token_issuer" yaml:"token_issuer"`         // Token 签发人

	// Slave 数量计算配置
	WorkersPerSlave int `json:"workers_per_slave" yaml:"workers_per_slave"` // 每个 Slave 承担的 Worker 数量,默认 100
	MinSlaveCount   int `json:"min_slave_count" yaml:"min_slave_count"`     // 最小 Slave 数量,默认 1

	// Snowflake WorkerID（0-31）
	// 多 Master 部署时各副本须不同，否则会生成重复的任务 ID；0 表示未设置，
	// 由 NewMaster 兜底为 1（保持单机行为不变）
	WorkerID int `json:"worker_id" yaml:"worker_id"`
}

// SlaveConfig Slave 配置
type SlaveConfig struct {
	SlaveID         string            `json:"slave_id" yaml:"slave_id"`
	MasterAddr      string            `json:"master_addr" yaml:"master_addr"`
	GRPCPort        int32             `json:"grpc_port" yaml:"grpc_port"`
	RealtimePort    int               `json:"realtime_port" yaml:"realtime_port"` // 实时报告服务器端口（0表示禁用）
	Region          string            `json:"region" yaml:"region"`
	Labels          map[string]string `json:"labels" yaml:"labels"`
	MaxConcurrency  int               `json:"max_concurrency" yaml:"max_concurrency"` // 最大并发任务数
	CanReuse        bool              `json:"can_reuse" yaml:"can_reuse"`             // 是否允许复用
	EnableTLS       bool              `json:"enable_tls" yaml:"enable_tls"`
	CertFile        string            `json:"cert_file" yaml:"cert_file"`
	ReportBuffer    int               `json:"report_buffer" yaml:"report_buffer"`
	ReportInterval  time.Duration     `json:"report_interval" yaml:"report_interval"`
	ResourceMonitor bool              `json:"resource_monitor" yaml:"resource_monitor"` // 是否启用资源监控
	// AbortOnMasterLost Master 失联超过阈值（3 个心跳周期）时是否中止当前任务。
	// 主备切换窗口内旧任务会沦为"孤儿"（新主不认识、无法停止、报告残缺），
	// 开启后任务跟随 Master 存亡；默认关闭，任务跑完但报告可能残缺
	AbortOnMasterLost bool `json:"abort_on_master_lost" yaml:"abort_on_master_lost"`
}
