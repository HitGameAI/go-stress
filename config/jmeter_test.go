/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:10:28
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:20:16
 * @FilePath: \go-stress\config\jmeter_test.go
 * @Description: JMeter 脚本导入测试
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamalyes/go-stress/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadJMeterPlanSimple(t *testing.T) {
	cfg, report, err := LoadJMeterPlan("testdata/simple.jmx", false)
	require.NoError(t, err, "报告:\n%s", report)
	require.NotNil(t, cfg)

	t.Run("线程组映射为并发与请求数", func(t *testing.T) {
		assert.Equal(t, uint64(50), cfg.Concurrency)
		assert.Equal(t, uint64(200), cfg.Requests)
		require.NotNil(t, cfg.Advanced)
		assert.Equal(t, 10*time.Second, cfg.Advanced.RampUp)
	})

	t.Run("用户定义变量", func(t *testing.T) {
		assert.Equal(t, "api.example.com", cfg.Variables["host"])
	})

	t.Run("禁用的采样器不被导入", func(t *testing.T) {
		for _, api := range cfg.APIs {
			assert.NotContains(t, api.URL, "should-not-run",
				"enabled=false 的采样器不该出现，JMeter 本身也不会执行它")
		}
		require.Len(t, cfg.APIs, 2)
	})

	t.Run("URL 由 protocol+domain+port+path 拼出且变量已转写", func(t *testing.T) {
		assert.Equal(t, "https://{{.host}}:8443/v1/login", cfg.APIs[0].URL)
		assert.Equal(t, "POST", cfg.APIs[0].Method)
		// 未写端口时不应凭空补上
		assert.Equal(t, "https://{{.host}}/v1/orders?token={{.token}}", cfg.APIs[1].URL)
	})

	t.Run("请求体与表单变量转写", func(t *testing.T) {
		assert.Equal(t, `{"user":"{{.username}}","pass":"{{.password}}"}`, cfg.APIs[0].Body)
	})

	t.Run("组级请求头下发到每个采样器", func(t *testing.T) {
		for _, api := range cfg.APIs {
			assert.Equal(t, "application/json", api.Headers["Content-Type"])
			assert.Equal(t, "{{.host}}", api.Headers["X-Env"])
		}
	})

	t.Run("断言映射", func(t *testing.T) {
		login := cfg.APIs[0]
		require.Len(t, login.Verify, 2)
		assert.Equal(t, types.VerifyTypeStatusCode, login.Verify[0].Type)
		assert.Equal(t, "200", login.Verify[0].Expect)
		assert.Equal(t, types.VerifyTypeResponseTime, login.Verify[1].Type)
		assert.Equal(t, 800, login.Verify[1].Expect)

		orders := cfg.APIs[1]
		require.Len(t, orders.Verify, 1)
		assert.Equal(t, types.VerifyTypeContains, orders.Verify[0].Type)
		assert.Equal(t, `"code":0`, orders.Verify[0].Expect)
	})

	t.Run("提取器映射", func(t *testing.T) {
		require.Len(t, cfg.APIs[0].Extractors, 1)
		e := cfg.APIs[0].Extractors[0]
		assert.Equal(t, "token", e.Name)
		assert.Equal(t, types.ExtractorTypeJSONPath, e.Type)
		assert.Equal(t, "$.data.token", e.JSONPath)
		assert.Equal(t, "NOT_FOUND", e.Default)
	})

	t.Run("定时器映射为思考时间", func(t *testing.T) {
		for _, api := range cfg.APIs {
			require.NotNil(t, api.ThinkTime, "组级定时器应下发到每个采样器")
			assert.Equal(t, ThinkTimeConstant, api.ThinkTime.Type)
			assert.Equal(t, 1500*time.Millisecond, api.ThinkTime.Delay)
		}
	})

	t.Run("CSV 数据源映射且相对路径按脚本所在目录解析", func(t *testing.T) {
		require.NotNil(t, cfg.CSVData)
		assert.Equal(t, filepath.Join("testdata", "users.csv"), cfg.CSVData.Path)
		assert.Equal(t, []string{"username", "password"}, cfg.CSVData.Variables)
		assert.True(t, cfg.CSVData.ShouldRecycle())
		assert.Equal(t, CSVSharingAll, cfg.CSVData.Sharing)
		assert.True(t, cfg.CSVData.IgnoreFirstLine)
	})

	t.Run("采样器串成依赖链以保证按序执行", func(t *testing.T) {
		// 不串链会退化成轮询，每轮只发一个，请求配比与依赖关系双双不成立
		assert.Empty(t, cfg.APIs[0].DependsOn)
		assert.Equal(t, []string{cfg.APIs[0].Name}, cfg.APIs[1].DependsOn)
	})

	t.Run("on_sample_error=continue 时验证失败不阻断后续", func(t *testing.T) {
		for _, api := range cfg.APIs {
			for _, v := range api.Verify {
				assert.True(t, v.ContinueOnFailure,
					"JMeter 默认错误处理是 continue，验证失败不应跳过后续请求")
			}
		}
	})

	t.Run("顶层公共请求字段被清空", func(t *testing.T) {
		// 否则 DefaultConfig 的残值会经公共配置合并回落到采样器上
		assert.Empty(t, cfg.URL)
		assert.Empty(t, cfg.Method)
		assert.Empty(t, cfg.Body)
	})

	t.Run("报告列出了丢弃项", func(t *testing.T) {
		assert.NotEmpty(t, report.Mapped)
		assert.NotEmpty(t, report.Drops)
		assert.False(t, report.HasFatal(), "该脚本不应有致命丢失项")
		assert.Contains(t, report.String(), "ResultCollector")
	})
}

