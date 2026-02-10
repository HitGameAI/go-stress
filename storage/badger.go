/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-26 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-26 15:30:00
 * @FilePath: \go-stress\storage\badger.go
 * @Description: BadgerDB 存储适配器 - 高性能 LSM-Tree 存储
 *
 * Copyright (c) 2026 by kamalyes, All Rights Reserved.
 */
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/kamalyes/go-logger"
	"github.com/kamalyes/go-toolbox/pkg/syncx"
)

// BadgerStorage BadgerDB 存储（实现 Interface）
type BadgerStorage struct {
	db        *badger.DB
	writeChan chan *RequestResult
	batchSize int
	wg        sync.WaitGroup
	mu        sync.RWMutex
	nodeID    string
	logger    logger.ILogger
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc

	// 实时计数器
	totalCount   *syncx.Uint64
	successCount *syncx.Uint64
	failedCount  *syncx.Uint64
	skippedCount *syncx.Uint64
}

// NewBadgerStorage 创建 BadgerDB 存储
func NewBadgerStorage(dbPath, nodeID string, log logger.ILogger) (*BadgerStorage, error) {
	log.Infof("🗄️  初始化 BadgerDB 存储: %s (节点: %s)", dbPath, nodeID)

	// BadgerDB 配置
	opts := badger.DefaultOptions(dbPath).
		WithLoggingLevel(badger.WARNING). // 减少日志
		WithNumVersionsToKeep(1).         // 只保留最新版本
		WithCompactL0OnClose(true).       // 关闭时压缩
		WithValueThreshold(256).          // 大于 256 字节的值单独存储
		WithNumMemtables(2).              // 内存表数量
		WithNumLevelZeroTables(2).        // L0 表数量
		WithNumLevelZeroTablesStall(4).   // L0 表停顿阈值
		WithMaxLevels(5).                 // 最大层级
		WithValueLogFileSize(64 << 20).   // 64MB value log
		WithBlockCacheSize(64 << 20).     // 64MB block cache
		WithIndexCacheSize(32 << 20).     // 32MB index cache
		WithSyncWrites(false).            // 异步写入（性能优先）
		WithDetectConflicts(false).       // 禁用冲突检测
		WithNumCompactors(2)              // 压缩线程数

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("打开 BadgerDB 失败: %w", err)
	}
	log.Infof("✅ BadgerDB 已启动 (节点: %s, 路径: %s)", nodeID, dbPath)

	ctx, cancel := context.WithCancel(context.Background())

	storage := &BadgerStorage{
		db:           db,
		writeChan:    make(chan *RequestResult, 10000), // 1万缓冲
		batchSize:    500,                              // 每批 500 条
		nodeID:       nodeID,
		logger:       log,
		closed:       false,
		ctx:          ctx,
		cancel:       cancel,
		totalCount:   syncx.NewUint64(0),
		successCount: syncx.NewUint64(0),
		failedCount:  syncx.NewUint64(0),
		skippedCount: syncx.NewUint64(0),
	}

	// 启动事件循环（批量写入 + GC）
	storage.wg.Add(1)
	go storage.runEventLoop()

	return storage, nil
}

// Write 异步写入请求详情
func (s *BadgerStorage) Write(detail *RequestResult) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	select {
	case s.writeChan <- detail:
		// 成功入队
	default:
		// 队列满，同步写入（避免丢数据）
		s.logger.Warnf("⚠️  写入队列已满，同步写入: %s", detail.ID)
		s.writeOne(detail)
	}
}

