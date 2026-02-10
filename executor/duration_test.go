/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 19:05:12
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 19:20:38
 * @FilePath: \go-stress\executor\duration_test.go
 * @Description: 按时长压测测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunTaskByDuration 按时长压测必须真的跑满时长并正常收尾
//
// 回归用：config.Duration 此前只有校验与分布式协议读过，独立模式的执行器从不理它
//   - 配置里写 duration: 3s 会被完全忽略，跑的是 DefaultConfig 的「每并发 1 个请求」
//   - 补上实现后又因 Worker 返回 ctx.Err() 而以 DeadlineExceeded 收场，
//     一次正常的定时压测被判为 FATAL，报告也不生成
func TestRunTaskByDuration(t *testing.T) {
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	const runFor = 600 * time.Millisecond

	start := time.Now()
	result := RunTask(RunOptions{
		Logger:      logger.NewEmptyLogger(),
		StorageMode: StorageModeMemory,
		NoReport:    true,
		NoPrint:     true,
		NoWait:      true,
		ConfigFunc: func() *config.Config {
			cfg := config.DefaultConfig()
			cfg.URL = srv.URL
			cfg.Method = http.MethodGet
			cfg.Concurrency = 4
			cfg.Requests = 0
			cfg.Duration = runFor
			cfg.Timeout = 5 * time.Second
			cfg.Advanced.DisableRealtime = true
			return cfg
		},
	})
	elapsed := time.Since(start)

	require.NoError(t, result.Error, "按时长跑完是正常完成，不该作为错误返回")
	require.NotNil(t, result.Report, "正常完成必须给出报告")

	assert.GreaterOrEqual(t, elapsed, runFor, "应当真的跑满设定时长，实际 %s", elapsed)
	assert.Greater(t, result.Report.TotalRequests, uint64(1),
		"按时长压测不应退化成「每并发一个请求」，实际只发了 %d 个", result.Report.TotalRequests)
	// 服务端实收与报告数允许有不超过并发数的偏差，方向不定
	//
	// 时长截止点上两边都可能多算：ctx 到期时某个请求已被服务端处理并计数，
	// 而结果还没进采集器（实收偏多）；或该请求在客户端侧因 ctx 到期而失败，
	// 计入了报告的失败数却未必到过服务端（报告偏多）
	// 每个并发最多各有一个在途请求，故偏差上界是并发数 ——
	// 断言「两者相等」或任一方向的大小关系都会偶发失败
	recorded, servedN := int64(result.Report.TotalRequests), served.Load()
	diff := recorded - servedN
	if diff < 0 {
		diff = -diff
	}
	assert.LessOrEqual(t, diff, int64(4),
		"实收与报告的偏差不应超过并发数（实收 %d，报告 %d）", servedN, recorded)
}

// TestRunTaskDurationWithQPS 时长与定速可以共存
func TestRunTaskDurationWithQPS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	const (
		runFor = time.Second
		qps    = 120.0
	)

	result := RunTask(RunOptions{
		Logger:      logger.NewEmptyLogger(),
		StorageMode: StorageModeMemory,
		NoReport:    true,
		NoPrint:     true,
		NoWait:      true,
		ConfigFunc: func() *config.Config {
			cfg := config.DefaultConfig()
			cfg.URL = srv.URL
			cfg.Method = http.MethodGet
			cfg.Concurrency = 8
			cfg.Requests = 0
			cfg.Duration = runFor
			cfg.Timeout = 5 * time.Second
			cfg.Advanced.DisableRealtime = true
			cfg.Advanced.TargetQPS = qps
			return cfg
		},
	})

	require.NoError(t, result.Error)
	require.NotNil(t, result.Report)

	// 这里只断言「限速把速率压下来了」，不断言下界
	//
	// 速率精度由 TestRateLimiterPacesAcrossWorkers 以纯单测覆盖（无 HTTP、
	// 无其他包争用），那里才是验证精度的地方
	//
	// 此处要跑真实 HTTP，与整套测试并行时机器被占满，完成数会掉到任何预设
	// 下界以下 —— 那是调度现象而非缺陷，为它设下界只会得到一个时不时变红的
	// 测试，而那比没有测试更糟：它训练人忽略红色
	//
	// 上界方向则是稳健的：负载只会让完成数更少，不会让限速失效而变多。
	// 不限速时 8 个并发打本地 httptest 一秒可达数万请求，落在目标速率的
	// 数倍以内即可证明限速生效
	want := qps * runFor.Seconds()
	got := float64(result.Report.TotalRequests)
	assert.Positive(t, got, "限速不应把发压完全卡死")
	assert.Less(t, got, want*4,
		"请求数远超目标速率，限速未生效：目标约 %.0f 个，实际 %d", want, result.Report.TotalRequests)
}

// TestValidateConfigAllowsDurationWithoutRequests 校验必须放行按时长压测
func TestValidateConfigAllowsDurationWithoutRequests(t *testing.T) {
	base := func() *config.Config {
		cfg := config.DefaultConfig()
		cfg.URL = "http://127.0.0.1:1/x"
		cfg.Concurrency = 1
		return cfg
	}

	t.Run("请求数与时长都为零时报错", func(t *testing.T) {
		cfg := base()
		cfg.Requests, cfg.Duration = 0, 0
		assert.Error(t, validateConfig(cfg))
	})

	t.Run("只给时长应放行", func(t *testing.T) {
		cfg := base()
		cfg.Requests, cfg.Duration = 0, 10*time.Second
		assert.NoError(t, validateConfig(cfg), "按时长压测时请求数本就为 0")
	})

	t.Run("只给请求数应放行", func(t *testing.T) {
		cfg := base()
		cfg.Requests, cfg.Duration = 10, 0
		assert.NoError(t, validateConfig(cfg))
	})
}

// TestWorkerByDurationLoopsUntilContextDone reqCount 为 0 时由 ctx 决定何时停止
func TestWorkerByDurationLoopsUntilContextDone(t *testing.T) {
	var calls atomic.Int64
	w := &Worker{
		id:       0,
		reqCount: 0, // 0 表示按时长跑
		client:   stubClient{},
		handler: func(ctx context.Context, req *Request) (*Response, error) {
			calls.Add(1)
			return &Response{StatusCode: 200}, nil
		},
		controller:     &NoOpController{},
		depContext:     NewWorkerDependencyContext(),
		logger:         logger.NewEmptyLogger(),
		think:          newThinkTimer(),
		trace:          newTraceGen(),
		extractorCache: map[string]*ExtractorManager{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := w.Run(ctx)
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, context.DeadlineExceeded, "到点应返回 ctx 的错误，由上层判定是否正常")
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond, "应持续到 ctx 结束")
	assert.Zero(t, calls.Load(), "缺少 APISelector 时不应真的发请求")
}

// stubClient 满足 Client 接口的空实现
type stubClient struct{}

func (stubClient) Connect(ctx context.Context) error { return nil }
func (stubClient) Send(ctx context.Context, req *Request) (*Response, error) {
	return &Response{StatusCode: 200}, nil
}
func (stubClient) Close() error       { return nil }
func (stubClient) Type() ProtocolType { return ProtocolHTTP }
