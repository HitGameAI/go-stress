/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 17:02:51
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:17:28
 * @FilePath: \go-stress\config\jmeter_xml.go
 * @Description: JMeter .jmx 的 XML 模型与遍历工具
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package config

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// jmxNode .jmx 的通用节点
//
// 不为每种元素声明独立 struct：.jmx 的元素标签有上百种（各类采样器、断言、
// 后置处理器、控制器、插件自带的元素），但它们的属性模型是统一的——都由
// stringProp / boolProp / intProp / elementProp / collectionProp 这几种带
// name 属性的子节点承载。用一个通用节点加 xml:",any" 收全部子节点，再按 name
// 取值，既覆盖得全，也不会因为遇到没声明过的标签就丢数据
type jmxNode struct {
	XMLName     xml.Name
	Name        string    `xml:"name,attr"`
	TestName    string    `xml:"testname,attr"`
	TestClass   string    `xml:"testclass,attr"`
	ElementType string    `xml:"elementType,attr"`
	Enabled     string    `xml:"enabled,attr"`
	Text        string    `xml:",chardata"`
	Children    []jmxNode `xml:",any"`
}

// jmxTestPlan .jmx 的根元素
type jmxTestPlan struct {
	XMLName xml.Name  `xml:"jmeterTestPlan"`
	Version string    `xml:"version,attr"`
	JMeter  string    `xml:"jmeter,attr"`
	Trees   []jmxNode `xml:"hashTree"`
}

// tag 返回节点的标签名
func (n *jmxNode) tag() string {
	return n.XMLName.Local
}

// label 返回用于日志与报告的可读名称
func (n *jmxNode) label() string {
	if n.TestName != "" {
		return n.TestName
	}
	if n.Name != "" {
		return n.Name
	}
	return n.tag()
}

// isEnabled 判断元素是否启用
//
// 必须尊重 enabled="false"：JMeter 跑的时候会跳过被禁用的元素，
// 导入时若一并算上，等于凭空多打一批 JMeter 不会发的请求
func (n *jmxNode) isEnabled() bool {
	return n.Enabled != "false"
}

// prop 取某个 name 的属性值（直接子节点，去首尾空白）
func (n *jmxNode) prop(name string) (string, bool) {
	for i := range n.Children {
		c := &n.Children[i]
		if c.Name != name {
			continue
		}
		switch c.tag() {
		case "stringProp", "boolProp", "intProp", "longProp", "doubleProp", "floatProp":
			return strings.TrimSpace(c.Text), true
		}
	}
	return "", false
}

// propString 取字符串属性，缺失时返回 def
func (n *jmxNode) propString(name, def string) string {
	if v, ok := n.prop(name); ok && v != "" {
		return v
	}
	return def
}

// propInt 取整数属性，缺失或无法解析时返回 def
func (n *jmxNode) propInt(name string, def int) int {
	v, ok := n.prop(name)
	if !ok || v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

// propFloat 取浮点属性，缺失或无法解析时返回 def
// JMeter 的延迟类参数常写成 "1000.0"，用整数解析会失败
func (n *jmxNode) propFloat(name string, def float64) float64 {
	v, ok := n.prop(name)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// propBool 取布尔属性，缺失时返回 def
func (n *jmxNode) propBool(name string, def bool) bool {
	v, ok := n.prop(name)
	if !ok || v == "" {
		return def
	}
	return strings.EqualFold(v, "true")
}

// element 取某个 name 的 elementProp 子节点
func (n *jmxNode) element(name string) *jmxNode {
	for i := range n.Children {
		c := &n.Children[i]
		if c.tag() == "elementProp" && c.Name == name {
			return c
		}
	}
	return nil
}

// collection 取某个 name 的 collectionProp 下的全部子节点
// name 为空时返回首个 collectionProp 的子节点，便于处理 name 不固定的情形
func (n *jmxNode) collection(name string) []jmxNode {
	for i := range n.Children {
		c := &n.Children[i]
		if c.tag() != "collectionProp" {
			continue
		}
		if name == "" || c.Name == name {
			return c.Children
		}
	}
	return nil
}

// jmxPair 一个元素与紧随其后的子树
type jmxPair struct {
	Node *jmxNode
	Tree *jmxNode // 可能为 nil（<hashTree/> 空子树或根本没有）
}

// pairsOf 把 hashTree 的子节点按「元素 + 紧随其后的 hashTree」配对
//
// .jmx 的层级不是靠嵌套表达的：一个元素的子元素放在它后面那个兄弟 <hashTree>
// 里，形如 [ThreadGroup, hashTree, Sampler, hashTree, ...]。不按这个规则配对
// 就还原不出「这个断言属于哪个采样器」，而断言挂错采样器会让验证结果完全错位
func pairsOf(tree *jmxNode) []jmxPair {
	if tree == nil {
		return nil
	}

	var out []jmxPair
	for i := 0; i < len(tree.Children); i++ {
		c := &tree.Children[i]
		if c.tag() == "hashTree" {
			// 没有前导元素的孤立子树，跳过
			continue
		}
		pair := jmxPair{Node: c}
		if i+1 < len(tree.Children) && tree.Children[i+1].tag() == "hashTree" {
			pair.Tree = &tree.Children[i+1]
			i++ // 子树已消费
		}
		out = append(out, pair)
	}
	return out
}

// parseJMX 解析 .jmx 内容为根 hashTree
func parseJMX(data []byte) (*jmxNode, string, error) {
	var plan jmxTestPlan
	if err := xml.Unmarshal(data, &plan); err != nil {
		return nil, "", err
	}
	if len(plan.Trees) == 0 {
		return nil, plan.JMeter, nil
	}
	return &plan.Trees[0], plan.JMeter, nil
}
