/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 16:22:35
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 16:50:51
 * @FilePath: \go-stress\executor\trace_test.go
 * @Description: trace 标识生成测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// w3cTraceparent W3C trace-context 规定的 traceparent 形状
// version(2) "-" trace-id(32) "-" parent-id(16) "-" flags(2)，全部小写十六进制
var w3cTraceparent = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

func TestTraceGenFormat(t *testing.T) {
	g := newTraceGen()

	for i := 0; i < 1000; i++ {
		traceID, traceparent := g.next()

		require.True(t, w3cTraceparent.MatchString(traceparent),
			"traceparent 不符合 W3C 形状: %q", traceparent)

		// trace-id 必须是 traceparent 中的那一段，报告里的值才能直接拿去搜服务端日志
		assert.Equal(t, traceparent[3:35], traceID)

		// W3C 要求 trace-id 与 parent-id 都不得全零
		assert.NotEqual(t, strings.Repeat("0", 32), traceID, "trace-id 不得全零")
		assert.NotEqual(t, strings.Repeat("0", 16), traceparent[36:52], "parent-id 不得全零")
	}
}

func TestTraceGenUnique(t *testing.T) {
	t.Run("同一生成器内不重复", func(t *testing.T) {
		g := newTraceGen()
		seen := make(map[string]struct{}, 10000)
		for i := 0; i < 10000; i++ {
			id, _ := g.next()
			_, dup := seen[id]
			require.False(t, dup, "第 %d 次生成出现重复 trace-id: %s", i, id)
			seen[id] = struct{}{}
		}
	})

	t.Run("不同生成器之间不重复", func(t *testing.T) {
		// 每个 Worker 各持一个生成器，前缀随机，跨 Worker 不应撞
		const gens, each = 50, 200
		seen := make(map[string]struct{}, gens*each)
		for i := 0; i < gens; i++ {
			g := newTraceGen()
			for j := 0; j < each; j++ {
				id, _ := g.next()
				_, dup := seen[id]
				require.False(t, dup, "跨生成器出现重复 trace-id: %s", id)
				seen[id] = struct{}{}
			}
		}
	})
}

// TestPutHex64 手写的十六进制写入必须与标准库一致
func TestPutHex64(t *testing.T) {
	cases := []uint64{0, 1, 0xf, 0x10, 0x9e3779b97f4a7c15, ^uint64(0)}
	for _, v := range cases {
		var buf [16]byte
		putHex64(buf[:], v)
		// 基准必须来自标准库：若对照实现与被测实现同算法，这个用例只是自证
		assert.Equal(t, fmt.Sprintf("%016x", v), string(buf[:]), "v=%#x", v)
	}
}
