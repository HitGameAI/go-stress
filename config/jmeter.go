/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:03:36
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:18:05
 * @FilePath: \go-stress\config\jmeter.go
 * @Description: JMeter .jmx 脚本导入
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kamalyes/go-stress/types"
)

// JMXDropSeverity 未能导入的元素的严重程度
type JMXDropSeverity int

const (
	// JMXDropInfo 忽略它不影响施加的负载（如监听器、结果树）
	JMXDropInfo JMXDropSeverity = iota
	// JMXDropWarn 丢失了断言或提取等语义，负载模型不变但校验强度下降
	JMXDropWarn
	// JMXDropFatal 会改变施加的负载本身，静默忽略必然得出错误结论
	JMXDropFatal
)

func (s JMXDropSeverity) String() string {
	switch s {
	case JMXDropFatal:
		return "FATAL"
	case JMXDropWarn:
		return "WARN"
	default:
		return "INFO"
	}
}

// JMXDrop 一条未能导入的记录
type JMXDrop struct {
	Element  string          // 元素标签，如 ConstantThroughputTimer
	Name     string          // 元素的 testname
	Reason   string          // 为什么导不了
	Severity JMXDropSeverity // 严重程度
}

// JMXReport .jmx 转换报告
//
// 这份报告是导入功能的主体而非附属品：压测工具最坏的失败模式不是报错，
// 而是静默产出一个「不是你要的那个测试」，数字却煞有介事。凡是没能原样
// 搬过来的东西都必须逐条说清
type JMXReport struct {
	Source        string    // 源文件
	JMeterVersion string    // 脚本声明的 JMeter 版本
	Mapped        []string  // 已映射的条目
	Drops         []JMXDrop // 未能导入的条目
}

// HasFatal 是否存在会改变负载模型的丢失项
func (r *JMXReport) HasFatal() bool {
	for _, d := range r.Drops {
		if d.Severity == JMXDropFatal {
			return true
		}
	}
	return false
}

// FatalDrops 返回全部致命丢失项
func (r *JMXReport) FatalDrops() []JMXDrop {
	var out []JMXDrop
	for _, d := range r.Drops {
		if d.Severity == JMXDropFatal {
			out = append(out, d)
		}
	}
	return out
}

// String 渲染为可直接打印的报告文本
func (r *JMXReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "JMeter 脚本转换报告: %s", r.Source)
	if r.JMeterVersion != "" {
		fmt.Fprintf(&b, " (JMeter %s)", r.JMeterVersion)
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60) + "\n")

	for _, m := range r.Mapped {
		fmt.Fprintf(&b, "  ✓ %s\n", m)
	}

	if len(r.Drops) > 0 {
		b.WriteString("\n  未能导入:\n")
		for _, d := range r.Drops {
			name := d.Name
			if name != "" && name != d.Element {
				name = fmt.Sprintf(" 「%s」", name)
			} else {
				name = ""
			}
			fmt.Fprintf(&b, "  ✗ [%s] %s%s\n      %s\n",
				d.Severity, d.Element, name, d.Reason)
		}
	}
	return b.String()
}

// addMapped 记录一条成功映射
func (r *JMXReport) addMapped(format string, args ...any) {
	r.Mapped = append(r.Mapped, fmt.Sprintf(format, args...))
}

// drop 记录一条丢失
func (r *JMXReport) drop(sev JMXDropSeverity, element, name, reason string) {
	r.Drops = append(r.Drops, JMXDrop{
		Element: element, Name: name, Reason: reason, Severity: sev,
	})
}

// jmxVarPattern JMeter 的变量引用 ${name}
// 排除 ${__func(...)} 形式的函数调用：那是 JMeter 的内置函数，不是变量
var jmxVarPattern = regexp.MustCompile(`\$\{([A-Za-z0-9_\-.]+)\}`)

// jmxFuncPattern JMeter 的内置函数调用 ${__xxx(...)}
var jmxFuncPattern = regexp.MustCompile(`\$\{__[A-Za-z0-9_]+\s*\(`)

// convertVars 把 JMeter 的 ${name} 改写为 go-stress 的 {{.name}}
func convertVars(s string) string {
	if s == "" {
		return s
	}
	return jmxVarPattern.ReplaceAllString(s, "{{.$1}}")
}

// hasJMeterFunc 判断文本里是否含 JMeter 内置函数调用
func hasJMeterFunc(s string) bool {
	return jmxFuncPattern.MatchString(s)
}

// LoadJMeterPlan 读取 .jmx 并转换为 go-stress 配置
//
// 返回的报告无论成功与否都有效，调用方应当完整呈现给使用者。
// allowLossy 为 false 时，一旦存在会改变负载模型的丢失项即报错而不返回配置
func LoadJMeterPlan(path string, allowLossy bool) (*Config, *JMXReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 JMeter 脚本失败: %w", err)
	}

	root, version, err := parseJMX(data)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 JMeter 脚本失败: %w", err)
	}

	report := &JMXReport{Source: path, JMeterVersion: version}
	if root == nil {
		return nil, report, fmt.Errorf("JMeter 脚本中没有 hashTree，文件可能不完整")
	}

	c := &jmxConverter{
		report:  report,
		baseDir: filepath.Dir(path),
	}
	cfg, err := c.convert(root)
	if err != nil {
		return nil, report, err
	}

	if report.HasFatal() && !allowLossy {
		var names []string
		for _, d := range report.FatalDrops() {
			names = append(names, d.Element)
		}
		return nil, report, fmt.Errorf(
			"脚本中有 %d 处无法导入且会改变施加的负载（%s），"+
				"按原样导入会得出错误结论；确认可接受差异后加 -jmx-allow-lossy 继续",
			len(names), strings.Join(names, ", "))
	}

	return cfg, report, nil
}