// runEventLoop 使用 EventLoop 统一管理批量写入和GC
func (s *BadgerStorage) runEventLoop() {
	defer s.wg.Done()

	batch := make([]*RequestResult, 0, s.batchSize)

	// 批量写入刷新函数
	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := s.writeBatch(batch); err != nil {
			s.logger.Errorf("❌ BadgerDB 批量写入失败: %v", err)
		} else {
			s.logger.Debugf("✅ BadgerDB 批量写入成功: %d 条", len(batch))
		}

		batch = batch[:0] // 清空但保留容量
	}

	// GC 执行函数
	runGC := func() {
		s.mu.RLock()
		closed := s.closed
		s.mu.RUnlock()

		if closed {
			return
		}

		err := s.db.RunValueLogGC(0.5) // 回收 50% 以上空间的日志文件
		// badger.ErrNoRewrite 表示没有可回收的内容，这是正常情况
		if err != nil && !errors.Is(err, badger.ErrNoRewrite) {
			s.logger.Warnf("⚠️  BadgerDB GC 警告: %v", err)
		}
	}

	// 使用 EventLoop 统一管理事件循环
	syncx.NewEventLoop(s.ctx).
		OnChannel(s.writeChan, func(detail *RequestResult) {
			batch = append(batch, detail)
			// 达到批量大小，立即写入
			if len(batch) >= s.batchSize {
				flush()
			}
		}).
		OnTicker(1*time.Second, flush). // 每秒定时刷新
		OnTicker(5*time.Minute, runGC). // 每5分钟GC
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

// writeOne 同步写入单条
func (s *BadgerStorage) writeOne(detail *RequestResult) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := s.makeKey(detail)
		value, err := json.Marshal(detail)
		if err != nil {
			return err
		}

		if err := txn.Set(key, value); err != nil {
			return err
		}

		// 更新计数器
		s.totalCount.Add(1)
		if detail.Skipped {
			s.skippedCount.Add(1)
		} else if detail.Success {
			s.successCount.Add(1)
		} else {
			s.failedCount.Add(1)
		}

		return nil
	})
}

// writeBatch 批量写入
func (s *BadgerStorage) writeBatch(batch []*RequestResult) error {
	wb := s.db.NewWriteBatch()
	defer wb.Cancel()

	for _, detail := range batch {
		key := s.makeKey(detail)
		value, err := json.Marshal(detail)
		if err != nil {
			s.logger.Errorf("❌ 序列化失败: %v", err)
			continue
		}

		if err := wb.Set(key, value); err != nil {
			s.logger.Errorf("❌ 写入失败: %v", err)
			continue
		}

		// 更新计数器
		s.totalCount.Add(1)
		if detail.Skipped {
			s.skippedCount.Add(1)
		} else if detail.Success {
			s.successCount.Add(1)
		} else {
			s.failedCount.Add(1)
		}
	}

	return wb.Flush()
}

// makeKey 生成存储键
// 格式: req:{nodeID}:{taskID}:{timestamp}:{id}
func (s *BadgerStorage) makeKey(detail *RequestResult) []byte {
	return []byte(fmt.Sprintf("req:%s:%s:%d:%s",
		detail.NodeID,
		detail.TaskID,
		detail.Timestamp.Unix(),
		detail.ID,
	))
}

// Query 分页查询请求详情
func (s *BadgerStorage) Query(offset, limit int, statusFilter StatusFilter, nodeID, taskID string) ([]*RequestResult, error) {
	results := make([]*RequestResult, 0, limit)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = limit * 2
		opts.Reverse = true // 倒序（最新的在前）

		it := txn.NewIterator(opts)
		defer it.Close()

		// 构建前缀
		prefix := s.makePrefix(nodeID, taskID)
		skipped := 0
		matched := 0

		for it.Seek([]byte(prefix + "\xff")); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()

			// 获取值
			var detail RequestResult
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &detail)
			})
			if err != nil {
				s.logger.Errorf("❌ 反序列化失败: %v", err)
				continue
			}

			// nodeID / taskID 过滤（前缀未必能完全收窄，见 makePrefix）
			if !s.matchIDs(&detail, nodeID, taskID) {
				continue
			}

			// 状态过滤
			if !s.matchFilter(&detail, statusFilter) {
				continue
			}

			// 跳过 offset
			if skipped < offset {
				skipped++
				continue
			}

			// 达到 limit，停止
			if matched >= limit {
				break
			}

			results = append(results, &detail)
			matched++
		}

		return nil
	})

	return results, err
}