// TestLoadJMeterPlanRejectsLossy 会改变负载的元素默认必须拦下
//
// 压测工具最坏的失败模式不是报错，而是静默产出一个「不是你要的那个测试」，
// 数字却煞有介事。限速定时器、条件控制器、脚本处理器都属于这一类
func TestLoadJMeterPlanRejectsLossy(t *testing.T) {
	cfg, report, err := LoadJMeterPlan("testdata/lossy.jmx", false)
	require.Error(t, err, "存在致命丢失项时应拒绝产出配置")
	assert.Nil(t, cfg, "拒绝时不应返回半成品配置")
	require.NotNil(t, report)

	assert.True(t, report.HasFatal())
	assert.ErrorContains(t, err, "-jmx-allow-lossy", "报错应指明如何在确认后继续")

	var elements []string
	for _, d := range report.FatalDrops() {
		elements = append(elements, d.Element)
	}
	assert.Contains(t, elements, "ConstantThroughputTimer")
	assert.Contains(t, elements, "IfController")
	assert.Contains(t, elements, "JSR223PostProcessor")

	t.Run("显式允许后可继续", func(t *testing.T) {
		cfg, _, err := LoadJMeterPlan("testdata/lossy.jmx", true)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, uint64(20), cfg.Concurrency)
		assert.Equal(t, uint64(100), cfg.Requests)
	})
}

func TestLoadJMeterPlanErrors(t *testing.T) {
	t.Run("文件不存在", func(t *testing.T) {
		_, _, err := LoadJMeterPlan("testdata/nope.jmx", false)
		assert.ErrorContains(t, err, "读取 JMeter 脚本失败")
	})

	t.Run("不是合法 XML", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.jmx")
		require.NoError(t, os.WriteFile(p, []byte("not xml at all <<<"), 0o600))
		_, _, err := LoadJMeterPlan(p, false)
		assert.ErrorContains(t, err, "解析 JMeter 脚本失败")
	})

	t.Run("没有线程组", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "empty.jmx")
		require.NoError(t, os.WriteFile(p, []byte(
			`<?xml version="1.0"?><jmeterTestPlan version="1.2"><hashTree>`+
				`<TestPlan testclass="TestPlan" testname="空"/><hashTree/>`+
				`</hashTree></jmeterTestPlan>`), 0o600))
		_, _, err := LoadJMeterPlan(p, false)
		assert.ErrorContains(t, err, "没有启用的线程组")
	})

	t.Run("循环次数为永远且未设持续时间", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "forever.jmx")
		require.NoError(t, os.WriteFile(p, []byte(
			`<?xml version="1.0"?><jmeterTestPlan version="1.2"><hashTree>`+
				`<TestPlan testclass="TestPlan" testname="x"/><hashTree>`+
				`<ThreadGroup testclass="ThreadGroup" testname="g">`+
				`<stringProp name="ThreadGroup.num_threads">5</stringProp>`+
				`<elementProp name="ThreadGroup.main_controller" elementType="LoopController">`+
				`<boolProp name="LoopController.continue_forever">true</boolProp>`+
				`<stringProp name="LoopController.loops">-1</stringProp>`+
				`</elementProp></ThreadGroup><hashTree>`+
				`<HTTPSamplerProxy testclass="HTTPSamplerProxy" testname="s">`+
				`<stringProp name="HTTPSampler.domain">h</stringProp>`+
				`<stringProp name="HTTPSampler.path">/p</stringProp>`+
				`</HTTPSamplerProxy><hashTree/>`+
				`</hashTree></hashTree></hashTree></jmeterTestPlan>`), 0o600))
		_, report, err := LoadJMeterPlan(p, false)
		require.Error(t, err, "推不出请求总数时应拦下")
		assert.True(t, report.HasFatal())
	})
}

