/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 20:06:31
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 20:18:05
 * @FilePath: \go-stress\executor\verify_result_test.go
 * @Description: 验证结果计入报告的测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package executor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-stress/config"
	"github.com/kamalyes/go-stress/statistics"
	"github.com/kamalyes/go-stress/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okServer 返回 200 与固定 JSON，使传输层始终成功，便于隔离验证逻辑
func okServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":0,"msg":"ok"}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runWithAPIs 以给定的 API 列表跑一轮，返回报告
func runWithAPIs(t *testing.T, apis []config.APIConfig, requests uint64) *statisticsReport {
	t.Helper()
	result := RunTask(RunOptions{
		Logger:      logger.NewEmptyLogger(),
		StorageMode: StorageModeMemory,
		NoReport:    true,
		NoPrint:     true,
		NoWait:      true,
		ConfigFunc: func() *config.Config {
			cfg := config.DefaultConfig()
			cfg.Concurrency = 1
			cfg.Requests = requests
			cfg.Timeout = 5 * time.Second
			cfg.Advanced.DisableRealtime = true
			cfg.APIs = apis
			return cfg
		},
	})
	require.NotNil(t, result.Report, "报告不应为空")
	return &statisticsReport{result: result}
}

// statisticsReport 包装运行结果，便于断言
type statisticsReport struct {
	result *RunResult
}

// TestPerAPIVerifyFailureCountsAsFailure 单个 API 的验证失败必须计入失败数
//
// 回归用：验证此前有两条执行路径 —— 中间件用顶层 config.Verify，Worker 用
// apis[].verify。只有中间件那条会把失败转成错误，于是 Worker 这条路径上
// 「HTTP 200 但断言不通过」被记为成功：成功率 100%，而断言全红
func TestPerAPIVerifyFailureCountsAsFailure(t *testing.T) {
	url := okServer(t)

	t.Run("验证不通过应判失败", func(t *testing.T) {
		rep := runWithAPIs(t, []config.APIConfig{{
			Name:   "must_fail",
			URL:    url,
			Method: http.MethodGet,
			Verify: []config.VerifyConfig{{
				Type:     types.VerifyTypeContains,
				Operator: types.OpContains,
				Expect:   "THIS_NEVER_APPEARS",
			}},
		}}, 3)

		r := rep.result.Report
		assert.Equal(t, uint64(3), r.TotalRequests)
		assert.Zero(t, r.SuccessRequests, "断言不通过的请求不该计为成功")
		assert.Equal(t, uint64(3), r.FailedRequests)
		assert.Zero(t, r.SuccessRate)
	})

	t.Run("验证通过应判成功", func(t *testing.T) {
		rep := runWithAPIs(t, []config.APIConfig{{
			Name:   "must_pass",
			URL:    url,
			Method: http.MethodGet,
			Verify: []config.VerifyConfig{{
				Type:     types.VerifyTypeContains,
				Operator: types.OpContains,
				Expect:   `"code":0`,
			}},
		}}, 3)

		r := rep.result.Report
		assert.Equal(t, uint64(3), r.SuccessRequests)
		assert.Zero(t, r.FailedRequests)
	})
}

// TestContinueOnFailureStillCountsAsFailure continue_on_failure 不改变成功率
//
// 该字段的语义是「验证失败时不阻断后续 API」，与这条请求算不算失败无关。
// 此前二者被同一个 bool 搅在一起：设了 continue_on_failure 就连失败都不计了
func TestContinueOnFailureStillCountsAsFailure(t *testing.T) {
	rep := runWithAPIs(t, []config.APIConfig{{
		Name:   "soft_fail",
		URL:    okServer(t),
		Method: http.MethodGet,
		Verify: []config.VerifyConfig{{
			Type:              types.VerifyTypeContains,
			Operator:          types.OpContains,
			Expect:            "THIS_NEVER_APPEARS",
			ContinueOnFailure: true,
		}},
	}}, 2)

	r := rep.result.Report
	assert.Equal(t, uint64(2), r.FailedRequests,
		"continue_on_failure 只决定是否阻断依赖，不该把失败抹成成功")
}

