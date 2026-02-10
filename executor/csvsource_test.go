/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:07:55
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:19:30
 * @FilePath: \go-stress\executor\csvsource_test.go
 * @Description: CSV 数据源测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kamalyes/go-stress/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCSV 落一个临时 CSV 文件并返回路径
func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.csv")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func loadSource(t *testing.T, cfg *config.CSVDataConfig) *CSVSource {
	t.Helper()
	data, err := config.LoadCSVData(cfg)
	require.NoError(t, err)
	return NewCSVSource(data)
}

func TestCSVLoadHeaderAndRows(t *testing.T) {
	path := writeCSV(t, "username,password\nalice,p1\nbob,p2\ncarol,p3\n")

	t.Run("首行作为表头", func(t *testing.T) {
		src := loadSource(t, &config.CSVDataConfig{Path: path})
		assert.Equal(t, []string{"username", "password"}, src.Variables())
		assert.Equal(t, 3, src.RowCount())
	})

	t.Run("显式变量名且跳过表头", func(t *testing.T) {
		src := loadSource(t, &config.CSVDataConfig{
			Path:            path,
			Variables:       []string{"u", "p"},
			IgnoreFirstLine: true,
		})
		assert.Equal(t, []string{"u", "p"}, src.Variables())
		assert.Equal(t, 3, src.RowCount())

		vars := map[string]string{}
		require.True(t, newCSVCursor(src).injectInto(vars))
		assert.Equal(t, map[string]string{"u": "alice", "p": "p1"}, vars)
	})

	t.Run("表头两侧空白被去掉", func(t *testing.T) {
		p := writeCSV(t, "  user ,  pass \nalice,p1\n")
		src := loadSource(t, &config.CSVDataConfig{Path: p})
		assert.Equal(t, []string{"user", "pass"}, src.Variables())
	})

	t.Run("尾列缺失的行被补成空串", func(t *testing.T) {
		p := writeCSV(t, "a,b,c\n1,2,3\n4,5\n")
		src := loadSource(t, &config.CSVDataConfig{Path: p})
		require.Equal(t, 2, src.RowCount())

		c := newCSVCursor(src)
		vars := map[string]string{}
		require.True(t, c.injectInto(vars))
		require.True(t, c.injectInto(vars))
		assert.Equal(t, "", vars["c"], "缺失列应为空串而非越界 panic")
	})
}

func TestCSVLoadErrors(t *testing.T) {
	t.Run("路径为空", func(t *testing.T) {
		_, err := config.LoadCSVData(&config.CSVDataConfig{})
		assert.Error(t, err)
	})
	t.Run("文件不存在", func(t *testing.T) {
		_, err := config.LoadCSVData(&config.CSVDataConfig{Path: "/nonexistent/x.csv"})
		assert.Error(t, err)
	})
	t.Run("只有表头没有数据行", func(t *testing.T) {
		_, err := config.LoadCSVData(&config.CSVDataConfig{Path: writeCSV(t, "a,b\n")})
		assert.ErrorContains(t, err, "没有数据行")
	})
	t.Run("sharing 取值非法", func(t *testing.T) {
		_, err := config.LoadCSVData(&config.CSVDataConfig{
			Path:    writeCSV(t, "a\n1\n"),
			Sharing: "bogus",
		})
		assert.ErrorContains(t, err, "sharing")
	})
}

func TestCSVRecycle(t *testing.T) {
	path := writeCSV(t, "id\n1\n2\n")

	t.Run("默认回绕", func(t *testing.T) {
		src := loadSource(t, &config.CSVDataConfig{Path: path})
		c := newCSVCursor(src)
		got := []string{}
		for i := 0; i < 5; i++ {
			vars := map[string]string{}
			require.True(t, c.injectInto(vars))
			got = append(got, vars["id"])
		}
		assert.Equal(t, []string{"1", "2", "1", "2", "1"}, got)
	})

	t.Run("显式关闭回绕则用尽后停止", func(t *testing.T) {
		no := false
		src := loadSource(t, &config.CSVDataConfig{Path: path, Recycle: &no})
		c := newCSVCursor(src)
		require.True(t, c.injectInto(map[string]string{}))
		require.True(t, c.injectInto(map[string]string{}))
		assert.False(t, c.injectInto(map[string]string{}), "第三次应报告数据用尽")
	})
}

func TestCSVSharingModes(t *testing.T) {
	// 10 行数据，2 个并发各取 5 次
	content := "id\n"
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf("%d\n", i)
	}
	path := writeCSV(t, content)

	t.Run("all 模式全局不重复", func(t *testing.T) {
		no := false
		src := loadSource(t, &config.CSVDataConfig{
			Path: path, Sharing: config.CSVSharingAll, Recycle: &no,
		})

		var mu sync.Mutex
		seen := map[string]int{}
		var wg sync.WaitGroup
		for w := 0; w < 2; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := newCSVCursor(src)
				for i := 0; i < 5; i++ {
					vars := map[string]string{}
					if !c.injectInto(vars) {
						return
					}
					mu.Lock()
					seen[vars["id"]]++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		assert.Len(t, seen, 10, "共享游标下 10 行应被 2 个并发各取 5 次、互不重复")
		for id, n := range seen {
			assert.Equal(t, 1, n, "id=%s 被取了 %d 次", id, n)
		}
	})

	t.Run("worker 模式各自从头开始", func(t *testing.T) {
		src := loadSource(t, &config.CSVDataConfig{Path: path, Sharing: config.CSVSharingWorker})

		first, second := newCSVCursor(src), newCSVCursor(src)
		for i := 1; i <= 3; i++ {
			a, b := map[string]string{}, map[string]string{}
			require.True(t, first.injectInto(a))
			require.True(t, second.injectInto(b))
			assert.Equal(t, fmt.Sprint(i), a["id"])
			assert.Equal(t, a["id"], b["id"], "独立游标下两者应取到同一行")
		}
	})
}

// TestCSVCursorNilSafe 未配置数据源时不应干扰执行
func TestCSVCursorNilSafe(t *testing.T) {
	assert.Nil(t, NewCSVSource(nil))
	assert.Nil(t, newCSVCursor(nil))

	var c *csvCursor
	vars := map[string]string{}
	assert.True(t, c.injectInto(vars), "未配置数据源应返回 true 以继续执行")
	assert.Empty(t, vars)
}