// jmxConverter 承载一次转换的中间状态
type jmxConverter struct {
	report  *JMXReport
	baseDir string // .jmx 所在目录，用于解析 CSV 的相对路径

	// onSampleError 线程组的错误处理策略，决定依赖链的失败传播如何对齐
	onSampleError   string
	threadGroupName string
}

// convert 从根 hashTree 转换出配置
func (c *jmxConverter) convert(root *jmxNode) (*Config, error) {
	cfg := DefaultConfig()
	cfg.Protocol = types.ProtocolHTTP
	cfg.APIs = nil

	var threadGroups int

	for _, p := range pairsOf(root) {
		switch p.Node.tag() {
		case "TestPlan":
			if !p.Node.isEnabled() {
				continue
			}
			c.applyTestPlan(p.Node, cfg)
			// 测试计划自身的子树里放着线程组与全局元素
			threadGroups += c.walkPlanTree(p.Tree, cfg)
		default:
			c.dropUnknown(p.Node)
		}
	}

	if threadGroups == 0 {
		return nil, fmt.Errorf("脚本中没有启用的线程组（ThreadGroup），无从得出并发与请求数")
	}
	if len(cfg.APIs) == 0 {
		return nil, fmt.Errorf("脚本中没有启用的 HTTP 采样器（HTTPSamplerProxy），没有可发的请求")
	}

	// 把采样器串成依赖链，使它们按脚本顺序逐个执行
	//
	// 这一步不能省：JMeter 的采样器在每轮循环里是全部按序执行的，而 go-stress
	// 在没有 depends_on 时走 API 选择器（轮询），每轮只发其中一个。登录→查询会
	// 变成交替发送，请求配比和依赖关系双双不成立
	c.chainAPIs(cfg)

	// 单个采样器且无验证/提取/思考时间时退化为单 API 写法，更贴近手写习惯
	if len(cfg.APIs) == 1 {
		only := cfg.APIs[0]
		if len(only.Verify) == 0 && len(only.Extractors) == 0 && only.ThinkTime.IsZero() {
			cfg.URL = only.URL
			cfg.Method = only.Method
			cfg.Body = only.Body
			cfg.Headers = only.Headers
			cfg.APIs = nil
			return cfg, nil
		}
	}

	// 用 APIs 表达时清掉顶层的公共请求字段，避免 DefaultConfig 的残值
	// 经公共配置合并回落到某个采样器上
	cfg.URL, cfg.Method, cfg.Body = "", "", ""

	return cfg, nil
}

// chainAPIs 把采样器按脚本顺序串成一条依赖链
func (c *jmxConverter) chainAPIs(cfg *Config) {
	if len(cfg.APIs) < 2 {
		return
	}
	for i := 1; i < len(cfg.APIs); i++ {
		cfg.APIs[i].DependsOn = []string{cfg.APIs[i-1].Name}
	}
	c.report.addMapped("%d 个请求按脚本顺序串为依赖链，每轮循环依次执行", len(cfg.APIs))

	// 依赖链的副作用：上游失败时下游会被跳过
	//
	// JMeter 的默认错误处理是 continue（某个采样器失败，后面的照发），与此不同。
	// 验证失败可以靠 ContinueOnFailure 抹平，但传输层失败（连不上、超时）无法，
	// 这点差异必须说清而不能默默带过
	if c.onSampleError == "" || c.onSampleError == "continue" {
		for i := range cfg.APIs {
			for j := range cfg.APIs[i].Verify {
				cfg.APIs[i].Verify[j].ContinueOnFailure = true
			}
		}
		c.report.drop(JMXDropWarn, "ThreadGroup", c.threadGroupName,
			"脚本的错误处理为 continue（某个采样器失败后面照发）。"+
				"验证失败已设为不阻断后续请求；但传输层失败（连接被拒、超时）仍会"+
				"使后续请求按「依赖失败」跳过，与 JMeter 不同，统计里会多出跳过计数")
	}
}

// applyTestPlan 处理 TestPlan 自身的属性（用户定义变量）
func (c *jmxConverter) applyTestPlan(n *jmxNode, cfg *Config) {
	args := n.element("TestPlan.user_defined_variables")
	if args == nil {
		return
	}
	vars := c.readArguments(args)
	if len(vars) == 0 {
		return
	}
	if cfg.Variables == nil {
		cfg.Variables = make(map[string]any, len(vars))
	}
	for k, v := range vars {
		cfg.Variables[k] = convertVars(v)
	}
	c.report.addMapped("用户定义变量 %d 项 → variables", len(vars))
}

