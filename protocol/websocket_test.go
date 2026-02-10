/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 18:25:09
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 18:36:52
 * @FilePath: \go-stress\protocol\websocket_test.go
 * @Description: WebSocket 客户端测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsServer 起一个 WebSocket 测试服务，handler 决定收到消息后如何应对
func wsServer(t *testing.T, handler func(*websocket.Conn)) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func wsClient(t *testing.T, url string, timeout time.Duration) Client {
	t.Helper()
	c, err := NewWebSocketClient(&config.Config{
		URL:       url,
		Timeout:   timeout,
		WebSocket: &config.WebSocketConfig{},
	})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

// TestWebSocketEcho 正常回声往返
func TestWebSocketEcho(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn) {
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if conn.WriteMessage(mt, msg) != nil {
				return
			}
		}
	})

	c := wsClient(t, url, 3*time.Second)
	require.NoError(t, c.Connect(context.Background()))

	resp, err := c.Send(context.Background(), &types.Request{Body: "ping"})
	require.NoError(t, err)
	assert.Equal(t, "ping", string(resp.Body))
	assert.Equal(t, 200, resp.StatusCode)
}

// TestWebSocketReadDeadline 服务端不回复时必须按超时失败，而不是永久阻塞
//
// 回归用：gorilla 的 ReadMessage 不认 ctx，只认连接 deadline。此前两者都没设，
// 服务端接受连接却不回复时 Send 会一直挂着，而它全程持有 c.mu，
// 那个 Worker 就彻底卡死，-timeout 对 WebSocket 完全无效
func TestWebSocketReadDeadline(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn) {
		// 收下消息但从不回复，并保持连接不关
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		time.Sleep(3 * time.Second)
	})

	c := wsClient(t, url, 200*time.Millisecond)
	require.NoError(t, c.Connect(context.Background()))

	start := time.Now()
	_, err := c.Send(context.Background(), &types.Request{Body: "hello"})
	elapsed := time.Since(start)

	require.Error(t, err, "服务端不回复时应按超时失败")
	assert.Less(t, elapsed, 1500*time.Millisecond,
		"应在约 200ms 处超时，实际耗时 %s（说明 deadline 未生效）", elapsed)
}

// TestWebSocketCtxDeadlineTakesPrecedence ctx 的截止时间更早时应以它为准
func TestWebSocketCtxDeadlineTakesPrecedence(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		time.Sleep(3 * time.Second)
	})

	// 配置超时很长，但 ctx 只给 150ms
	c := wsClient(t, url, 30*time.Second)
	require.NoError(t, c.Connect(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Send(ctx, &types.Request{Body: "hello"})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, time.Second,
		"ctx 截止时间更早时应以它为准，实际耗时 %s", elapsed)
}

// TestWebSocketAbnormalCloseIsFailure 异常关闭（1006）必须算失败
//
// 回归用：此前把 CloseAbnormalClosure 与优雅关闭一并当作成功并返回 200，
// 于是服务端压测中途崩掉时报告仍显示 100% 成功
func TestWebSocketAbnormalCloseIsFailure(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		// 不发关闭帧直接断开，对端会看到 1006
		conn.UnderlyingConn().Close()
	})

	c := wsClient(t, url, 2*time.Second)
	require.NoError(t, c.Connect(context.Background()))

	_, err := c.Send(context.Background(), &types.Request{Body: "hello"})
	assert.Error(t, err, "没有关闭帧的断连是故障，不该被计为成功")
}

// TestWebSocketGracefulCloseIsSuccess 优雅关闭仍按成功处理
func TestWebSocketGracefulCloseIsSuccess(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		// 按协议发送关闭帧
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
		time.Sleep(50 * time.Millisecond)
	})

	c := wsClient(t, url, 2*time.Second)
	require.NoError(t, c.Connect(context.Background()))

	resp, err := c.Send(context.Background(), &types.Request{Body: "hello"})
	require.NoError(t, err, "对端优雅关闭属于长连接测试的正常收尾")
	assert.Equal(t, 200, resp.StatusCode)
}

// TestWebSocketHandshakeTimeoutUnit 握手超时必须等于配置的超时本身
//
// 回归用：原先写成 time.Duration(cfg.Timeout) * time.Second，把纳秒数又乘了 1e9：
//
//	-timeout 2s  → 63 年（等于没有超时）
//	-timeout 30s → 溢出成负数，握手截止时间落在过去，拨号立即失败
//
// 而 30s 是 -timeout 的默认值，也就是说默认配置下 WebSocket 压测根本连不上
func TestWebSocketHandshakeTimeoutUnit(t *testing.T) {
	for _, timeout := range []time.Duration{
		200 * time.Millisecond,
		2 * time.Second,
		30 * time.Second, // 默认值，溢出的那个
		5 * time.Minute,
	} {
		c, err := NewWebSocketClient(&config.Config{
			URL:       "ws://127.0.0.1:1/x",
			Timeout:   timeout,
			WebSocket: &config.WebSocketConfig{},
		})
		require.NoError(t, err)

		ws, ok := c.(*WebSocketClient)
		require.True(t, ok)
		assert.Equal(t, timeout, ws.dialer.HandshakeTimeout,
			"握手超时应与配置一致，不得再乘时间单位（timeout=%s）", timeout)
		assert.Positive(t, ws.dialer.HandshakeTimeout,
			"握手超时为非正数时 gorilla 会把截止时间算在过去，拨号必然立即失败")
	}

	t.Run("未配置超时时取兜底值", func(t *testing.T) {
		c, err := NewWebSocketClient(&config.Config{
			URL:       "ws://127.0.0.1:1/x",
			WebSocket: &config.WebSocketConfig{},
		})
		require.NoError(t, err)
		ws := c.(*WebSocketClient)
		assert.Equal(t, defaultWebSocketTimeout, ws.dialer.HandshakeTimeout)
	})
}

// TestWebSocketDefaultTimeoutCanConnect 默认超时下必须能正常连上
func TestWebSocketDefaultTimeoutCanConnect(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn) {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		conn.WriteMessage(mt, msg)
	})

	// 30s 正是 -timeout 的默认值
	c := wsClient(t, url, 30*time.Second)
	require.NoError(t, c.Connect(context.Background()),
		"默认超时下应能建立连接")

	resp, err := c.Send(context.Background(), &types.Request{Body: "x"})
	require.NoError(t, err)
	assert.Equal(t, "x", string(resp.Body))
}
