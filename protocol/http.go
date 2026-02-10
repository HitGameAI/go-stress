/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2025-12-30 00:00:00
 * @FilePath: \go-stress\protocol\http.go
 * @Description: 协议客户端实现 - HTTP
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package protocol

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/types"
	"github.com/kamalyes/go-toolbox/pkg/httpx"
)

// HTTPClient HTTP协议客户端
type HTTPClient struct {
	config *config.Config
	client *httpx.Client
}

// NewHTTPClient 创建HTTP客户端
func NewHTTPClient(cfg *config.Config) (*HTTPClient, error) {
	if cfg.HTTP == nil {
		cfg.HTTP = &config.HTTPConfig{
			KeepAlive:       true,
			MaxConnsPerHost: config.DefaultMaxConnsPerHost,
			FollowRedirects: true,
		}
	}

	return &HTTPClient{
		config: cfg,
		client: httpx.NewHttpClient(buildHTTPClient(cfg.HTTP)),
	}, nil
}

// buildHTTPClient 按配置构造底层 http.Client
//
// 此前只在 httpx 的两个预设客户端之间二选一，于是 HTTPConfig 的四个字段里
// 有三个（HTTP2 / FollowRedirects / MaxConnsPerHost）根本没人读，而 KeepAlive
// 也没起到它名字所说的作用：两个预设客户端都复用连接，差别只在空闲连接池大小。
// 实测 100 个请求在 keepalive=false 下仍只用了 15 条 TCP 连接——真关掉应是 100 条，
// 于是「测量建连开销」这个用途被静默破坏
func buildHTTPClient(cfg *config.HTTPConfig) *http.Client {
	maxConns := cfg.MaxConnsPerHost
	if maxConns <= 0 {
		maxConns = config.DefaultMaxConnsPerHost
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 60 * time.Second,
		}).DialContext,
		MaxConnsPerHost: maxConns,
		// 空闲连接池必须与并发上限同量级：标准库默认每 host 只留 2 条空闲连接，
		// 压测时绝大多数连接刚归还就被关掉，退化成频繁重建
		MaxIdleConnsPerHost:   maxConns,
		MaxIdleConns:          0, // 0 表示不限总数，由每 host 的上限约束
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableKeepAlives:     !cfg.KeepAlive,
		// 不设 TLSClientConfig：一旦设置，ForceAttemptHTTP2 会被标准库忽略，
		// HTTP/2 协商便无从发生
		ForceAttemptHTTP2: cfg.HTTP2,
	}

	client := &http.Client{Transport: transport}
	if !cfg.FollowRedirects {
		// 返回 ErrUseLastResponse 而非报错：压测关心的是 3xx 本身的耗时与状态码，
		// 把重定向响应原样交回即可
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	// 不设 client.Timeout：超时已由每个请求的 ctx 承载（见 Send），
	// 两套 deadline 并存只会让超时错误的来源难以分辨
	return client
}

// Connect HTTP无需显式连接
func (h *HTTPClient) Connect(ctx context.Context) error {
	return nil
}

// Send 发送HTTP请求
func (h *HTTPClient) Send(ctx context.Context, req *types.Request) (*types.Response, error) {
	startTime := time.Now()

	// 超时与取消都挂在 ctx 上，必须在构建请求之前派生
	if h.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.config.Timeout)
		defer cancel()
	}

	// 使用 go-toolbox 的 httpx 构建请求，并把本次请求的 ctx 挂上去
	//
	// 原先只调 client.NewRequest(method, url)，它取的是构造客户端时的 ctx，
	// 而 httpx.NewDefaultHttpClient 用的是 context.Background()：派生出的超时 ctx
	// 没有任何下游，于是 -timeout 形同虚设（http.DefaultClient 自身也没有 Timeout），
	// Ctrl-C 与内存阈值停止也无法中断已发出的请求，只能等服务端自己断开
	//
	// WithContext 返回的是克隆体而非原对象，返回值必须接住，否则这行等于没写
	httpReq := h.client.NewRequest(req.Method, req.URL).WithContext(ctx)

	// 设置Headers
	for k, v := range req.Headers {
		httpReq.SetHeader(k, v)
	}

	// 设置Body - 使用SetBodyRaw发送原始字节，避免二次JSON编码
	if req.Body != "" {
		httpReq.SetBodyRaw([]byte(req.Body))
	}

	// 执行请求
	httpResp, err := httpReq.Send()
	duration := time.Since(startTime)

	// 解析Query参数
	var queryString string
	if u, parseErr := url.Parse(req.URL); parseErr == nil {
		queryString = u.RawQuery
	}

	if err != nil {
		return &types.Response{
			Duration:       duration,
			Error:          fmt.Errorf("HTTP请求失败: %w", err),
			RequestURL:     req.URL,
			RequestMethod:  req.Method,
			RequestHeaders: req.Headers,
			RequestBody:    req.Body,
			RequestQuery:   queryString,
		}, err
	}

	// 读取响应体 - httpx.Response.Body() 返回 ([]byte, error)
	body, err := httpResp.Body()
	if err != nil {
		return &types.Response{
			StatusCode:     httpResp.StatusCode,
			Duration:       duration,
			Error:          fmt.Errorf("读取响应失败: %w", err),
			RequestURL:     req.URL,
			RequestMethod:  req.Method,
			RequestHeaders: req.Headers,
			RequestBody:    req.Body,
			RequestQuery:   queryString,
		}, err
	}

	// 构建响应
	headers := make(map[string]string)
	for k, v := range httpResp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	response := &types.Response{
		StatusCode:     httpResp.StatusCode,
		Headers:        headers,
		Body:           body,
		Duration:       duration,
		RequestURL:     req.URL,
		RequestMethod:  req.Method,
		RequestHeaders: req.Headers,
		RequestBody:    req.Body,
		RequestQuery:   queryString,
	}

	return response, nil
}

// Close 关闭HTTP客户端
func (h *HTTPClient) Close() error {
	// HTTP客户端无需显式关闭
	return nil
}

// Type 返回协议类型
func (h *HTTPClient) Type() types.ProtocolType {
	return types.ProtocolHTTP
}