// walkPlanTree 遍历测试计划下的元素，返回启用的线程组个数
func (c *jmxConverter) walkPlanTree(tree *jmxNode, cfg *Config) int {
	groups := 0
	for _, p := range pairsOf(tree) {
		node := p.Node
		if !node.isEnabled() {
			c.report.drop(JMXDropInfo, node.tag(), node.label(), "元素在脚本中已禁用，JMeter 不会执行它")
			continue
		}

		switch node.tag() {
		case "ThreadGroup", "SetupThreadGroup", "PostThreadGroup":
			if node.tag() != "ThreadGroup" {
				c.report.drop(JMXDropWarn, node.tag(), node.label(),
					"setUp/tearDown 线程组没有对应概念，其中的请求不会被导入")
				continue
			}
			if groups > 0 {
				c.report.drop(JMXDropFatal, node.tag(), node.label(),
					"存在多个线程组，而 go-stress 只有一组全局并发配置；"+
						"合并会改变各组的并发比例，请拆成多个脚本分别导入")
				continue
			}
			c.applyThreadGroup(node, cfg)
			c.walkGroupTree(p.Tree, cfg, nil)
			groups++

		// 全局作用域的元素：挂在测试计划下，对所有采样器生效
		case "HeaderManager":
			h := c.readHeaders(node)
			if len(h) > 0 {
				cfg.Headers = mergeHeaders(cfg.Headers, h)
				c.report.addMapped("全局请求头 %d 条 → headers", len(h))
			}
		case "CSVDataSet":
			c.applyCSVDataSet(node, cfg)
		case "Arguments":
			vars := c.readArguments(node)
			if len(vars) > 0 {
				if cfg.Variables == nil {
					cfg.Variables = make(map[string]any, len(vars))
				}
				for k, v := range vars {
					cfg.Variables[k] = convertVars(v)
				}
				c.report.addMapped("用户定义变量 %d 项 → variables", len(vars))
			}
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer":
			if tt := c.readTimer(node); tt != nil {
				cfg.ThinkTime = tt
				c.report.addMapped("%s → 全局思考时间 %s", node.tag(), describeThinkTime(tt))
			}
		default:
			c.dropUnknown(node)
		}
	}
	return groups
}

// applyThreadGroup 把线程组映射为并发、请求数、ramp-up 与持续时间
func (c *jmxConverter) applyThreadGroup(n *jmxNode, cfg *Config) {
	c.onSampleError = strings.ToLower(n.propString("ThreadGroup.on_sample_error", "continue"))
	c.threadGroupName = n.label()

	threads := n.propInt("ThreadGroup.num_threads", 1)
	if threads < 1 {
		threads = 1
	}
	cfg.Concurrency = uint64(threads)

	rampUp := time.Duration(n.propInt("ThreadGroup.ramp_time", 0)) * time.Second
	if rampUp > 0 {
		if cfg.Advanced == nil {
			cfg.Advanced = &AdvancedConfig{}
		}
		cfg.Advanced.RampUp = rampUp
	}

	detail := fmt.Sprintf("并发 %d", threads)

	// 循环次数在 main_controller 这个 LoopController 里
	loops := -1
	forever := false
	if lc := n.element("ThreadGroup.main_controller"); lc != nil {
		forever = lc.propBool("LoopController.continue_forever", false)
		loops = lc.propInt("LoopController.loops", -1)
	}

	// scheduler=true 时以持续时间为准，JMeter 此时忽略循环次数
	if n.propBool("ThreadGroup.scheduler", false) {
		if d := n.propInt("ThreadGroup.duration", 0); d > 0 {
			cfg.Duration = time.Duration(d) * time.Second
			cfg.Requests = 0
			detail += fmt.Sprintf("，持续 %s", cfg.Duration)
			if delay := n.propInt("ThreadGroup.delay", 0); delay > 0 {
				c.report.drop(JMXDropWarn, "ThreadGroup", n.label(),
					fmt.Sprintf("启动延迟 %ds（ThreadGroup.delay）没有对应配置，已忽略", delay))
			}
		}
	}

	if cfg.Duration == 0 {
		switch {
		case forever || loops < 0:
			c.report.drop(JMXDropFatal, "LoopController", n.label(),
				"循环次数为「永远」而线程组又没开调度器，无法推出请求总数；"+
					"请在脚本里设定循环次数或持续时间，或导入后用 -n / -duration 指定")
		case loops == 0:
			c.report.drop(JMXDropFatal, "LoopController", n.label(),
				"循环次数为 0，导入后不会发出任何请求")
		default:
			cfg.Requests = uint64(loops)
			detail += fmt.Sprintf("，每并发 %d 次", loops)
		}
	}

	if rampUp > 0 {
		detail += fmt.Sprintf("，ramp-up %s", rampUp)
	}
	c.report.addMapped("线程组「%s」→ %s", n.label(), detail)
}

