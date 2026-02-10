/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-02-10 16:56:28
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-02-10 17:15:22
 * @FilePath: \go-stress\config\csvdata.go
 * @Description: CSV 数据驱动参数源
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package config

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
)

// CSVSharing CSV 游标的共享方式，对应 JMeter CSVDataSet 的 shareMode
type CSVSharing string

const (
	// CSVSharingAll 全部并发共享一个游标，每条请求序列取走一行且互不重复
	// 对应 JMeter 的 "All threads"，也是默认行为
	CSVSharingAll CSVSharing = "all"
	// CSVSharingWorker 每个并发各持独立游标，都从第一行开始
	// 对应 JMeter 的 "Current thread"
	CSVSharingWorker CSVSharing = "worker"
)

// CSVDataConfig CSV 数据驱动参数源
//
// 没有它，一万次请求只能打同一个账号，服务端的缓存、唯一索引、限流都会把压测
// 结果带偏，而真实流量是每个请求各带不同参数的
type CSVDataConfig struct {
	// Path CSV 文件路径
	Path string `json:"path" yaml:"path"`
	// Variables 变量名列表，按列顺序对应；留空则取文件首行作为表头
	Variables []string `json:"variables,omitempty" yaml:"variables,omitempty"`
	// Delimiter 分隔符，留空为逗号；只取首个字符
	Delimiter string `json:"delimiter,omitempty" yaml:"delimiter,omitempty"`
	// Recycle 数据用尽后是否回到首行；用指针以区分"未配置"与"显式 false"，默认 true
	Recycle *bool `json:"recycle,omitempty" yaml:"recycle,omitempty"`
	// IgnoreFirstLine 显式给出 Variables 时，是否仍跳过文件首行（表头）
	// 未给 Variables 时首行必然被当作表头，此项无意义
	IgnoreFirstLine bool `json:"ignore_first_line,omitempty" yaml:"ignore_first_line,omitempty"`
	// Sharing 游标共享方式，留空为 all
	Sharing CSVSharing `json:"sharing,omitempty" yaml:"sharing,omitempty"`
}

// ShouldRecycle 返回是否循环取数，未配置时为 true
func (c *CSVDataConfig) ShouldRecycle() bool {
	return c.Recycle == nil || *c.Recycle
}

// CSVData 已载入内存的 CSV 数据
//
// 启动时整体读入而非流式逐行读：压测的热路径上不能有文件 IO 与解析，
// 取数必须退化为一次下标访问
type CSVData struct {
	// Variables 变量名，顺序与 Rows 的列一致
	Variables []string
	// Rows 数据行，长度不足的行在载入时已补齐为空串
	Rows [][]string
	// Recycle 用尽后是否回绕
	Recycle bool
	// Sharing 游标共享方式
	Sharing CSVSharing
}

// LoadCSVData 读入并校验 CSV 数据源
func LoadCSVData(cfg *CSVDataConfig) (*CSVData, error) {
	if cfg == nil || strings.TrimSpace(cfg.Path) == "" {
		return nil, fmt.Errorf("CSV 数据源未指定路径")
	}

	f, err := os.Open(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("打开 CSV 文件失败: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	// 允许各行列数不等：真实数据常有尾列缺失，由下面统一补齐，
	// 若交给 csv 包校验会直接整份文件读取失败
	reader.FieldsPerRecord = -1
	if d := []rune(cfg.Delimiter); len(d) > 0 {
		reader.Comma = d[0]
	}

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("解析 CSV 文件失败: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("CSV 文件 %s 没有任何内容", cfg.Path)
	}

	variables := cfg.Variables
	body := records
	switch {
	case len(variables) == 0:
		// 未给变量名：首行即表头
		variables = trimAll(records[0])
		body = records[1:]
	case cfg.IgnoreFirstLine:
		body = records[1:]
	}

	if len(variables) == 0 {
		return nil, fmt.Errorf("CSV 文件 %s 未能确定变量名", cfg.Path)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("CSV 文件 %s 除表头外没有数据行", cfg.Path)
	}

	// 补齐/截断到变量个数，使取数时无需再判断长度
	rows := make([][]string, 0, len(body))
	for _, rec := range body {
		row := make([]string, len(variables))
		copy(row, rec)
		rows = append(rows, row)
	}

	sharing := cfg.Sharing
	if sharing == "" {
		sharing = CSVSharingAll
	}
	if sharing != CSVSharingAll && sharing != CSVSharingWorker {
		return nil, fmt.Errorf("CSV 数据源的 sharing 取值非法: %q（可选 all | worker）", sharing)
	}

	return &CSVData{
		Variables: variables,
		Rows:      rows,
		Recycle:   cfg.ShouldRecycle(),
		Sharing:   sharing,
	}, nil
}

// trimAll 去掉每个表头字段的首尾空白，避免 "a, b" 这类写法产出带空格的变量名
func trimAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.TrimSpace(s)
	}
	return out
}
