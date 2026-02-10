/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 16:59:17
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:16:35
 * @FilePath: \go-stress\executor\csvsource.go
 * @Description: CSV 数据源的取数游标
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"sync/atomic"

	"github.com/kamalyes/go-stress/config"
)

// CSVSource CSV 数据源的共享部分，由全部 Worker 共用
//
// 数据在启动时一次性读入（见 config.LoadCSVData），此处只负责发号：
// 热路径上仅一次原子自增加一次下标访问，不做文件 IO、解析或分配
type CSVSource struct {
	data   *config.CSVData
	cursor atomic.Uint64 // 仅 sharing=all 时使用
}

// NewCSVSource 基于已载入的数据创建共享源
func NewCSVSource(data *config.CSVData) *CSVSource {
	if data == nil || len(data.Rows) == 0 {
		return nil
	}
	return &CSVSource{data: data}
}

// Variables 返回变量名列表
func (s *CSVSource) Variables() []string {
	return s.data.Variables
}

// RowCount 返回数据行数
func (s *CSVSource) RowCount() int {
	return len(s.data.Rows)
}

// Sharing 返回游标共享方式
func (s *CSVSource) Sharing() config.CSVSharing {
	return s.data.Sharing
}

// Recycle 返回数据用尽后是否回绕
func (s *CSVSource) Recycle() bool {
	return s.data.Recycle
}

// nextShared 取共享游标的下一行，返回 nil 表示数据已用尽且不回绕
func (s *CSVSource) nextShared() []string {
	// Add 返回自增后的值，减一得到本次应取的下标
	n := s.cursor.Add(1) - 1
	return s.rowAt(n)
}

// rowAt 按序号取行，按 Recycle 决定越界行为
func (s *CSVSource) rowAt(n uint64) []string {
	total := uint64(len(s.data.Rows))
	if n >= total {
		if !s.Recycle() {
			return nil
		}
		n %= total
	}
	return s.data.Rows[n]
}

// csvCursor 单个 Worker 持有的取数句柄
//
// sharing=all 时转发到共享游标；sharing=worker 时在本地自增，
// 两种模式都不额外加锁
type csvCursor struct {
	source *CSVSource
	local  uint64
}

// newCSVCursor 创建 Worker 本地游标，source 为 nil 时返回 nil
func newCSVCursor(source *CSVSource) *csvCursor {
	if source == nil {
		return nil
	}
	return &csvCursor{source: source}
}

// next 取下一行数据，返回 nil 表示数据已用尽且不回绕
func (c *csvCursor) next() []string {
	if c == nil {
		return nil
	}
	if c.source.Sharing() == config.CSVSharingWorker {
		n := c.local
		c.local++
		return c.source.rowAt(n)
	}
	return c.source.nextShared()
}

// injectInto 取下一行并以「变量名 -> 值」写入 vars
//
// 复用请求序列级的变量表而非新建 map：该表每轮请求已就地清空，
// 且 CSV 列名不含点号，与提取变量的 "apiName.varName" 键天然不冲突
// 返回 false 表示数据已用尽且不回绕，调用方应据此停止发压
func (c *csvCursor) injectInto(vars map[string]string) bool {
	if c == nil {
		return true // 未配置数据源，不影响执行
	}
	row := c.next()
	if row == nil {
		return false
	}
	names := c.source.Variables()
	for i, name := range names {
		if i >= len(row) {
			break
		}
		vars[name] = row[i]
	}
	return true
}
