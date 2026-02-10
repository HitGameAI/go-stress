/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 16:55:12
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:15:06
 * @FilePath: \go-stress\config\thinktime.go
 * @Description: 请求间思考时间配置
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package config

import "time"

// ThinkTimeType 思考时间的分布类型
type ThinkTimeType string

const (
	// ThinkTimeConstant 固定延迟，对应 JMeter 的 ConstantTimer
	ThinkTimeConstant ThinkTimeType = "constant"
	// ThinkTimeUniform 延迟 + [0, Range) 均匀分布，对应 JMeter 的 UniformRandomTimer
	ThinkTimeUniform ThinkTimeType = "uniform"
	// ThinkTimeGaussian 延迟 + 标准差为 Deviation 的正态偏移，对应 JMeter 的 GaussianRandomTimer
	ThinkTimeGaussian ThinkTimeType = "gaussian"
)

// ThinkTimeConfig 请求之间的思考时间
//
// 为什么需要它：不带思考时间的压测是「全速打」（开环），而真实用户在两次操作之间
// 会停顿，这是「闭环」负载。两者对同一个服务施加的压力相差一个数量级，混淆了就
// 得不出能用的结论。JMeter 脚本里的 Timer 表达的正是这件事，导入时必须能落地
type ThinkTimeConfig struct {
	// Type 分布类型，留空时按 constant 处理
	Type ThinkTimeType `json:"type,omitempty" yaml:"type,omitempty"`
	// Delay 基础延迟
	Delay time.Duration `json:"delay,omitempty" yaml:"delay,omitempty"`
	// Range uniform 的随机区间上界（实际延迟为 Delay + [0, Range)）
	Range time.Duration `json:"range,omitempty" yaml:"range,omitempty"`
	// Deviation gaussian 的标准差（实际延迟为 Delay + N(0, Deviation)，负值截断为 0）
	Deviation time.Duration `json:"deviation,omitempty" yaml:"deviation,omitempty"`
}

// IsZero 判断是否无需等待，用于在热路径上跳过整套计算
func (c *ThinkTimeConfig) IsZero() bool {
	return c == nil || (c.Delay <= 0 && c.Range <= 0 && c.Deviation <= 0)
}

// Normalize 补齐缺省值并校正非法组合，返回自身便于链式调用
func (c *ThinkTimeConfig) Normalize() *ThinkTimeConfig {
	if c == nil {
		return nil
	}
	if c.Type == "" {
		c.Type = ThinkTimeConstant
	}
	// 负值一律按 0 处理：时间不能倒流，与其在热路径上反复判断不如此处夹一次
	if c.Delay < 0 {
		c.Delay = 0
	}
	if c.Range < 0 {
		c.Range = 0
	}
	if c.Deviation < 0 {
		c.Deviation = 0
	}
	return c
}
