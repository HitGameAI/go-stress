/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-25 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-25 16:00:00
 * @FilePath: \go-stress\protocol\websocket.go
 * @Description: WebSocket 协议客户端实现
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package protocol

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kamalyes/go-stress/config"
)

// defaultWebSocketTimeout 未配置超时时的兜底读写上限
const defaultWebSocketTimeout = 30 * time.Second

// WebSocketClient WebSocket 客户端
type WebSocketClient struct {
	config  *config.Config
	conn    *websocket.Conn
	dialer  *websocket.Dialer
	headers http.Header
	mu      sync.Mutex // 保护并发读写
}

// NewWebSocketClient 创建 WebSocket 客户端
func NewWebSocketClient(cfg *config.Config) (Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// 验证 URL
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket URL: %w", err)
	}

	// WebSocket URL 必须是 ws:// 或 wss://
	if u.Scheme != "ws" && u.Scheme != "wss" {
		// 自动转换 http/https 为 ws/wss
		switch u.Scheme {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		default:
			return nil, fmt.Errorf("invalid websocket scheme: %s (expected ws or wss)", u.Scheme)
		}
		cfg.URL = u.String()
	}

	// 构建请求头
	headers := make(http.Header)
	for k, v := range cfg.Headers {
		headers.Set(k, v)
	}

	// cfg.Timeout 本身已是 time.Duration（纳秒），不能再乘时间单位
	//
	// 原先写的是 time.Duration(cfg.Timeout) * time.Second，等于把纳秒数又乘了 1e9：
	//   -timeout 2s  → 握手超时 63 年（等于没有超时）
	//   -timeout 30s → 溢出 int64 变成负数，握手截止时间落在过去，
	//                  于是拨号立即失败。而 30s 正是默认值，
	//                  也就是说默认配置下 WebSocket 压测根本连不上
	handshake := cfg.Timeout
	if handshake <= 0 {
		handshake = defaultWebSocketTimeout
	}
	dialer := &websocket.Dialer{
		HandshakeTimeout: handshake,
		Proxy:            http.ProxyFromEnvironment,
	}

	client := &WebSocketClient{
		config:  cfg,
		dialer:  dialer,
		headers: headers,
	}

	return client, nil
}

// Connect 建立 WebSocket 连接
func (c *WebSocketClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil // 已连接
	}

	conn, httpResp, err := c.dialer.DialContext(ctx, c.config.URL, c.headers)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}
	c.conn = conn

	if httpResp != nil {
		httpResp.Body.Close()
	}

	return nil
}

// Send 发送 WebSocket 请求
func (c *WebSocketClient) Send(ctx context.Context, req *Request) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	startTime := time.Now()

	// 确保连接已建立
	if c.conn == nil {
		conn, httpResp, err := c.dialer.DialContext(ctx, c.config.URL, c.headers)
		if err != nil {
			return nil, fmt.Errorf("websocket dial failed: %w", err)
		}
		c.conn = conn
		if httpResp != nil {
			httpResp.Body.Close()
		}
	}

	// 发送消息
	messageType := websocket.TextMessage
	if req.Metadata != nil {
		if mt, ok := req.Metadata["message_type"].(int); ok {
			messageType = mt
		}
	}

	// 读写都必须设截止时间
	//
	// gorilla/websocket 的 WriteMessage / ReadMessage 不接受 ctx，只认连接上的
	// deadline。不设的话，服务端接受了连接却不回复时 ReadMessage 会永久阻塞——
	// 而整个 Send 全程持有 c.mu，那个 Worker 就彻底卡死，-timeout 对 WebSocket
	// 形同虚设（与此前 HTTP 那条路径同类的问题）
	//
	// 注意这是「上界」而非「即时取消」：ctx 被取消时已发出的读仍会等到截止时间。
	// 要做到即时取消得在另一个 goroutine 里关掉连接，而那意味着每个请求多一个
	// goroutine，对热路径不划算
	deadline := c.deadline(ctx)
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		c.dropConn()
		return nil, fmt.Errorf("websocket set write deadline failed: %w", err)
	}
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		c.dropConn()
		return nil, fmt.Errorf("websocket set read deadline failed: %w", err)
	}

	if err := c.conn.WriteMessage(messageType, []byte(req.Body)); err != nil {
		// 连接断开,关闭并返回错误
		c.dropConn()
		return nil, fmt.Errorf("websocket write failed: %w", err)
	}

	// 接收响应
	_, respBody, err := c.conn.ReadMessage()
	if err != nil {
		// 只有优雅关闭才算正常：1000 NormalClosure、1001 GoingAway 都是对端
		// 按协议发了关闭帧。而 1006 AbnormalClosure 表示连接没有关闭帧就断了
		// （对端崩溃、中间设备掐断），那是故障而非正常收尾
		//
		// 此前把 1006 一并算作成功并返回 200，于是服务端压测中途崩掉时
		// 报告仍显示 100% 成功——这类失真比报错危险得多
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			c.dropConn()

			duration := time.Since(startTime)
			headers := make(map[string]string)
			for k, v := range c.headers {
				if len(v) > 0 {
					headers[k] = v[0]
				}
			}

			// 返回成功响应，表示连接已正常处理
			return &Response{
				StatusCode:     200,
				Body:           []byte{},
				Duration:       duration,
				RequestURL:     c.config.URL,
				RequestMethod:  "WEBSOCKET",
				RequestHeaders: headers,
				RequestBody:    req.Body,
			}, nil
		}

		// 其他错误（含 1006 异常关闭、读超时）一律视为失败
		c.dropConn()
		return nil, fmt.Errorf("websocket read failed: %w", err)
	}

	duration := time.Since(startTime)

	// 转换 http.Header 为 map[string]string
	headers := make(map[string]string)
	for k, v := range c.headers {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	// 构造响应
	response := &Response{
		StatusCode:     200, // WebSocket 成功时状态码固定为 200
		Body:           respBody,
		Duration:       duration,
		RequestURL:     c.config.URL,
		RequestMethod:  "WEBSOCKET",
		RequestHeaders: headers,
		RequestBody:    req.Body,
	}

	return response, nil
}

// deadline 计算本次读写的截止时间
//
// 取 ctx 的截止时间与配置超时中较早的那个；两者都没有时退回一个保守的兜底值，
// 以保证任何情况下都不会无限等待
func (c *WebSocketClient) deadline(ctx context.Context) time.Time {
	timeout := c.config.Timeout
	if timeout <= 0 {
		timeout = defaultWebSocketTimeout
	}
	d := time.Now().Add(timeout)

	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(d) {
		return ctxDeadline
	}
	return d
}

// dropConn 关闭并丢弃当前连接，下次 Send 会重新拨号
// 调用方必须已持有 c.mu
func (c *WebSocketClient) dropConn() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Type 返回协议类型
func (c *WebSocketClient) Type() ProtocolType {
	return ProtocolWebSocket
}

// Close 关闭客户端连接
func (c *WebSocketClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}
