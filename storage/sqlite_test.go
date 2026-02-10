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
