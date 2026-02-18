//go:build !cgo

/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-11-03 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-11-03 00:00:00
 * @FilePath: \go-stress\storage\sqlite_stub.go
 * @Description: SQLite 存储的无 CGO 桩实现 - CGO_ENABLED=0 构建时替代 sqlite.go，
 *   使容器化静态构建可行；需要 sqlite 时请用 CGO 构建，或改用 badger
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package storage

import (
	"fmt"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/types"
)

// sqliteCGOEnabled 标记当前二进制是否包含真正的 SQLite 实现
// （测试用它跳过 sqlite 用例，与 sqlite.go 中的同名常量互斥）
const sqliteCGOEnabled = false

// DetailStorage 无 CGO 构建下的占位实现
//
// NewDetailStorage 在此构建下必然失败，这些方法仅为满足 Interface
// 签名而存在（storage factory 以 Interface 返回值编译引用本类型），
// 正常流程永远走不到
type DetailStorage struct{}

// NewDetailStorage 创建 SQLite 存储 - 无 CGO 构建下必然失败
//
// go-sqlite3 依赖 CGO，CGO_ENABLED=0（容器静态构建的常规姿势）下无法编译。
// 失败信息里给出两条出路，而不是让使用者对着一串链接错误发呆
func NewDetailStorage(dbPath, nodeID string, log logger.ILogger) (*DetailStorage, error) {
	return nil, fmt.Errorf(
		"当前二进制以 CGO_ENABLED=0 构建，不含 SQLite 支持（path=%s, nodeID=%s）；"+
			"请改用 -storage badger（纯 Go），或以 CGO 构建后使用 sqlite", dbPath, nodeID)
}

// Write 实现 Interface（不可达）
func (s *DetailStorage) Write(detail *types.RequestResult) {}

// Query 实现 Interface（不可达）
func (s *DetailStorage) Query(offset, limit int, statusFilter StatusFilter, nodeID, taskID string) ([]*types.RequestResult, error) {
	return nil, fmt.Errorf("sqlite storage is not built into this binary (CGO_ENABLED=0)")
}

// Count 实现 Interface（不可达）
func (s *DetailStorage) Count(statusFilter StatusFilter, nodeID, taskID string) (int, error) {
	return 0, fmt.Errorf("sqlite storage is not built into this binary (CGO_ENABLED=0)")
}

// Close 实现 Interface（不可达）
func (s *DetailStorage) Close() error { return nil }

// GetNodeID 实现 Interface（不可达）
func (s *DetailStorage) GetNodeID() string { return "" }
