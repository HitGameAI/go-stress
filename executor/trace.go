/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 16:20:08
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 16:50:12
 * @FilePath: \go-stress\executor\trace.go
 * @Description: 请求级 trace 标识生成（W3C trace-context）
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	cryptorand "crypto/rand"
	"encoding/binary"
)

// HeaderTraceparent W3C trace-context 的传播头
//
// 用标准头而非自造的 X-Trace-Id：被压服务只要接了任意 OTel SDK 或主流框架的
// 链路中间件，就会自动把它作为父 span 接上，服务端那条链路与此处的压测结果
// 共享同一个 trace-id，无需对方改一行代码
const HeaderTraceparent = "traceparent"

// traceparentLen "00-" + 32 位 trace-id + "-" + 16 位 span-id + "-01"
const traceparentLen = 3 + 32 + 1 + 16 + 3

// traceGen 为单个 Worker 生成 W3C trace-context 标识
//
// 每个 Worker 独占一个实例，故全程无锁、无原子操作：trace-id 取「Worker 创建时
// 一次性随机的 8 字节前缀 + 8 字节自增序号」，前缀保证跨 Worker 与跨次压测不撞，
// 序号保证同一 Worker 内唯一
//
// 不逐请求调 crypto/rand：这里的标识只用于把压测结果与被压服务的日志对上，
// 不承担任何安全语义，而压测工具自身绝不能成为被测系统的瓶颈
type traceGen struct {
	prefix uint64
	seq    uint64
}

// newTraceGen 创建生成器，随机前缀在此一次性取得
// crypto/rand 失败时回落到一个非零常量混入指针无关的序号，仍能保证格式合法
func newTraceGen() *traceGen {
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return &traceGen{prefix: 0x9e3779b97f4a7c15}
	}
	prefix := binary.BigEndian.Uint64(b[:])
	if prefix == 0 {
		prefix = 0x9e3779b97f4a7c15 // trace-id 不得全零
	}
	return &traceGen{prefix: prefix}
}

// next 返回本次请求的 trace-id（32 位十六进制）与完整的 traceparent 头值
//
// 两者共用一次缓冲区填充，但各自转成 string 时仍需各一次分配：
// Go 字符串不可变，无法让 trace-id 复用 traceparent 的底层数组
func (g *traceGen) next() (traceID, traceparent string) {
	g.seq++

	var buf [traceparentLen]byte
	buf[0], buf[1], buf[2] = '0', '0', '-'
	putHex64(buf[3:19], g.prefix)
	putHex64(buf[19:35], g.seq)
	buf[35] = '-'
	// span-id 取序号的 splitmix64 终结函数，避免与 trace-id 尾部逐位相同
	putHex64(buf[36:52], spanIDFor(g.prefix, g.seq))
	buf[52], buf[53], buf[54] = '-', '0', '1' // flags=01 表示已采样

	return string(buf[3:35]), string(buf[:])
}

// spanIDFor 由前缀与序号导出 span-id，保证非零（W3C 要求 parent-id 不得全零）
func spanIDFor(prefix, seq uint64) uint64 {
	x := prefix ^ (seq * 0x9e3779b97f4a7c15)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	if x == 0 {
		return 1
	}
	return x
}

const hexDigits = "0123456789abcdef"

// putHex64 把 v 以 16 位小写十六进制写入 dst（dst 长度必须为 16）
// 手写而非 strconv/hex：避免为每个请求产生中间切片与字符串
func putHex64(dst []byte, v uint64) {
	for i := 15; i >= 0; i-- {
		dst[i] = hexDigits[v&0xf]
		v >>= 4
	}
}
