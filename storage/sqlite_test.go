//go:build cgo

/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 15:30:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 16:05:17
 * @FilePath: \go-stress\storage\sqlite_test.go
 * @Description: SQLite 存储测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetailStorageCloseTerminates Close 必须返回，且不丢未落盘的记录
//
// 回归用，曾有两个缺陷叠加导致 Close 永久阻塞：
//  1. NewDetailStorage 漏了 ctx/cancel 的初始化，batchWriter 的 EventLoop 仅在
//     ctx.Done() 时退出，ctx 为 nil 时该分支永不触发
//  2. Close 走的是 close(writeChan)，而 EventLoop 基于 reflect.Select，已关闭的
//     通道恒为 ready，事件循环就地空转打满 CPU，wg.Wait() 永不返回
//
// 本用例给 Close 加超时断言：一旦再退回那套写法，测试会在 10 秒内失败而非挂死
func TestDetailStorageCloseTerminates(t *testing.T) {
	const records = 250 // 跨过 batchSize(100)，确保既有已落盘批次也有残留批次

	dbPath := filepath.Join(t.TempDir(), "close-test.db")
	strg, err := NewDetailStorage(dbPath, "close-test", logger.NewEmptyLogger())
	require.NoError(t, err)

	for i := 0; i < records; i++ {
		strg.Write(&RequestResult{
			ID:         fmt.Sprintf("req-%d", i),
			NodeID:     "close-test",
			TaskID:     "task-1",
			Success:    true,
			StatusCode: 200,
			Timestamp:  time.Now(),
		})
	}

	// Close 在独立协程中执行，主协程用超时兜底，避免死锁把整个测试拖成挂起
	done := make(chan error, 1)
	go func() { done <- strg.Close() }()

	select {
	case closeErr := <-done:
		assert.NoError(t, closeErr)
	case <-time.After(10 * time.Second):
		t.Fatal("Close 超过 10 秒未返回：batchWriter 的事件循环没有退出")
	}

	// Close 应当把通道里残留的记录一并落盘，而非只刷内存中的那一批
	reopened, err := NewDetailStorage(dbPath, "close-test", logger.NewEmptyLogger())
	require.NoError(t, err)
	defer reopened.Close()

	count, err := reopened.Count(StatusFilterAll, "", "")
	require.NoError(t, err)
	assert.Equal(t, records, count, "Close 应排空写入通道，不丢最后一批记录")
}

// TestDetailStorageMigratesLegacySchema 旧库（无 trace_id 列）必须能被升级并正常读写
//
// 回归用：CREATE TABLE IF NOT EXISTS 对已存在的表什么都不做，新增列只能靠
// ALTER TABLE 补；若漏掉这一步，所有按列名查询的旧库都会直接报
// "no such column: trace_id"，且历史行的 trace_id 为 NULL，需用可空类型接收
func TestDetailStorageMigratesLegacySchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// 1. 先造一个"旧版"库：列清单里没有 trace_id，并写入一行历史数据
	legacy, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = legacy.Exec(`
		CREATE TABLE request_details (
			id TEXT PRIMARY KEY, node_id TEXT NOT NULL, task_id TEXT NOT NULL,
			group_id INTEGER, api_name TEXT, timestamp INTEGER NOT NULL,
			url TEXT, method TEXT, query TEXT, headers TEXT, body TEXT,
			duration INTEGER NOT NULL, status_code INTEGER,
			success INTEGER NOT NULL, skipped INTEGER NOT NULL, size INTEGER,
			error TEXT, response_body TEXT, response_headers TEXT,
			verifications TEXT, extracted_vars TEXT
		);
		INSERT INTO request_details VALUES (
			'legacy-1', 'node-old', 'task-old', 1, 'old_api', 1793622796,
			'http://old/api', 'GET', '', '{}', '',
			1000, 200, 1, 0, 128,
			'', '{"ok":true}', '{}', '[]', '{}'
		);
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	// 2. 用当前代码打开同一个库：应自动补列，而非报错
	strg, err := NewDetailStorage(dbPath, "node-old", logger.NewEmptyLogger())
	require.NoError(t, err, "打开旧库应自动完成表结构升级")
	defer strg.Close()

	// 3. 历史行仍可读出，trace_id 为空而非令 Scan 失败
	results, err := strg.Query(0, 10, StatusFilterAll, "", "")
	require.NoError(t, err, "升级后应能按列名查询旧数据")
	require.Len(t, results, 1)
	assert.Equal(t, "legacy-1", results[0].ID)
	assert.Empty(t, results[0].TraceID, "历史行没有 trace_id，应读成空串")

	// 4. 新写入的记录带 trace_id，与旧行共存
	strg.Write(&RequestResult{
		ID: "new-1", NodeID: "node-old", TaskID: "task-old",
		TraceID: "0af7651916cd43dd8448eb211c80319c",
		Success: true, StatusCode: 200, Timestamp: time.Now(),
	})

	// Close 会排空通道并落盘，随后重开校验
	require.NoError(t, strg.Close())

	reopened, err := NewDetailStorage(dbPath, "node-old", logger.NewEmptyLogger())
	require.NoError(t, err)
	defer reopened.Close()

	all, err := reopened.Query(0, 10, StatusFilterAll, "", "")
	require.NoError(t, err)
	require.Len(t, all, 2)

	byID := map[string]string{}
	for _, r := range all {
		byID[r.ID] = r.TraceID
	}
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", byID["new-1"])
	assert.Empty(t, byID["legacy-1"])
}

// TestDetailStorageQueryFilterNotInjectable 过滤参数来自外部，必须走占位符绑定
//
// 回归用：原先是 fmt.Sprintf("node_id = '%s'", nodeID)，而 nodeID/taskID 取自
// 实时报告与 Master 的 HTTP 查询参数，构造出的值可以闭合引号并改写整条 WHERE
func TestDetailStorageQueryFilterNotInjectable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "inject.db")
	strg, err := NewDetailStorage(dbPath, "node-1", logger.NewEmptyLogger())
	require.NoError(t, err)

	strg.Write(&RequestResult{
		ID: "r1", NodeID: "node-1", TaskID: "task-1",
		Success: true, StatusCode: 200, Timestamp: time.Now(),
	})
	require.NoError(t, strg.Close())

	reopened, err := NewDetailStorage(dbPath, "node-1", logger.NewEmptyLogger())
	require.NoError(t, err)
	defer reopened.Close()

	// 基线：正常过滤能命中
	hit, err := reopened.Count(StatusFilterAll, "node-1", "")
	require.NoError(t, err)
	require.Equal(t, 1, hit, "正常过滤应命中 1 条")

	// 注入载荷必须被当作普通字符串比较，命中 0 条且不报错、不返回全表
	for _, payload := range []string{
		`node-x' OR '1'='1`,
		`' OR 1=1 --`,
		`node-1'; DROP TABLE request_details; --`,
	} {
		count, err := reopened.Count(StatusFilterAll, payload, "")
		assert.NoError(t, err, "载荷 %q 不应产生 SQL 错误", payload)
		assert.Equal(t, 0, count, "载荷 %q 应被当作字面值，命中 0 条", payload)
	}

	// 表必须还在（证明上面的 DROP 没有被执行）
	still, err := reopened.Count(StatusFilterAll, "node-1", "")
	require.NoError(t, err, "表应完好无损")
	assert.Equal(t, 1, still)
}
