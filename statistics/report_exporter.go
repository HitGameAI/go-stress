/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 21:00:00
 * @FilePath: \go-stress\statistics\report_exporter.go
 * @Description: 报告导出器 - 职责单一化重构
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamalyes/go-logger"
)

// ReportExporter 报告导出器（职责单一：只负责导出报告到文件）
type ReportExporter struct {
	collector *Collector
	builder   *ReportBuilder
	logger    logger.ILogger
}

// NewReportExporter 创建报告导出器
func NewReportExporter(collector *Collector) *ReportExporter {
	return &ReportExporter{
		collector: collector,
		builder:   NewReportBuilder(collector),
		logger:    collector.logger,
	}
}

// ExportHTML 导出HTML报告（完整版，包含明细）
func (e *ReportExporter) ExportHTML(totalTime time.Duration, filename string) error {
	return e.ExportHTMLWithLimit(totalTime, filename, -1) // -1 表示导出全部详情
}

// ExportHTMLWithLimit 导出HTML报告（指定详情导出数量）
func (e *ReportExporter) ExportHTMLWithLimit(totalTime time.Duration, filename string, detailsLimit int) error {
	// 第一步：使用 ReportBuilder 构建完整报告（包含明细）
	report := e.builder.BuildFullReportWithLimit(totalTime, detailsLimit)

	// 第二步：生成报告目录和文件名
	reportDir := filepath.Dir(filename)
	baseName := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	jsonFilename := baseName + ".json"
	jsonFullPath := filepath.Join(reportDir, jsonFilename)

	// 第三步：生成并保存 JSON 数据文件
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON failed: %w", err)
	}
	if err := os.WriteFile(jsonFullPath, jsonBytes, 0644); err != nil {
		return fmt.Errorf("write JSON file failed: %w", err)
	}
	e.logger.Info("✅ JSON数据已生成: %s", jsonFullPath)

	// 第四步：生成静态资源文件（CSS、JS）
	if err := e.generateStaticFiles(reportDir, jsonFilename); err != nil {
		return fmt.Errorf("generate static files failed: %w", err)
	}

	// 第五步：使用 HTMLFormatter 格式化为 HTML
	formatter := &HTMLFormatter{
		IsRealtime:   false,
		JSONFilename: jsonFilename,
	}
	htmlBytes, err := formatter.Format(report)
	if err != nil {
		return fmt.Errorf("format HTML failed: %w", err)
	}

	// 第六步：写入 HTML 文件
	if err := os.WriteFile(filename, htmlBytes, 0644); err != nil {
		return fmt.Errorf("write file failed: %w", err)
	}

	e.logger.Info("✅ HTML报告已生成: %s", filename)
	return nil
}

// ExportJSON 导出JSON报告
func (e *ReportExporter) ExportJSON(totalTime time.Duration, filename string, includeDetails bool) error {
	// 使用 ReportBuilder 构建报告
	report := e.builder.BuildReport(totalTime, includeDetails)

	// 使用 JSONFormatter 格式化
	formatter := &JSONFormatter{Indent: true}
	jsonBytes, err := formatter.Format(report)
	if err != nil {
		return fmt.Errorf("format JSON failed: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(filename, jsonBytes, 0644); err != nil {
		return fmt.Errorf("write file failed: %w", err)
	}

	e.logger.Info("✅ JSON报告已生成: %s", filename)
	return nil
}

// generateStaticFiles 生成静态资源文件（CSS、JS）
func (e *ReportExporter) generateStaticFiles(reportDir, jsonFilename string) error {
	// 生成 CSS 文件
	cssPath := filepath.Join(reportDir, "report.css")
	if err := os.WriteFile(cssPath, []byte(reportCSS), 0644); err != nil {
		return fmt.Errorf("write CSS file failed: %w", err)
	}
	e.logger.Info("✅ CSS文件已生成: %s", cssPath)

	// 生成 report_actions.js 文件
	actionsJsPath := filepath.Join(reportDir, "report_actions.js")
	if err := os.WriteFile(actionsJsPath, []byte(reportActionsJS), 0644); err != nil {
		return fmt.Errorf("write Actions JS file failed: %w", err)
	}
	e.logger.Info("✅ Actions JS文件已生成: %s", actionsJsPath)

	// 生成 JS 文件（静态模式：IS_REALTIME = false，替换JSON文件名占位符）
	jsContent := strings.ReplaceAll(reportJS, "IS_REALTIME_PLACEHOLDER", "false")
	jsContent = strings.ReplaceAll(jsContent, "JSON_FILENAME_PLACEHOLDER", jsonFilename)
	jsPath := filepath.Join(reportDir, "report.js")
	if err := os.WriteFile(jsPath, []byte(jsContent), 0644); err != nil {
		return fmt.Errorf("write JS file failed: %w", err)
	}
	e.logger.Info("✅ JS文件已生成: %s", jsPath)

	return nil
}
