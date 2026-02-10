/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2025-12-30 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-24 00:55:15
 * @FilePath: \go-stress\statistics\realtime_server.go
 * @Description: 实时报告服务器
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package statistics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// RealtimeServer 实时报告服务器
type RealtimeServer struct {
	collector   *Collector
	builder     *ReportBuilder // 使用ReportBuilder构建报告
	server      *http.Server
	clients     map[chan []byte]bool
	mu          *syncx.RWLock
	startTime   time.Time
	endTime     time.Time
	isCompleted bool
	isPaused    bool
	isStopped   bool
	port        int
	ctx         context.Context
	cancel      context.CancelFunc
	pauseCtx    context.Context
	pauseCancel context.CancelFunc
	logger      logger.ILogger
}

// NewRealtimeServer 创建实时报告服务器
func NewRealtimeServer(collector *Collector, port int, log logger.ILogger) *RealtimeServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &RealtimeServer{
		collector: collector,
		builder:   NewReportBuilder(collector), // 初始化ReportBuilder
		mu:        syncx.NewRWLock(),
		clients:   make(map[chan []byte]bool),
		startTime: time.Now(),
		port:      port,
		ctx:       ctx,
		cancel:    cancel,
		logger:    log,
	}
}

// GetPort 获取实时报告服务器端口
func (s *RealtimeServer) GetPort() int {
	return s.port
}

// Start 启动服务器
func (s *RealtimeServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/report.css", s.handleCSS)
	mux.HandleFunc("/report.js", s.handleJS)
	mux.HandleFunc("/report_actions.js", s.handleActionsJS)
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/api/data", s.handleData)
	mux.HandleFunc("/api/details", s.handleDetails)
	mux.HandleFunc("/api/pause", s.handlePause)
	mux.HandleFunc("/api/resume", s.handleResume)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/status", s.handleStatus)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		s.logger.Info("🌐 实时报告服务器启动: http://localhost:%d", s.port)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Errorf("实时报告服务器错误: %v", err)
		}
	}()

	// 启动数据广播
	go s.broadcastLoop()

	return nil
}

// MarkCompleted 标记测试完成（固定结束时间，避免 QPS 继续变化）
func (s *RealtimeServer) MarkCompleted() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isCompleted {
		s.endTime = time.Now()
		s.isCompleted = true
		s.logger.Debug("实时服务器已标记为完成状态")
	}
}

// Stop 停止服务器
func (s *RealtimeServer) Stop() error {
	// 防止重复关闭
	s.mu.Lock()
	if s.isStopped {
		s.mu.Unlock()
		return nil
	}
	s.isStopped = true
	s.logger.Debug("🔒 正在关闭实时报告服务器...")
	s.mu.Unlock()

	// 取消context，停止broadcastLoop和所有SSE连接
	if s.cancel != nil {
		s.cancel()
	}

	// 关闭 HTTP 服务器（这会触发所有handleStream的context取消，由defer清理client channels）
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.server.Shutdown(ctx); err != nil {
			// 强制关闭
			return s.server.Close()
		}
	}
	return nil
}

// handleIndex 处理首页
func (s *RealtimeServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// 使用 HTMLFormatter 格式化实时报告
	formatter := &HTMLFormatter{
		IsRealtime:   true,
		JSONFilename: "", // 实时模式不需要 JSON 文件
	}
	report := s.collectData()
	htmlBytes, err := formatter.Format(report)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 注入 Favicon
	html := injectFavicon(string(htmlBytes))

	w.Write([]byte(html))
}

// handleCSS 提供CSS样式文件
func (s *RealtimeServer) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(reportCSS))
}

// handleJS 提供JavaScript脚本文件
func (s *RealtimeServer) handleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// 替换占位符为实时模式
	jsContent := strings.ReplaceAll(reportJS, "IS_REALTIME_PLACEHOLDER", "true")
	jsContent = strings.ReplaceAll(jsContent, "JSON_FILENAME_PLACEHOLDER", "")
	w.Write([]byte(jsContent))
}

// handleActionsJS 提供操作功能JavaScript脚本文件
func (s *RealtimeServer) handleActionsJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Write([]byte(reportActionsJS))
}

// handleStream 处理SSE流
func (s *RealtimeServer) handleStream(w http.ResponseWriter, r *http.Request) {
	// 设置SSE响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 创建客户端通道
	clientChan := make(chan []byte, 10)
	s.mu.Lock()
	s.clients[clientChan] = true
	s.mu.Unlock()

	// 使用标志记录 channel 是否已关闭
	var closeOnce sync.Once
	closeChannel := func() {
		closeOnce.Do(func() {
			close(clientChan)
		})
	}

	// 客户端断开时清理
	defer func() {
		s.mu.Lock()
		delete(s.clients, clientChan)
		s.mu.Unlock()
		closeChannel()
	}()

	// 发送初始数据
	data := s.collectData()
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	w.(http.Flusher).Flush()

	// 持续推送数据
	syncx.NewEventLoop(r.Context()).
		OnChannel(clientChan, func(msg []byte) {
			fmt.Fprintf(w, "data: %s\n\n", msg)
			w.(http.Flusher).Flush()
		}).
		Run()
}