// Count 统计总数
func (s *BadgerStorage) Count(statusFilter StatusFilter, nodeID, taskID string) (int, error) {
	// 如果没有过滤条件，直接返回计数器
	if nodeID == "" && taskID == "" {
		switch statusFilter {
		case StatusFilterSuccess:
			return int(s.successCount.Load()), nil
		case StatusFilterFailed:
			return int(s.failedCount.Load()), nil
		case StatusFilterSkipped:
			return int(s.skippedCount.Load()), nil
		case StatusFilterAll:
			return int(s.totalCount.Load()), nil
		}
	}

	// 有过滤条件，需要遍历统计
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // 只需要键

		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := s.makePrefix(nodeID, taskID)

		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()

			// 需要获取值来判断状态
			var detail RequestResult
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &detail)
			})
			if err != nil {
				continue
			}

			if s.matchIDs(&detail, nodeID, taskID) && s.matchFilter(&detail, statusFilter) {
				count++
			}
		}

		return nil
	})

	return count, err
}

// makePrefix 生成查询前缀
func (s *BadgerStorage) makePrefix(nodeID, taskID string) string {
	// 键的布局是 req:<nodeID>:<taskID>:<ts>:<id>，taskID 不是前导分量，
	// 只给 taskID 时拼出的 "req::<taskID>:" 匹配不到任何真实键（真实键形如
	// req:node-1:task-1:...），过滤结果恒为空；此时只能退回全量扫描，
	// 由 matchIDs 逐条比对
	if nodeID == "" {
		return "req:"
	}
	if taskID == "" {
		return fmt.Sprintf("req:%s:", nodeID)
	}
	return fmt.Sprintf("req:%s:%s:", nodeID, taskID)
}

// matchIDs 比对 nodeID/taskID，补上前缀无法收窄的那部分过滤
// 前缀已收窄的情况下再比对一次的成本可忽略，换来的是 nodeID/taskID 自身含 ':'
// 时也不会因前缀过度匹配而多算
func (s *BadgerStorage) matchIDs(detail *RequestResult, nodeID, taskID string) bool {
	if nodeID != "" && detail.NodeID != nodeID {
		return false
	}
	if taskID != "" && detail.TaskID != taskID {
		return false
	}
	return true
}

// matchFilter 匹配状态过滤器
func (s *BadgerStorage) matchFilter(detail *RequestResult, filter StatusFilter) bool {
	switch filter {
	case StatusFilterSuccess:
		return detail.Success && !detail.Skipped
	case StatusFilterFailed:
		return !detail.Success && !detail.Skipped
	case StatusFilterSkipped:
		return detail.Skipped
	default:
		return true
	}
}

// Close 关闭存储
func (s *BadgerStorage) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.logger.Info("🔒 关闭 BadgerDB 存储...")

	// 取消 context，触发事件循环关闭（OnShutdown 负责排空并落盘）
	//
	// 不 close(writeChan)：已关闭的通道在 reflect.Select 里恒为 ready，会让事件
	// 循环空转；且与并发的 Write 撞上时会 panic: send on closed channel
	s.cancel()
	s.wg.Wait()

	return s.db.Close()
}

// GetNodeID 获取节点ID
func (s *BadgerStorage) GetNodeID() string {
	return s.nodeID
}

// GetStats 获取存储统计信息
func (s *BadgerStorage) GetStats() map[string]interface{} {
	lsm, vlog := s.db.Size()

	return map[string]interface{}{
		"type":          "badger",
		"node_id":       s.nodeID,
		"total_count":   s.totalCount.Load(),
		"success_count": s.successCount.Load(),
		"failed_count":  s.failedCount.Load(),
		"skipped_count": s.skippedCount.Load(),
		"lsm_size":      lsm,
		"vlog_size":     vlog,
		"total_size":    lsm + vlog,
	}
}
