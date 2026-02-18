//go:build cgo

/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-24 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-25 22:12:09
 * @FilePath: \go-stress\storage\sqlite.go
 * @Description: SQLite存储层 - 持久化请求详情（支持无限存储）
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/mathx"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
	_ "github.com/mattn/go-sqlite3"
)

const (
	// 表名常量
	tableRequestDetails = "request_details"

	// sqliteCGOEnabled 标记当前二进制包含真正的 SQLite 实现
	// （测试用它跳过 sqlite 用例，与 sqlite_stub.go 中的同名常量互斥）
	sqliteCGOEnabled = true
)

// DetailStorage SQLite持久化存储（实现 Interface）
type DetailStorage struct {
	db          *sql.DB
	writeChan   chan *RequestResult
	batchSize   int
	flushTicker *time.Ticker
	wg          sync.WaitGroup
	closed      bool
	mu          sync.Mutex
	nodeID      string // 节点ID（分布式模式下标识数据来源）
	logger      logger.ILogger
	ctx         context.Context
	cancel      context.CancelFunc

	// 统计信息
	//
	// 用原子类型而非裸 uint64：dropCount 在 Write 里自增，而 Write 由所有 worker
	// 并发调用（通道打满时就会走到），裸自增是数据竞争；writeCount/flushCount 虽只由
	// batchWriter 单协程写，但 GetStats 是对外方法，可被其他协程随时读
	writeCount atomic.Uint64 // 写入总数
	flushCount atomic.Uint64 // 刷新次数
	dropCount  atomic.Uint64 // 丢弃数（通道满）
}