// walkGroupTree 遍历线程组下的元素
// inherited 为来自上层的思考时间，供采样器在自身未配置时继承
func (c *jmxConverter) walkGroupTree(tree *jmxNode, cfg *Config, inherited *ThinkTimeConfig) {
	// 先扫一遍，收集作用于整组的元素（JMeter 的作用域规则：
	// 同层级的配置元件对该层级下所有采样器生效，与书写顺序无关）
	groupHeaders := map[string]string{}
	groupThink := inherited
	for _, p := range pairsOf(tree) {
		n := p.Node
		if !n.isEnabled() {
			continue
		}
		switch n.tag() {
		case "HeaderManager":
			for k, v := range c.readHeaders(n) {
				groupHeaders[k] = v
			}
		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer":
			if tt := c.readTimer(n); tt != nil {
				groupThink = tt
			}
		case "CSVDataSet":
			c.applyCSVDataSet(n, cfg)
		}
	}
	if len(groupHeaders) > 0 {
		c.report.addMapped("线程组级请求头 %d 条 → 应用到该组所有请求", len(groupHeaders))
	}
	if groupThink != nil && groupThink != inherited {
		c.report.addMapped("线程组级思考时间 → %s", describeThinkTime(groupThink))
	}

	for _, p := range pairsOf(tree) {
		n := p.Node
		if !n.isEnabled() {
			c.report.drop(JMXDropInfo, n.tag(), n.label(), "元素在脚本中已禁用，JMeter 不会执行它")
			continue
		}

		switch n.tag() {
		case "HTTPSamplerProxy", "HTTPSampler", "HTTPSampler2":
			api := c.buildAPI(n, p.Tree, groupHeaders, groupThink)
			if api != nil {
				cfg.APIs = append(cfg.APIs, *api)
			}

		// 上面已统一处理过，这里不再重复报告
		case "HeaderManager", "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "CSVDataSet":

		case "Arguments":
			vars := c.readArguments(n)
			if len(vars) > 0 {
				if cfg.Variables == nil {
					cfg.Variables = make(map[string]any, len(vars))
				}
				for k, v := range vars {
					cfg.Variables[k] = convertVars(v)
				}
				c.report.addMapped("用户定义变量 %d 项 → variables", len(vars))
			}

		default:
			c.dropUnknown(n)
		}
	}
}

// buildAPI 把一个 HTTP 采样器及其子树转换为 APIConfig
func (c *jmxConverter) buildAPI(n, tree *jmxNode, groupHeaders map[string]string, groupThink *ThinkTimeConfig) *APIConfig {
	rawURL, err := c.samplerURL(n)
	if err != nil {
		c.report.drop(JMXDropFatal, n.tag(), n.label(), err.Error())
		return nil
	}

	api := &APIConfig{
		Name:   sanitizeAPIName(n.label()),
		URL:    rawURL,
		Method: strings.ToUpper(n.propString("HTTPSampler.method", "GET")),
	}

	// 请求头：组级为底，采样器自身的覆盖之
	headers := map[string]string{}
	for k, v := range groupHeaders {
		headers[k] = v
	}

	think := groupThink
	var detail []string

	for _, p := range pairsOf(tree) {
		child := p.Node
		if !child.isEnabled() {
			c.report.drop(JMXDropInfo, child.tag(), child.label(), "元素在脚本中已禁用，JMeter 不会执行它")
			continue
		}

		switch child.tag() {
		case "HeaderManager":
			for k, v := range c.readHeaders(child) {
				headers[k] = v
			}

		case "ResponseAssertion":
			api.Verify = append(api.Verify, c.readResponseAssertion(child)...)
		case "JSONPathAssertion":
			if v := c.readJSONPathAssertion(child); v != nil {
				api.Verify = append(api.Verify, *v)
			}
		case "DurationAssertion":
			if ms := child.propInt("DurationAssertion.duration", 0); ms > 0 {
				api.Verify = append(api.Verify, VerifyConfig{
					Type:        types.VerifyTypeResponseTime,
					Operator:    types.OpLTE,
					Expect:      ms,
					Description: "由 DurationAssertion 转换",
				})
			}
		case "SizeAssertion":
			if v := c.readSizeAssertion(child); v != nil {
				api.Verify = append(api.Verify, *v)
			}

		case "JSONPostProcessor":
			api.Extractors = append(api.Extractors, c.readJSONExtractors(child)...)
		case "RegexExtractor":
			if e := c.readRegexExtractor(child); e != nil {
				api.Extractors = append(api.Extractors, *e)
			}

		case "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer":
			if tt := c.readTimer(child); tt != nil {
				think = tt
			}

		default:
			c.dropUnknown(child)
		}
	}

	if len(headers) > 0 {
		api.Headers = headers
	}
	if !think.IsZero() {
		api.ThinkTime = think
	}

	if body, ok := c.samplerBody(n); ok {
		api.Body = body
	}

	if len(api.Verify) > 0 {
		detail = append(detail, fmt.Sprintf("%d 条验证", len(api.Verify)))
	}
	if len(api.Extractors) > 0 {
		detail = append(detail, fmt.Sprintf("%d 个提取器", len(api.Extractors)))
	}
	if !api.ThinkTime.IsZero() {
		detail = append(detail, "思考时间 "+describeThinkTime(api.ThinkTime))
	}
	suffix := ""
	if len(detail) > 0 {
		suffix = "（" + strings.Join(detail, "、") + "）"
	}
	c.report.addMapped("请求「%s」%s %s%s", api.Name, api.Method, api.URL, suffix)

	return api
}