// TestJMXSchedulerDuration 开了调度器时以持续时间为准
func TestJMXSchedulerDuration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sched.jmx")
	require.NoError(t, os.WriteFile(p, []byte(
		`<?xml version="1.0"?><jmeterTestPlan version="1.2"><hashTree>`+
			`<TestPlan testclass="TestPlan" testname="x"/><hashTree>`+
			`<ThreadGroup testclass="ThreadGroup" testname="g">`+
			`<stringProp name="ThreadGroup.num_threads">8</stringProp>`+
			`<boolProp name="ThreadGroup.scheduler">true</boolProp>`+
			`<stringProp name="ThreadGroup.duration">120</stringProp>`+
			`<elementProp name="ThreadGroup.main_controller" elementType="LoopController">`+
			`<boolProp name="LoopController.continue_forever">true</boolProp>`+
			`</elementProp></ThreadGroup><hashTree>`+
			`<HTTPSamplerProxy testclass="HTTPSamplerProxy" testname="s">`+
			`<stringProp name="HTTPSampler.domain">h.example.com</stringProp>`+
			`<stringProp name="HTTPSampler.path">/p</stringProp>`+
			`</HTTPSamplerProxy><hashTree/>`+
			`</hashTree></hashTree></hashTree></jmeterTestPlan>`), 0o600))

	cfg, report, err := LoadJMeterPlan(p, false)
	require.NoError(t, err, "报告:\n%s", report)
	assert.Equal(t, uint64(8), cfg.Concurrency)
	assert.Equal(t, 120*time.Second, cfg.Duration)
	assert.Zero(t, cfg.Requests, "以持续时间为准时不应再给请求数，JMeter 此时也忽略循环次数")
}

// TestJMXSingleSamplerDegradesToSimpleForm 单采样器且无验证时用单 API 写法
func TestJMXSingleSamplerDegradesToSimpleForm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "one.jmx")
	require.NoError(t, os.WriteFile(p, []byte(
		`<?xml version="1.0"?><jmeterTestPlan version="1.2"><hashTree>`+
			`<TestPlan testclass="TestPlan" testname="x"/><hashTree>`+
			`<ThreadGroup testclass="ThreadGroup" testname="g">`+
			`<stringProp name="ThreadGroup.num_threads">4</stringProp>`+
			`<elementProp name="ThreadGroup.main_controller" elementType="LoopController">`+
			`<stringProp name="LoopController.loops">9</stringProp>`+
			`</elementProp></ThreadGroup><hashTree>`+
			`<HTTPSamplerProxy testclass="HTTPSamplerProxy" testname="唯一请求">`+
			`<stringProp name="HTTPSampler.domain">h.example.com</stringProp>`+
			`<stringProp name="HTTPSampler.path">/ping</stringProp>`+
			`<stringProp name="HTTPSampler.method">GET</stringProp>`+
			`</HTTPSamplerProxy><hashTree/>`+
			`</hashTree></hashTree></hashTree></jmeterTestPlan>`), 0o600))

	cfg, report, err := LoadJMeterPlan(p, false)
	require.NoError(t, err, "报告:\n%s", report)
	assert.Empty(t, cfg.APIs, "单采样器无验证时应退化为单 API 写法")
	assert.Equal(t, "http://h.example.com/ping", cfg.URL)
	assert.Equal(t, "GET", cfg.Method)
}

func TestConvertVars(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"无变量":                       "无变量",
		"${token}":                  "{{.token}}",
		"a=${x}&b=${y}":             "a={{.x}}&b={{.y}}",
		"${user_name}":              "{{.user_name}}",
		"${a.b}":                    "{{.a.b}}",
		"https://${host}:8080/${p}": "https://{{.host}}:8080/{{.p}}",
		"${__time(yyyy,)}":          "${__time(yyyy,)}", // 内置函数不是变量，不改写
	}
	for in, want := range cases {
		assert.Equal(t, want, convertVars(in), "输入 %q", in)
	}
}

func TestHasJMeterFunc(t *testing.T) {
	assert.True(t, hasJMeterFunc("${__time(yyyy,)}"))
	assert.True(t, hasJMeterFunc(`{"t":"${__UUID()}"}`))
	assert.False(t, hasJMeterFunc("${token}"))
	assert.False(t, hasJMeterFunc("普通文本"))
}

// TestPairsOfHashTree hashTree 是兄弟节点而非子节点，必须成对读取
func TestPairsOfHashTree(t *testing.T) {
	root, _, err := parseJMX([]byte(
		`<?xml version="1.0"?><jmeterTestPlan version="1.2"><hashTree>` +
			`<A testname="a1"/><hashTree><B testname="b1"/><hashTree/></hashTree>` +
			`<C testname="c1"/><hashTree/>` +
			`</hashTree></jmeterTestPlan>`))
	require.NoError(t, err)
	require.NotNil(t, root)

	ps := pairsOf(root)
	require.Len(t, ps, 2, "应得到 A 与 C 两个配对，各自的 hashTree 已被消费")
	assert.Equal(t, "A", ps[0].Node.tag())
	require.NotNil(t, ps[0].Tree, "A 的子树应被认出")
	assert.Equal(t, "C", ps[1].Node.tag())

	inner := pairsOf(ps[0].Tree)
	require.Len(t, inner, 1)
	assert.Equal(t, "B", inner[0].Node.tag(), "层级必须靠兄弟 hashTree 还原")
}