// NewDetailStorage 创建存储实例
func NewDetailStorage(dbPath, nodeID string, log logger.ILogger) (*DetailStorage, error) {
	// 如果不是内存模式，确保目录存在
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("创建目录失败: %w", err)
		}
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池（SQLite 仅支持单写多读）
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // 连接永不过期

	// 优化 SQLite 性能
	pragmas := []string{
		"PRAGMA journal_mode = WAL",    // Write-Ahead Logging 模式
		"PRAGMA synchronous = NORMAL",  // 平衡性能和安全性
		"PRAGMA cache_size = 10000",    // 10MB 缓存
		"PRAGMA temp_store = MEMORY",   // 临时表存内存
		"PRAGMA mmap_size = 268435456", // 256MB 内存映射
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			log.Warnf("⚠️  执行 %s 失败: %v", pragma, err)
		}
	}

	// 创建表结构
	schema := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (
		id TEXT PRIMARY KEY,
		node_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		group_id TEXT,
		api_name TEXT,
		timestamp INTEGER NOT NULL,
		url TEXT,
		method TEXT,
		query TEXT,
		headers TEXT,
		body TEXT,
		duration INTEGER NOT NULL,
		status_code INTEGER,
		success INTEGER NOT NULL,
		skipped INTEGER NOT NULL,
		size INTEGER,
		error TEXT,
		response_body TEXT,
		response_headers TEXT,
		verifications TEXT,
		extracted_vars TEXT,
		trace_id TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_node_id ON %s(node_id);
	CREATE INDEX IF NOT EXISTS idx_task_id ON %s(task_id);
	CREATE INDEX IF NOT EXISTS idx_timestamp ON %s(timestamp);
	CREATE INDEX IF NOT EXISTS idx_success ON %s(success);
	CREATE INDEX IF NOT EXISTS idx_skipped ON %s(skipped);
	CREATE INDEX IF NOT EXISTS idx_api_name ON %s(api_name);
	`, tableRequestDetails, tableRequestDetails, tableRequestDetails, tableRequestDetails, tableRequestDetails, tableRequestDetails, tableRequestDetails)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("创建表失败: %w", err)
	}

	// 既有库补列：CREATE TABLE IF NOT EXISTS 对已存在的表什么都不做，
	// 旧库因此没有 trace_id，而查询按固定列名读取会直接失败
	// 列已存在时 SQLite 报 "duplicate column name"，属预期，忽略即可
	if _, err := db.Exec(fmt.Sprintf(
		"ALTER TABLE %s ADD COLUMN trace_id TEXT", tableRequestDetails,
	)); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		db.Close()
		return nil, fmt.Errorf("升级表结构失败: %w", err)
	}

	if dbPath != ":memory:" {
		log.Infof("💾 SQLite 存储已启用: %s (节点: %s)", dbPath, nodeID)
	} else {
		log.Infof("💾 SQLite 内存模式已启用 (节点: %s)", nodeID)
	}

	// ctx/cancel 必须初始化：batchWriter 的 EventLoop 仅在 ctx.Done() 时退出，
	// 此处漏掉会让 s.ctx 为 nil，关闭分支永不触发，Close 的 wg.Wait() 永久阻塞
	ctx, cancel := context.WithCancel(context.Background())

	storage := &DetailStorage{
		db:          db,
		writeChan:   make(chan *RequestResult, 10000), // 1万缓冲
		batchSize:   100,                              // 每100条批量写入
		flushTicker: time.NewTicker(1 * time.Second),  // 每秒强制刷新
		ctx:         ctx,
		cancel:      cancel,
		nodeID:      nodeID,
		logger:      log,
	}

	// 启动异步写入协程
	storage.wg.Add(1)
	go storage.batchWriter()

	return storage, nil
}

// Write 异步写入请求详情（实现 Interface）
func (s *DetailStorage) Write(detail *RequestResult) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	select {
	case s.writeChan <- detail:
		// 写入成功
	default:
		// 通道满了，丢弃（避免阻塞主流程）
		dropped := s.dropCount.Add(1)
		if dropped%1000 == 1 { // 每1000次丢弃警告一次
			s.logger.Warnf("⚠️  写入通道已满，已丢弃 %d 条记录", dropped)
		}
	}
}

// batchWriter 批量写入协程 - 使用 EventLoop
func (s *DetailStorage) batchWriter() {
	defer s.wg.Done()

	batch := make([]*RequestResult, 0, s.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		start := time.Now()
		if err := s.insertBatch(batch); err != nil {
			// 写入失败，记录日志但不阻塞
			s.logger.Errorf("❌ 批量写入 %d 条记录失败: %v", len(batch), err)
		} else {
			written := s.writeCount.Add(uint64(len(batch)))
			s.flushCount.Add(1)

			// 每写入10000条记录输出一次统计
			if written%10000 == 0 {
				duration := time.Since(start)
				s.logger.Debugf("📊 已写入 %d 条记录 (本次: %d 条, 耗时: %v)",
					written, len(batch), duration)
			}
		}

		batch = batch[:0] // 清空但保留容量
	}

	// 使用 EventLoop 统一管理事件循环
	syncx.NewEventLoop(s.ctx).
		OnChannel(s.writeChan, func(detail *RequestResult) {
			batch = append(batch, detail)
			if len(batch) >= s.batchSize {
				flush()
			}
		}).
		OnTicker(1*time.Second, flush). // 定时刷新
		// OnShutdown 先排空通道再落盘：事件循环退出时通道里可能还积着上万条
		// 尚未取走的记录（writeChan 缓冲 10000），只 flush 内存里的 batch 会把
		// 这一批结果连同最后的统计一起丢掉
		OnShutdown(func() {
			for {
				select {
				case detail := <-s.writeChan:
					batch = append(batch, detail)
					if len(batch) >= s.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}).
		Run()
}

// insertBatch 批量插入
func (s *DetailStorage) insertBatch(batch []*RequestResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(fmt.Sprintf(`
		INSERT INTO %s (
			id, node_id, task_id, group_id, api_name, timestamp, url, method, query, headers, body,
			duration, status_code, success, skipped, size, error,
			response_body, response_headers, verifications, extracted_vars, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, tableRequestDetails))
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, detail := range batch {
		// 序列化复杂字段
		headersJSON, _ := json.Marshal(detail.Headers)
		respHeadersJSON, _ := json.Marshal(detail.ResponseHeaders)
		verificationsJSON, _ := json.Marshal(detail.Verifications)
		extractedVarsJSON, _ := json.Marshal(detail.ExtractedVars)

		_, err := stmt.Exec(
			detail.ID,
			detail.NodeID,
			detail.TaskID,
			detail.GroupID,
			detail.APIName,
			detail.Timestamp.Unix(),
			detail.URL,
			detail.Method,
			detail.Query,
			string(headersJSON),
			detail.Body,
			detail.Duration.Microseconds(),
			detail.StatusCode,
			mathx.IF(detail.Success, 1, 0),
			mathx.IF(detail.Skipped, 1, 0),
			detail.Size,
			detail.ErrorMsg,
			detail.ResponseBody,
			string(respHeadersJSON),
			string(verificationsJSON),
			string(extractedVarsJSON),
			detail.TraceID,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// detailColumns 查询用的显式列清单，顺序与 scanDetail 的 Scan 参数严格对应
//
// 不用 SELECT *：列顺序由建表语句决定，而旧库经 ALTER TABLE 补列后新列排在末尾，
// 与新建库的顺序未必一致；一旦错位，按位置 Scan 会把值塞进错误的字段且不报错
const detailColumns = `id, node_id, task_id, group_id, api_name, timestamp,
	url, method, query, headers, body,
	duration, status_code, success, skipped, size, error,
	response_body, response_headers, verifications, extracted_vars, trace_id`

// Query 分页查询请求详情
func (s *DetailStorage) Query(offset, limit int, statusFilter StatusFilter, nodeID, taskID string) ([]*RequestResult, error) {
	query := fmt.Sprintf("SELECT %s FROM %s", detailColumns, tableRequestDetails)
	where := []string{}
	args := []any{}

	// 根据状态过滤
	switch statusFilter {
	case StatusFilterSuccess:
		where = append(where, "success = 1 AND skipped = 0")
	case StatusFilterFailed:
		where = append(where, "success = 0 AND skipped = 0")
	case StatusFilterSkipped:
		where = append(where, "skipped = 1")
	}

	// 节点/任务 ID 用占位符绑定，不得拼进 SQL：
	// 这两个值来自实时报告与 Master 的 HTTP 查询参数，由外部完全可控，
	// 原先的 fmt.Sprintf("node_id = '%s'", nodeID) 是一个 SQL 注入口子
	if nodeID != "" {
		where = append(where, "node_id = ?")
		args = append(args, nodeID)
	}
	if taskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, taskID)
	}

	// 组装 WHERE 子句
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*RequestResult
	unscannable := 0
	var firstScanErr error
	for rows.Next() {
		detail, err := s.scanDetail(rows)
		if err != nil {
			// 不静默丢弃：无法解析的行若直接 continue，报告里的明细会比 Count
			// 少若干条而没有任何提示（例如旧库里存在 NULL 列，Scan 进 string/
			// float64 会失败）。逐行告警在 schema 整体不匹配时会刷屏，
			// 故只留首个错误并在循环结束后汇总一次
			unscannable++
			if firstScanErr == nil {
				firstScanErr = err
			}
			continue
		}
		results = append(results, detail)
	}
	if unscannable > 0 {
		s.logger.Warnf("⚠️  查询结果中有 %d 行无法解析已跳过（首个错误: %v）", unscannable, firstScanErr)
	}

	return results, nil
}

