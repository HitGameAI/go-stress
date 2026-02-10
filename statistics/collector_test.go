/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 15:30:22
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 16:05:17
 * @FilePath: \go-stress\statistics\collector_test.go
 * @Description: 统计收集器测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"sync"
	"testing"

	"github.com/kamalyes/go-toolbox/pkg/syncx"
	"github.com/stretchr/testify/assert"
)

// TestIncrementCounterConcurrent 并发自增不丢计数
//
// 回归用：计数器曾写成 LoadOrStore + Store，读-改-写三步非原子，并发下后写覆盖先写，
// 表现为"失败请求 N 条，但错误表里次数之和小于 N"的自相矛盾报告
func TestIncrementCounterConcurrent(t *testing.T) {
	const (
		goroutines = 50
		perG       = 200
	)

	t.Run("单键并发自增", func(t *testing.T) {
		m := syncx.NewMap[string, uint64]()

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					incrementCounter(m, "boom")
				}
			}()
		}
		wg.Wait()

		got, ok := m.Load("boom")
		assert.True(t, ok, "键应当存在")
		assert.Equal(t, uint64(goroutines*perG), got, "计数不应被并发覆盖吞掉")
	})

	t.Run("多键并发自增", func(t *testing.T) {
		m := syncx.NewMap[int, uint64]()

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					incrementCounter(m, j%5) // 5 个键上制造争用
				}
			}(i)
		}
		wg.Wait()

		var total uint64
		m.Range(func(_ int, v uint64) bool {
			total += v
			return true
		})
		assert.Equal(t, uint64(goroutines*perG), total, "各键计数之和应等于自增总次数")
	})
}