// samplerURL 由 protocol/domain/port/path 拼出完整 URL
func (c *jmxConverter) samplerURL(n *jmxNode) (string, error) {
	path := convertVars(n.propString("HTTPSampler.path", ""))
	domain := convertVars(n.propString("HTTPSampler.domain", ""))

	// path 本身可能已是完整 URL（脚本里常直接写死）
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path, nil
	}

	if domain == "" {
		return "", fmt.Errorf("采样器没有 domain，且 path 也不是完整 URL，无从得出请求地址" +
			"（JMeter 可能依赖 HTTP Request Defaults，该元素尚未支持）")
	}

	scheme := strings.ToLower(convertVars(n.propString("HTTPSampler.protocol", "http")))
	if scheme != "http" && scheme != "https" {
		scheme = "http"
	}

	host := domain
	if port := convertVars(n.propString("HTTPSampler.port", "")); port != "" && port != "0" {
		// 默认端口不写进 URL，保持与脚本观感一致
		if !(scheme == "http" && port == "80") && !(scheme == "https" && port == "443") {
			host = domain + ":" + port
		}
	}

	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return scheme + "://" + host + path, nil
}

// samplerBody 取请求体
//
// JMeter 有两种写法：postBodyRaw=true 时 Arguments 的首个值即原始报文；
// 否则 Arguments 是「名=值」的表单参数
func (c *jmxConverter) samplerBody(n *jmxNode) (string, bool) {
	args := n.element("HTTPsampler.Arguments")
	if args == nil {
		args = n.element("HTTPSampler.Arguments")
	}
	if args == nil {
		return "", false
	}

	items := args.collection("Arguments.arguments")
	if len(items) == 0 {
		return "", false
	}

	if n.propBool("HTTPSampler.postBodyRaw", false) {
		raw := items[0].propString("Argument.value", "")
		if raw == "" {
			return "", false
		}
		if hasJMeterFunc(raw) {
			c.report.drop(JMXDropWarn, "HTTPSamplerProxy", n.label(),
				"请求体里含 JMeter 内置函数 ${__xxx()}，已原样保留但不会被求值")
		}
		return convertVars(raw), true
	}

	// 表单参数：拼成 a=1&b=2
	var parts []string
	for i := range items {
		name := items[i].propString("Argument.name", "")
		val := items[i].propString("Argument.value", "")
		if name == "" {
			continue
		}
		parts = append(parts, convertVars(name)+"="+convertVars(val))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "&"), true
}

// readHeaders 读 HeaderManager
func (c *jmxConverter) readHeaders(n *jmxNode) map[string]string {
	out := map[string]string{}
	for _, item := range n.collection("HeaderManager.headers") {
		name := item.propString("Header.name", "")
		if name == "" {
			continue
		}
		out[convertVars(name)] = convertVars(item.propString("Header.value", ""))
	}
	return out
}

// readArguments 读 Arguments / 用户定义变量
func (c *jmxConverter) readArguments(n *jmxNode) map[string]string {
	out := map[string]string{}
	for _, item := range n.collection("Arguments.arguments") {
		name := item.propString("Argument.name", "")
		if name == "" {
			continue
		}
		out[name] = item.propString("Argument.value", "")
	}
	return out
}

// readTimer 把 JMeter 的定时器转成思考时间
func (c *jmxConverter) readTimer(n *jmxNode) *ThinkTimeConfig {
	switch n.tag() {
	case "ConstantTimer":
		d := msToDuration(n.propFloat("ConstantTimer.delay", 0))
		if d <= 0 {
			return nil
		}
		return (&ThinkTimeConfig{Type: ThinkTimeConstant, Delay: d}).Normalize()

	case "UniformRandomTimer":
		// JMeter 的 UniformRandomTimer: delay 为固定偏移，range 为随机上界
		delay := msToDuration(n.propFloat("ConstantDelayOffset", 0))
		rng := msToDuration(n.propFloat("RandomTimer.range", 0))
		if delay <= 0 && rng <= 0 {
			return nil
		}
		return (&ThinkTimeConfig{Type: ThinkTimeUniform, Delay: delay, Range: rng}).Normalize()

	case "GaussianRandomTimer":
		delay := msToDuration(n.propFloat("ConstantDelayOffset", 0))
		dev := msToDuration(n.propFloat("RandomTimer.range", 0))
		if delay <= 0 && dev <= 0 {
			return nil
		}
		return (&ThinkTimeConfig{Type: ThinkTimeGaussian, Delay: delay, Deviation: dev}).Normalize()
	}
	return nil
}