// Count 统计总数
func (s *DetailStorage) Count(statusFilter StatusFilter, nodeID, taskID string) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", tableRequestDetails)
	where := []string{}
	args := []any{}

	// 根据状态过滤
	switch statusFilter {
	case StatusFilterSuccess:
		where = append(where, "success = 1 AND skipped = 0")
	case StatusFilterFailed:
		where = append(where, "success = 0 AND skipped = 0")
	case StatusFilterSkipped:
		where = append(where, "skipped = 1")
	}

	// 节点/任务 ID 用占位符绑定，理由同 Query
	if nodeID != "" {
		where = append(where, "node_id = ?")
		args = append(args, nodeID)
	}
	if taskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, taskID)
	}

	// 组装 WHERE 子句
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	var count int
	err := s.db.QueryRow(query, args...).Scan(&count)
	return count, err
}

// scanDetail 扫描行数据
func (s *DetailStorage) scanDetail(rows *sql.Rows) (*RequestResult, error) {
	var (
		detail                               RequestResult
		timestamp, duration                  int64
		success, skipped                     int
		headersJSON, respHeadersJSON         string
		verificationsJSON, extractedVarsJSON string
		// 旧库经 ALTER TABLE 补出的 trace_id 对历史行为 NULL，需用可空类型接
		traceID sql.NullString
	)

	err := rows.Scan(
		&detail.ID, &detail.NodeID, &detail.TaskID, &detail.GroupID, &detail.APIName, &timestamp,
		&detail.URL, &detail.Method, &detail.Query, &headersJSON, &detail.Body,
		&duration, &detail.StatusCode, &success, &skipped, &detail.Size, &detail.ErrorMsg,
		&detail.ResponseBody, &respHeadersJSON, &verificationsJSON, &extractedVarsJSON,
		&traceID,
	)
	if err != nil {
		return nil, err
	}

	detail.Timestamp = time.Unix(timestamp, 0)
	detail.Duration = time.Duration(duration) * time.Microsecond
	detail.Success = success == 1
	detail.Skipped = skipped == 1
	detail.TraceID = traceID.String

	json.Unmarshal([]byte(headersJSON), &detail.Headers)
	json.Unmarshal([]byte(respHeadersJSON), &detail.ResponseHeaders)
	json.Unmarshal([]byte(verificationsJSON), &detail.Verifications)
	json.Unmarshal([]byte(extractedVarsJSON), &detail.ExtractedVars)

	return &detail, nil
}

// Close 关闭存储
func (s *DetailStorage) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// 取消 ctx 让 batchWriter 的 EventLoop 退出（OnShutdown 负责排空并落盘）
	//
	// 这里不 close(writeChan)：EventLoop 基于 reflect.Select，已关闭的通道恒为
	// ready，会让事件循环空转打满 CPU 而非退出；且 Write 是先查 closed、再松锁、
	// 后投递，与 close 并发时会 panic: send on closed channel
	s.cancel()

	// 停止定时器
	s.flushTicker.Stop()

	// 等待 batchWriter 完成（会自动刷新剩余数据）
	s.wg.Wait()

	// 输出最终统计
	s.logger.Infof("✅ SQLite 存储已关闭")
	s.logger.Infof("   📝 总写入: %d 条", s.writeCount.Load())
	s.logger.Infof("   🔄 刷新次数: %d 次", s.flushCount.Load())
	if dropped := s.dropCount.Load(); dropped > 0 {
		s.logger.Warnf("   ⚠️  丢弃记录: %d 条", dropped)
	}

	return s.db.Close()
}

// GetNodeID 获取节点ID（实现 Interface）
func (s *DetailStorage) GetNodeID() string {
	return s.nodeID
}

// GetStats 获取存储统计信息
func (s *DetailStorage) GetStats() (writeCount, flushCount, dropCount uint64) {
	return s.writeCount.Load(), s.flushCount.Load(), s.dropCount.Load()
}
