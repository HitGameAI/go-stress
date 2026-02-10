/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:30:15
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:52:08
 * @FilePath: \go-stress\protocol\http_test.go
 * @Description: HTTP 客户端构造测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package protocol

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildHTTPClientHonorsConfig HTTPConfig 的每个字段都必须真的作用到传输层
//
// 回归用：此前 NewHTTPClient 只在 httpx 的两个预设客户端之间二选一，
// HTTP2 / FollowRedirects / MaxConnsPerHost 三个字段根本没人读
func TestBuildHTTPClientHonorsConfig(t *testing.T) {
	t.Run("MaxConnsPerHost 同时约束连接上限与空闲池", func(t *testing.T) {
		c := buildHTTPClient(&config.HTTPConfig{KeepAlive: true, MaxConnsPerHost: 256})
		tr := c.Transport.(*http.Transport)
		assert.Equal(t, 256, tr.MaxConnsPerHost)
		// 空闲池若停留在标准库默认的 2，连接刚归还就被关掉，长连接等于白设
		assert.Equal(t, 256, tr.MaxIdleConnsPerHost)
	})

	t.Run("未配置或非法值回落到默认", func(t *testing.T) {
		for _, v := range []int{0, -1} {
			tr := buildHTTPClient(&config.HTTPConfig{MaxConnsPerHost: v}).Transport.(*http.Transport)
			assert.Equal(t, config.DefaultMaxConnsPerHost, tr.MaxConnsPerHost, "输入 %d", v)
		}
	})

	t.Run("KeepAlive 映射到 DisableKeepAlives", func(t *testing.T) {
		on := buildHTTPClient(&config.HTTPConfig{KeepAlive: true}).Transport.(*http.Transport)
		assert.False(t, on.DisableKeepAlives)
		off := buildHTTPClient(&config.HTTPConfig{KeepAlive: false}).Transport.(*http.Transport)
		assert.True(t, off.DisableKeepAlives)
	})

	t.Run("HTTP2 映射到 ForceAttemptHTTP2 且不设 TLSClientConfig", func(t *testing.T) {
		tr := buildHTTPClient(&config.HTTPConfig{HTTP2: true}).Transport.(*http.Transport)
		assert.True(t, tr.ForceAttemptHTTP2)
		// 一旦设了 TLSClientConfig，标准库会忽略 ForceAttemptHTTP2
		assert.Nil(t, tr.TLSClientConfig, "设置 TLSClientConfig 会使 HTTP/2 协商失效")
	})

	t.Run("FollowRedirects 控制是否跟随", func(t *testing.T) {
		assert.Nil(t, buildHTTPClient(&config.HTTPConfig{FollowRedirects: true}).CheckRedirect)
		assert.NotNil(t, buildHTTPClient(&config.HTTPConfig{FollowRedirects: false}).CheckRedirect)
	})

	t.Run("不设 client.Timeout，超时交由每请求的 ctx 承载", func(t *testing.T) {
		assert.Zero(t, buildHTTPClient(&config.HTTPConfig{}).Timeout,
			"两套 deadline 并存会让超时错误难以分辨来源")
	})
}

// TestHTTPClientKeepAliveRealConnections 关掉长连接后应当每请求一条新连接
func TestHTTPClientKeepAliveRealConnections(t *testing.T) {
	const requests = 12

	run := func(keepAlive bool) int64 {
		var conns atomic.Int64
		srv := httptest.NewUnstartedServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"ok":true}`)
			}))
		srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				conns.Add(1)
			}
		}
		srv.Start()
		defer srv.Close()

		cfg := &config.Config{
			Timeout: 5 * time.Second,
			HTTP:    &config.HTTPConfig{KeepAlive: keepAlive, MaxConnsPerHost: 8, FollowRedirects: true},
		}
		client, err := NewHTTPClient(cfg)
		require.NoError(t, err)

		for i := 0; i < requests; i++ {
			resp, err := client.Send(context.Background(), &types.Request{
				URL: srv.URL + "/p", Method: http.MethodGet,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}
		return conns.Load()
	}

	reused := run(true)
	fresh := run(false)

	assert.Less(t, reused, int64(requests), "开长连接时连接数应远少于请求数，实际 %d", reused)
	assert.Equal(t, int64(requests), fresh,
		"关长连接后应当每请求一条新连接，实际只用了 %d 条——说明并未真正关闭", fresh)
}

// TestHTTPClientFollowRedirects 关掉跟随后应原样拿到 3xx
func TestHTTPClientFollowRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/from", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/to", http.StatusFound)
	})
	mux.HandleFunc("/to", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "arrived")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	send := func(follow bool) *types.Response {
		cfg := &config.Config{
			Timeout: 5 * time.Second,
			HTTP:    &config.HTTPConfig{KeepAlive: true, FollowRedirects: follow},
		}
		client, err := NewHTTPClient(cfg)
		require.NoError(t, err)
		resp, err := client.Send(context.Background(), &types.Request{
			URL: srv.URL + "/from", Method: http.MethodGet,
		})
		require.NoError(t, err)
		return resp
	}

	followed := send(true)
	assert.Equal(t, http.StatusOK, followed.StatusCode)
	assert.Equal(t, "arrived", string(followed.Body))

	kept := send(false)
	assert.Equal(t, http.StatusFound, kept.StatusCode,
		"关掉跟随时应原样返回 3xx，压测关心的正是这一跳的状态码与耗时")
}

// TestHTTPClientRespectsContextTimeout 超时必须真的作用到请求上
//
// 回归用：此前派生的超时 ctx 没有传给请求，-timeout 形同虚设，
// 慢接口会一直等到服务端自己返回
func TestHTTPClientRespectsContextTimeout(t *testing.T) {
	// 睡眠时长只需明显大于超时：httptest 的 Close 会等 handler 返回，
	// 睡太久整个测试套件要陪着等
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	cfg := &config.Config{
		Timeout: 100 * time.Millisecond,
		HTTP:    &config.HTTPConfig{KeepAlive: true, FollowRedirects: true},
	}
	client, err := NewHTTPClient(cfg)
	require.NoError(t, err)

	start := time.Now()
	_, err = client.Send(context.Background(), &types.Request{URL: srv.URL, Method: http.MethodGet})
	elapsed := time.Since(start)

	require.Error(t, err, "超过 Timeout 应当报错而非静候服务端")
	assert.Less(t, elapsed, 400*time.Millisecond, "应在约 100ms 处失败，实际耗时 %s", elapsed)
}