// applyCSVDataSet 把 CSVDataSet 转成 CSV 数据源配置
func (c *jmxConverter) applyCSVDataSet(n *jmxNode, cfg *Config) {
	path := n.propString("filename", "")
	if path == "" {
		c.report.drop(JMXDropFatal, n.tag(), n.label(), "CSVDataSet 没有 filename，无法确定数据文件")
		return
	}
	// JMeter 脚本里多为相对 .jmx 的路径
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.baseDir, path)
	}

	if cfg.CSVData != nil {
		c.report.drop(JMXDropFatal, n.tag(), n.label(),
			"存在多个 CSVDataSet，而 go-stress 目前只支持一个数据源；"+
				"多份数据会改变参数组合，请合并成一个 CSV 后再导入")
		return
	}

	var names []string
	if raw := n.propString("variableNames", ""); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}
	}

	recycle := n.propBool("recycle", true)
	sharing := CSVSharingAll
	switch mode := n.propString("shareMode", ""); {
	case strings.Contains(mode, "thread") && !strings.Contains(mode, "group"):
		// shareMode.thread = 每线程独立
		sharing = CSVSharingWorker
	case mode != "" && !strings.Contains(mode, "all"):
		c.report.drop(JMXDropWarn, n.tag(), n.label(),
			fmt.Sprintf("shareMode=%q 没有完全对应的语义，已按全局共享处理", mode))
	}

	cfg.CSVData = &CSVDataConfig{
		Path:            path,
		Variables:       names,
		Delimiter:       n.propString("delimiter", ","),
		Recycle:         &recycle,
		IgnoreFirstLine: n.propBool("ignoreFirstLine", false),
		Sharing:         sharing,
	}

	if n.propBool("stopThread", false) && recycle {
		c.report.drop(JMXDropWarn, n.tag(), n.label(),
			"同时配了 recycle=true 与 stopThread=true，JMeter 以 recycle 优先，此处同样按回绕处理")
	}

	c.report.addMapped("CSVDataSet「%s」→ csv_data（%s，sharing=%s，recycle=%v）",
		n.label(), path, sharing, recycle)
}

// readResponseAssertion 转换 ResponseAssertion
//
// JMeter 用位掩码表达匹配方式：MATCH=1 CONTAINS=2 NOT=4 EQUALS=8 SUBSTRING=16 OR=32
func (c *jmxConverter) readResponseAssertion(n *jmxNode) []VerifyConfig {
	const (
		flagMatch     = 1
		flagContains  = 2
		flagNot       = 4
		flagEquals    = 8
		flagSubstring = 16
		flagOr        = 32
	)

	field := n.propString("Assertion.test_field", "Assertion.response_data")
	testType := n.propInt("Assertion.test_type", flagSubstring)
	negate := testType&flagNot != 0

	// JMeter 的 collectionProp 名字确实拼错成 Asserion，两种都认
	items := n.collection("Asserion.test_strings")
	if items == nil {
		items = n.collection("Assertion.test_strings")
	}

	var values []string
	for i := range items {
		if v := strings.TrimSpace(items[i].Text); v != "" {
			values = append(values, convertVars(v))
		}
	}
	if len(values) == 0 {
		c.report.drop(JMXDropWarn, n.tag(), n.label(), "断言没有填任何期望值，已忽略")
		return nil
	}

	if testType&flagOr != 0 && len(values) > 1 {
		c.report.drop(JMXDropWarn, n.tag(), n.label(),
			"断言用了「或」组合多个期望值，而 go-stress 的多条验证之间是「与」；"+
				"已逐条转换，校验会比脚本更严格")
	}

	var out []VerifyConfig
	for _, v := range values {
		switch field {
		case "Assertion.response_code":
			op := types.OpEQ
			if negate {
				op = types.OpNE
			}
			out = append(out, VerifyConfig{
				Type:        types.VerifyTypeStatusCode,
				Operator:    op,
				Expect:      v,
				Description: "由 ResponseAssertion(response_code) 转换",
			})

		case "Assertion.response_data":
			switch {
			case testType&flagMatch != 0:
				if negate {
					c.report.drop(JMXDropWarn, n.tag(), n.label(),
						"「不匹配正则」没有对应的验证类型，该条已跳过")
					continue
				}
				out = append(out, VerifyConfig{
					Type:        types.VerifyTypeRegex,
					Expect:      v,
					Description: "由 ResponseAssertion(matches) 转换",
				})
			case testType&(flagSubstring|flagContains) != 0:
				op := types.OpContains
				if negate {
					op = types.OpNotContains
				}
				out = append(out, VerifyConfig{
					Type:        types.VerifyTypeContains,
					Operator:    op,
					Expect:      v,
					Description: "由 ResponseAssertion(substring) 转换",
				})
			case testType&flagEquals != 0:
				c.report.drop(JMXDropWarn, n.tag(), n.label(),
					"「响应体全文相等」没有对应的验证类型，该条已跳过")
				continue
			default:
				c.report.drop(JMXDropWarn, n.tag(), n.label(),
					fmt.Sprintf("无法识别的断言匹配方式 test_type=%d，该条已跳过", testType))
				continue
			}

		default:
			c.report.drop(JMXDropWarn, n.tag(), n.label(),
				fmt.Sprintf("断言作用于 %s，go-stress 只支持 response_code 与 response_data，该条已跳过", field))
			continue
		}
	}
	return out
}

// readJSONPathAssertion 转换 JSON Assertion
func (c *jmxConverter) readJSONPathAssertion(n *jmxNode) *VerifyConfig {
	path := n.propString("JSON_PATH", "")
	if path == "" {
		c.report.drop(JMXDropWarn, n.tag(), n.label(), "JSON 断言没有填 JSONPath，已忽略")
		return nil
	}

	v := VerifyConfig{
		Type:        types.VerifyTypeJSONPath,
		JSONPath:    convertVars(path),
		Description: "由 JSONPathAssertion 转换",
	}

	if !n.propBool("JSONVALIDATION", false) {
		// 只校验路径存在，没有期望值
		v.Operator = types.OpNotEmpty
		return &v
	}

	expect := convertVars(n.propString("EXPECTED_VALUE", ""))
	if n.propBool("ISREGEX", false) {
		v.Operator = types.OpRegex
		v.Regex = true
	} else if n.propBool("INVERT", false) {
		v.Operator = types.OpNE
	} else {
		v.Operator = types.OpEQ
	}
	v.Expect = expect
	return &v
}