// TestVerifyRecordedOnce 报告里的验证条目数必须等于配置声明的条数
//
// 回归用：DefaultConfig 总会给出一条顶层 verify（STATUS_CODE=200），于是
// config.Verify 从不为 nil、验证中间件总被安装；同时 loader 又把这条顶层配置
// 复制进每个未自带 verify 的 API，Worker 再跑一遍 —— 每个请求被验证两次，
// 声明 1 条却在报告里出现 2 条一模一样的记录
func TestVerifyRecordedOnce(t *testing.T) {
	url := okServer(t)

	t.Run("自带一条验证", func(t *testing.T) {
		rep := runWithAPIs(t, []config.APIConfig{{
			Name:   "own_rule",
			URL:    url,
			Method: http.MethodGet,
			Verify: []config.VerifyConfig{{
				Type:     types.VerifyTypeStatusCode,
				Operator: types.OpEQ,
				Expect:   200,
			}},
		}}, 1)

		details := rep.result.Executor.GetCollector().GetRequestDetails(0, 10, statistics.StatusFilterAll, "", "")
		require.Len(t, details, 1)
		assert.Len(t, details[0].Verifications, 1,
			"声明 1 条验证，报告里应恰好 1 条，实际 %d 条", len(details[0].Verifications))
	})

	t.Run("未自带验证时沿用默认的状态码检查", func(t *testing.T) {
		rep := runWithAPIs(t, []config.APIConfig{{
			Name:   "no_rule",
			URL:    url,
			Method: http.MethodGet,
		}}, 1)

		details := rep.result.Executor.GetCollector().GetRequestDetails(0, 10, statistics.StatusFilterAll, "", "")
		require.Len(t, details, 1)
		assert.Len(t, details[0].Verifications, 1,
			"继承来的默认验证也应只记一条，实际 %d 条", len(details[0].Verifications))
	})
}

// TestAllVerifyRulesExecuted 首条失败后其余规则仍须执行并记录
//
// 回归用：executeVerifications 原先在首个失败处直接 return，后续规则根本不会跑，
// 它们的结果也就不会进 resp.Verifications —— 使用者会以为那些规则通过了
func TestAllVerifyRulesExecuted(t *testing.T) {
	rep := runWithAPIs(t, []config.APIConfig{{
		Name:   "three_rules",
		URL:    okServer(t),
		Method: http.MethodGet,
		Verify: []config.VerifyConfig{
			{Type: types.VerifyTypeContains, Operator: types.OpContains, Expect: "NEVER_A"},
			{Type: types.VerifyTypeStatusCode, Operator: types.OpEQ, Expect: 200},
			{Type: types.VerifyTypeContains, Operator: types.OpContains, Expect: "NEVER_B"},
		},
	}}, 1)

	details := rep.result.Executor.GetCollector().GetRequestDetails(0, 10, statistics.StatusFilterAll, "", "")
	require.Len(t, details, 1)

	vs := details[0].Verifications
	require.Len(t, vs, 3, "三条规则都应执行并记录，实际 %d 条", len(vs))
	assert.False(t, vs[0].Success, "第 1 条应未通过")
	assert.True(t, vs[1].Success, "第 2 条应通过 —— 它在第 1 条失败之后，不该被跳过")
	assert.False(t, vs[2].Success, "第 3 条应未通过")
}

// TestVerifyErrorMessageIsLowCardinality 错误文本不得含响应内容
//
// 统计里的错误表以错误文本为键聚合：把响应体拼进去会让每个不同响应各占一行，
// 既看不出规律，也让错误表随请求数无界增长
func TestVerifyErrorMessageIsLowCardinality(t *testing.T) {
	// 每次响应都不同，若错误文本含响应内容则会产生多个互不相同的键
	var seq int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seq++
		fmt.Fprintf(w, `{"code":0,"nonce":%d}`, seq)
	}))
	defer srv.Close()

	rep := runWithAPIs(t, []config.APIConfig{{
		Name:   "varying_body",
		URL:    srv.URL,
		Method: http.MethodGet,
		Verify: []config.VerifyConfig{{
			Type:     types.VerifyTypeContains,
			Operator: types.OpContains,
			Expect:   "THIS_NEVER_APPEARS",
		}},
	}}, 6)

	errs := rep.result.Report.Errors
	require.Len(t, errs, 1, "响应各不相同时错误表仍应只有一行，实际 %d 行", len(errs))
	for msg, count := range errs {
		assert.Equal(t, uint64(6), count)
		assert.NotContains(t, msg, "nonce", "错误文本不应含响应内容：%s", msg)
		assert.True(t, strings.Contains(msg, "CONTAINS"),
			"错误文本应指出未通过的规则类型：%s", msg)
	}
}
