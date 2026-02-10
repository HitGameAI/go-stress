/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 10:37:11
 * @FilePath: \go-stress\storage\storage_factory_test.go
 * @Description: 存储工厂测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/stretchr/testify/assert"
)

func TestStorageFactory(t *testing.T) {
	log := logger.NewLogger()
	factory := NewStorageFactory(log)

	t.Run("GetSupportedTypes", func(t *testing.T) {
		types := factory.GetSupportedTypes()
		assert.Equal(t, 3, len(types))
		assert.Contains(t, types, StorageModeMemory)
		assert.Contains(t, types, StorageModeSQLite)
		assert.Contains(t, types, StorageModeBadger)
	})

	t.Run("ValidateConfig", func(t *testing.T) {
		// nil 配置
		err := factory.ValidateConfig(nil)
		assert.Error(t, err)

		// 缺少 NodeID
		err = factory.ValidateConfig(&StorageConfig{
			Type: StorageModeMemory,
		})
		assert.Error(t, err)

		// SQLite 缺少 Path
		err = factory.ValidateConfig(&StorageConfig{
			Type:   StorageModeSQLite,
			NodeID: "test",
		})
		assert.Error(t, err)

		// 正确的 Memory 配置
		err = factory.ValidateConfig(&StorageConfig{
			Type:   StorageModeMemory,
			NodeID: "test",
		})
		assert.NoError(t, err)

		// 正确的 SQLite 配置
		err = factory.ValidateConfig(&StorageConfig{
			Type:   StorageModeSQLite,
			NodeID: "test",
			Path:   "./test.db",
		})
		assert.NoError(t, err)
	})

	t.Run("CreateMemoryStorage", func(t *testing.T) {
		storage, err := factory.CreateStorage(&StorageConfig{
			Type:   StorageModeMemory,
			NodeID: "test-memory",
		})

		assert.NoError(t, err)
		assert.NotNil(t, storage)
		defer storage.Close()

		// 写入测试数据
		detail := &RequestResult{
			ID:        "test-1",
			NodeID:    "test-memory",
			TaskID:    "task-1",
			Success:   true,
			Timestamp: time.Now(),
		}
		storage.Write(detail)

		// 查询
		results, err := storage.Query(0, 10, StatusFilterAll, "", "")
		assert.NoError(t, err)
		assert.Equal(t, 1, len(results))

		// 统计
		count, err := storage.Count(StatusFilterAll, "", "")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("CreateSQLiteStorage", func(t *testing.T) {
		// t.TempDir 而非固定路径：SQLite 开 WAL 后还会生成 -shm/-wal 两个边车文件，
		// os.Remove(dbPath) 删不掉它们，残留既污染仓库，又让下一轮跑在旧库上
		// 撞出 UNIQUE constraint failed；TempDir 由 testing 负责整目录清理
		dbPath := filepath.Join(t.TempDir(), "test-storage.db")

		storage, err := factory.CreateStorage(&StorageConfig{
			Type:   StorageModeSQLite,
			NodeID: "test-sqlite",
			Path:   dbPath,
		})

		assert.NoError(t, err)
		assert.NotNil(t, storage)
		defer storage.Close()

		// 写入测试数据
		detail := &RequestResult{
			ID:        "test-2",
			NodeID:    "test-sqlite",
			TaskID:    "task-2",
			Success:   false,
			Timestamp: time.Now(),
		}
		storage.Write(detail)

		// 等待异步写入
		time.Sleep(2 * time.Second)

		// 查询
		results, err := storage.Query(0, 10, StatusFilterAll, "", "")
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, len(results), 1)
	})

	t.Run("CreateBadgerStorage", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "test-badger-data")

		storage, err := factory.CreateStorage(&StorageConfig{
			Type:   StorageModeBadger,
			NodeID: "test-badger",
			Path:   dbPath,
		})

		assert.NoError(t, err)
		assert.NotNil(t, storage)
		defer storage.Close()

		// 写入测试数据
		detail := &RequestResult{
			ID:        "test-3",
			NodeID:    "test-badger",
			TaskID:    "task-3",
			Success:   true,
			Skipped:   false,
			Timestamp: time.Now(),
		}
		storage.Write(detail)

		// 等待异步写入
		time.Sleep(2 * time.Second)

		// 查询
		results, err := storage.Query(0, 10, StatusFilterAll, "", "")
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, len(results), 1)

		// 统计
		count, err := storage.Count(StatusFilterSuccess, "", "")
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1)
	})

	t.Run("UnsupportedStorageType", func(t *testing.T) {
		storage, err := factory.CreateStorage(&StorageConfig{
			Type:   "redis",
			NodeID: "test",
		})

		assert.Error(t, err)
		assert.Nil(t, storage)
		assert.Contains(t, err.Error(), "不支持的存储类型")
	})
}

func TestStorageFilter(t *testing.T) {
	log := logger.NewLogger()
	factory := NewStorageFactory(log)

	// 创建 BadgerDB 存储用于过滤测试
	dbPath := filepath.Join(t.TempDir(), "test-filter-badger")

	storage, err := factory.CreateStorage(&StorageConfig{
		Type:   StorageModeBadger,
		NodeID: "filter-test",
		Path:   dbPath,
	})
	assert.NoError(t, err)
	defer storage.Close()

	// 写入测试数据
	testData := []*RequestResult{
		{ID: "1", NodeID: "node-1", TaskID: "task-1", Success: true, Timestamp: time.Now()},
		{ID: "2", NodeID: "node-1", TaskID: "task-2", Success: false, Timestamp: time.Now()},
		{ID: "3", NodeID: "node-2", TaskID: "task-1", Success: true, Timestamp: time.Now()},
		{ID: "4", NodeID: "node-2", TaskID: "task-2", Skipped: true, Timestamp: time.Now()},
	}

	for _, data := range testData {
		storage.Write(data)
	}

	// 等待写入
	time.Sleep(2 * time.Second)

	t.Run("FilterByNodeID", func(t *testing.T) {
		count, err := storage.Count(StatusFilterAll, "node-1", "")
		assert.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("FilterByTaskID", func(t *testing.T) {
		count, err := storage.Count(StatusFilterAll, "", "task-1")
		assert.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("FilterByNodeAndTask", func(t *testing.T) {
		count, err := storage.Count(StatusFilterAll, "node-1", "task-1")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("FilterByStatus", func(t *testing.T) {
		count, err := storage.Count(StatusFilterSuccess, "", "")
		assert.NoError(t, err)
		assert.Equal(t, 2, count)

		count, err = storage.Count(StatusFilterFailed, "", "")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)

		count, err = storage.Count(StatusFilterSkipped, "", "")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}