// handleData 处理数据API请求
func (s *RealtimeServer) handleData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data := s.collectData()
	json.NewEncoder(w).Encode(data)
}

// collectData 收集当前数据 - 使用ReportBuilder简化逻辑
func (s *RealtimeServer) collectData() *Report {
	// 读取状态（包括endTime，用于完成后的固定时间计算）
	s.mu.RLock()
	isCompleted := s.isCompleted
	isPaused := s.isPaused
	isStopped := s.isStopped
	endTime := s.endTime
	s.mu.RUnlock()

	// 使用ReportBuilder构建实时报告（传递endTime避免完成后QPS持续变化）
	return s.builder.BuildRealtimeReport(s.startTime, endTime, isCompleted, isPaused, isStopped)
}

// handleDetails 处理请求明细API
func (s *RealtimeServer) handleDetails(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 解析查询参数
	query := r.URL.Query()
	offset := 0
	limit := 100
	nodeId := ""
	taskId := ""
	// 支持 status 参数：all | success | failed | skipped
	statusParam := query.Get("status")
	statusFilter := ParseStatusFilter(statusParam) // 使用枚举

	if o := query.Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}
	if l := query.Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	// 直接取字符串而非 Sscanf("%d")：nodeId/taskId 都是字符串（如 "node-1"），
	// 用 %d 扫进 *string 会得到 "bad verb '%d' for string" 并保持空值，
	// 于是这两个过滤条件一直静默失效
	nodeId = query.Get("nodeId")
	taskId = query.Get("taskId")

	// 限制每次最多返回1000条
	if limit > 1000 {
		limit = 1000
	}

	details := s.collector.GetRequestDetails(offset, limit, statusFilter, nodeId, taskId)
	detailsCount := s.collector.GetRequestDetailsCount(statusFilter, nodeId, taskId)

	// 直接从原子计数器读取统计数据（O(1)操作，无锁）
	response := map[string]interface{}{
		"total":          detailsCount, // 已保存的详情记录数
		"offset":         offset,
		"limit":          limit,
		"details":        details,
		"total_requests": s.collector.totalRequests.Load(),   // 真实总请求数（原子读取）
		"success_count":  s.collector.successRequests.Load(), // 真实成功数（原子读取）
		"failed_count":   s.collector.failedRequests.Load(),  // 真实失败数（原子读取）
		"skipped_count":  s.collector.skippedRequests.Load(), // 真实跳过数（原子读取）
	}

	json.NewEncoder(w).Encode(response)
}

// handlePause 处理暂停请求
func (s *RealtimeServer) handlePause(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.mu.Lock()
	if !s.isPaused && !s.isStopped {
		s.isPaused = true
		s.logger.Warn("⏸  压测已暂停")
	}
	s.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "已暂停",
		"status":  "paused",
	})
}

// handleResume 处理恢复请求
func (s *RealtimeServer) handleResume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.mu.Lock()
	if s.isPaused && !s.isStopped {
		s.isPaused = false
		s.logger.Info("▶️  压测已恢复")
	}
	s.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "已恢复",
		"status":  "running",
	})
}

// handleStop 处理停止请求
func (s *RealtimeServer) handleStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.mu.Lock()
	s.isStopped = true
	s.isPaused = false
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()

	s.logger.Warn("⏹  压测已停止")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "已停止",
		"status":  "stopped",
	})
}

// handleStatus 处理状态查询请求
func (s *RealtimeServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.mu.RLock()
	defer s.mu.RUnlock()

	status := "running"
	if s.isStopped {
		status = "stopped"
	} else if s.isPaused {
		status = "paused"
	} else if s.isCompleted {
		status = "completed"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       status,
		"is_paused":    s.isPaused,
		"is_stopped":   s.isStopped,
		"is_completed": s.isCompleted,
	})
}

// IsPaused 检查是否暂停
func (s *RealtimeServer) IsPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isPaused
}

// IsStopped 检查是否停止
func (s *RealtimeServer) IsStopped() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isStopped
}

// broadcastLoop 广播循环 - 使用 EventLoop
func (s *RealtimeServer) broadcastLoop() {
	// 使用 EventLoop 统一管理定时广播
	syncx.NewEventLoop(s.ctx).
		OnTicker(1*time.Second, func() {
			s.mu.RLock()
			if len(s.clients) == 0 {
				s.mu.RUnlock()
				return
			}
			s.mu.RUnlock()

			data := s.collectData()
			jsonData, err := json.Marshal(data)
			if err != nil {
				return
			}

			s.mu.RLock()
			for clientChan := range s.clients {
				select {
				case clientChan <- jsonData:
				default:
					// 通道已满，跳过
				}
			}
			s.mu.RUnlock()
		}).
		Run()
}