// readSizeAssertion 转换 SizeAssertion
func (c *jmxConverter) readSizeAssertion(n *jmxNode) *VerifyConfig {
	size := n.propInt("SizeAssertion.size", -1)
	if size < 0 {
		return nil
	}

	// SizeAssertion.operator: 1=等于 2=不等于 3=大于 4=小于 5=大于等于 6=小于等于
	var op types.ExpectOperator
	switch n.propInt("SizeAssertion.operator", 1) {
	case 2:
		op = types.OpNE
	case 3:
		op = types.OpGT
	case 4:
		op = types.OpLT
	case 5:
		op = types.OpGTE
	case 6:
		op = types.OpLTE
	default:
		op = types.OpEQ
	}

	return &VerifyConfig{
		Type:        types.VerifyTypeResponseSize,
		Operator:    op,
		Expect:      size,
		Description: "由 SizeAssertion 转换",
	}
}

// readJSONExtractors 转换 JSON Extractor（一个元素可声明多个变量，以分号分隔）
func (c *jmxConverter) readJSONExtractors(n *jmxNode) []ExtractorConfig {
	names := splitJMeterList(n.propString("JSONPostProcessor.referenceNames", ""))
	paths := splitJMeterList(n.propString("JSONPostProcessor.jsonPathExprs", ""))
	defaults := splitJMeterList(n.propString("JSONPostProcessor.defaultValues", ""))

	if len(names) == 0 || len(paths) == 0 {
		c.report.drop(JMXDropWarn, n.tag(), n.label(), "JSON 提取器缺少变量名或 JSONPath，已忽略")
		return nil
	}
	if len(names) != len(paths) {
		c.report.drop(JMXDropWarn, n.tag(), n.label(),
			fmt.Sprintf("变量名 %d 个与 JSONPath %d 个数量不符，按较少者转换", len(names), len(paths)))
	}

	count := min(len(names), len(paths))
	out := make([]ExtractorConfig, 0, count)
	for i := 0; i < count; i++ {
		e := ExtractorConfig{
			Name:     names[i],
			Type:     types.ExtractorTypeJSONPath,
			JSONPath: convertVars(paths[i]),
			Source:   ExtractorSourceResponse,
		}
		if i < len(defaults) {
			e.Default = defaults[i]
		}
		out = append(out, e)
	}
	return out
}

// readRegexExtractor 转换正则提取器
func (c *jmxConverter) readRegexExtractor(n *jmxNode) *ExtractorConfig {
	name := n.propString("RegexExtractor.refname", "")
	expr := n.propString("RegexExtractor.regex", "")
	if name == "" || expr == "" {
		c.report.drop(JMXDropWarn, n.tag(), n.label(), "正则提取器缺少变量名或表达式，已忽略")
		return nil
	}

	if field := n.propString("RegexExtractor.useHeaders", "false"); field != "false" {
		c.report.drop(JMXDropWarn, n.tag(), n.label(),
			fmt.Sprintf("提取源 useHeaders=%q 不受支持，已按响应体处理", field))
	}
	if tpl := n.propString("RegexExtractor.template", "$1$"); tpl != "$1$" {
		c.report.drop(JMXDropWarn, n.tag(), n.label(),
			fmt.Sprintf("模板 %q 不受支持，只取第一个捕获组", tpl))
	}

	return &ExtractorConfig{
		Name:    name,
		Type:    types.ExtractorTypeRegex,
		Regex:   expr,
		Source:  ExtractorSourceResponse,
		Default: n.propString("RegexExtractor.default", ""),
	}
}

// dropUnknown 记录一个未支持的元素，并按其是否影响负载定级
func (c *jmxConverter) dropUnknown(n *jmxNode) {
	tag := n.tag()

	// 会改变施加的负载：静默忽略必然得出错误结论
	fatal := map[string]string{
		"ConstantThroughputTimer":   "吞吐量定时器把负载限制为固定速率，go-stress 没有限速能力，忽略它会按全速发压",
		"PreciseThroughputTimer":    "精确吞吐量定时器同上，go-stress 没有限速能力",
		"PoissonRandomTimer":        "泊松分布定时器没有对应的思考时间类型",
		"SyncTimer":                 "集合点会把请求攒成批次再齐发，忽略它会改变请求的时间分布",
		"ThroughputController":      "吞吐量控制器按比例裁剪请求，忽略它会改变各请求的占比",
		"IfController":              "条件控制器决定请求是否发出，忽略它会多发请求",
		"WhileController":           "循环控制器决定请求发几次，忽略它会改变请求总数",
		"LoopController":            "嵌套循环控制器会改变请求总数",
		"ForeachController":         "遍历控制器按数据条数决定请求次数，忽略它会改变请求总数",
		"RandomController":          "随机控制器每轮只执行其中一个子项，忽略它会把全部子项都发出去",
		"RandomOrderController":     "随机顺序控制器会改变请求顺序",
		"InterleaveControl":         "交替控制器每轮只执行一个子项，忽略它会把全部子项都发出去",
		"SwitchController":          "分支控制器每轮只执行一个子项",
		"RunTime":                   "运行时间控制器限定子项的执行时长",
		"JSR223PreProcessor":        "脚本前置处理器可改写请求内容，无法转换",
		"JSR223PostProcessor":       "脚本后置处理器可改写提取结果，无法转换",
		"JSR223Sampler":             "脚本采样器自行发起请求，无法转换",
		"BeanShellPreProcessor":     "BeanShell 前置处理器可改写请求内容，无法转换",
		"BeanShellPostProcessor":    "BeanShell 后置处理器可改写提取结果，无法转换",
		"BeanShellSampler":          "BeanShell 采样器自行发起请求，无法转换",
		"ConfigTestElement":         "HTTP Request Defaults 为采样器提供缺省的域名与路径，缺了它可能拼不出完整 URL",
		"AuthManager":               "HTTP 授权管理器会自动附加认证头，缺了它请求可能全部被拒",
		"CookieManager":             "Cookie 管理器维护会话，缺了它依赖登录态的请求会失败",
		"TransactionController":     "事务控制器会聚合子请求的耗时统计，指标口径与导入后不同",
		"ModuleController":          "模块控制器引用别处的可控制器，内容不在本处",
		"IncludeController":         "包含控制器引用外部 .jmx 文件，内容不在本处",
		"TestFragmentController":    "测试片段需由模块控制器引用才会执行",
		"CriticalSectionController": "临界区控制器会串行化子项的执行",
	}

	// 只影响校验强度或统计呈现，不改变发出的请求
	warn := map[string]string{
		"XPathAssertion":       "XPath 断言不受支持（go-stress 面向 JSON，无 XML 路径验证）",
		"XPath2Assertion":      "XPath2 断言不受支持",
		"HTMLAssertion":        "HTML 合法性断言不受支持",
		"MD5HexAssertion":      "MD5 断言不受支持",
		"SMIMEAssertion":       "SMIME 断言不受支持",
		"CompareAssertion":     "比较断言不受支持",
		"XPathExtractor":       "XPath 提取器不受支持",
		"XPath2Extractor":      "XPath2 提取器不受支持",
		"HtmlExtractor":        "CSS/jQuery 提取器不受支持",
		"BoundaryExtractor":    "边界提取器不受支持，可改写成正则提取器",
		"DebugPostProcessor":   "调试后置处理器只产出调试信息，无需转换",
		"DebugSampler":         "调试采样器不发真实请求",
		"UserParameters":       "用户参数（按线程轮换的变量）不受支持，可改用 CSV 数据源",
		"RandomVariableConfig": "随机变量配置不受支持，可改用内置的动态变量函数",
		"CounterConfig":        "计数器配置不受支持，可改用内置的 seq 变量函数",
		"DNSCacheManager":      "DNS 缓存管理器不受支持",
		"KeystoreConfig":       "密钥库配置不受支持",
		"LoginConfig":          "登录配置不受支持",
	}

	// 纯粹的结果收集与展示，对负载没有任何影响
	info := map[string]bool{
		"ResultCollector": true, "ViewResultsFullVisualizer": true,
		"SummaryReport": true, "StatVisualizer": true, "TableVisualizer": true,
		"GraphVisualizer": true, "SplineVisualizer": true, "MonitorHealthVisualizer": true,
		"SimpleDataWriter": true, "AssertionVisualizer": true, "ComparisonVisualizer": true,
		"MailerResultCollector": true, "BackendListener": true, "TestPlan": true,
		"WorkBench": true, "kg.apc.jmeter.vizualizers.CorrectedResultCollector": true,
	}

	switch {
	case info[tag]:
		c.report.drop(JMXDropInfo, tag, n.label(), "监听器/结果展示元素，不影响施加的负载")
	case fatal[tag] != "":
		c.report.drop(JMXDropFatal, tag, n.label(), fatal[tag])
	case warn[tag] != "":
		c.report.drop(JMXDropWarn, tag, n.label(), warn[tag])
	default:
		// 认不出来的一律按会影响负载处理：宁可挡下让人确认，不可默默放过
		c.report.drop(JMXDropFatal, tag, n.label(),
			"未知元素，无法判断它是否影响施加的负载；请确认后决定是否忽略")
	}
}

// ===== 小工具 =====

// msToDuration 把 JMeter 的毫秒数转为 Duration
func msToDuration(ms float64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// splitJMeterList 拆分 JMeter 的分号分隔列表
func splitJMeterList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// sanitizeAPIName 把采样器名整理成可用作变量前缀的 API 名
// 提取变量以 "apiName.varName" 为键，名字里带点号会把键切错
func sanitizeAPIName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "request"
	}
	s = strings.NewReplacer(".", "_", " ", "_", "\t", "_").Replace(s)
	return s
}

// describeThinkTime 渲染思考时间用于报告
func describeThinkTime(c *ThinkTimeConfig) string {
	if c.IsZero() {
		return "无"
	}
	switch c.Type {
	case ThinkTimeUniform:
		return fmt.Sprintf("%s + [0,%s) 均匀分布", c.Delay, c.Range)
	case ThinkTimeGaussian:
		return fmt.Sprintf("%s ± %s 正态分布", c.Delay, c.Deviation)
	default:
		return c.Delay.String()
	}
}
