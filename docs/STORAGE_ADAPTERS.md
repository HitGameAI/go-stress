# 存储适配器使用指南

## 📚 概述

go-stress 支持多种存储后端，通过统一的适配器接口实现：

| 存储类型 | 性能 | 持久化 | 适用场景 |
|---------|------|--------|---------|
| **Memory** | ⭐⭐⭐⭐⭐ | ❌ | 实时压测、短期测试 |
| **SQLite** | ⭐⭐⭐ | ✅ | 单机压测、复杂查询 |
| **BadgerDB** | ⭐⭐⭐⭐⭐ | ✅ | 高并发、海量数据 |

## 🚀 快速开始

### 1. 内存存储（默认）

**特点**：性能最高，数据不持久化

```go
import "github.com/kamalyes/go-stress/statistics"

// 创建工厂
factory := statistics.NewStorageFactory(logger)

// 创建内存存储
storage, err := factory.CreateStorage(&statistics.StorageConfig{
    Type:   statistics.StorageTypeMemory,
    NodeID: "node-1",
})
```

**命令行使用**：
```bash
go-stress -url https://api.example.com -c 100 -n 10000 \
  -storage memory
```

---

### 2. SQLite 存储

**特点**：轻量级文件数据库，支持 SQL 查询

```go
storage, err := factory.CreateStorage(&statistics.StorageConfig{
    Type:   statistics.StorageTypeSQLite,
    NodeID: "node-1",
    Path:   "stress-data/reports.db",
})
```

**命令行使用**：
```bash
go-stress -url https://api.example.com -c 100 -n 10000 \
  -storage sqlite \
  -storage-path ./stress-data/reports.db
```

**配置文件**：
```yaml
storage:
  mode: sqlite
  path: ./stress-data/reports.db
  params:
    batch_size: 100
    flush_interval: 1s
```

---

### 3. BadgerDB 存储（推荐高并发场景）

**特点**：高性能 LSM-Tree 存储，纯 Go 实现

```go
storage, err := factory.CreateStorage(&statistics.StorageConfig{
    Type:   statistics.StorageTypeBadger,
    NodeID: "node-1",
    Path:   "stress-data/badger",
})
```

**命令行使用**：
```bash
go-stress -url https://api.example.com -c 1000 -n 1000000 \
  -storage badger \
  -storage-path ./stress-data/badger
```

**配置文件**：
```yaml
storage:
  mode: badger
  path: ./stress-data/badger
  params:
    batch_size: 500
    gc_interval: 5m
```

## 📊 性能对比

### 写入性能测试

| 存储类型 | 并发数 | 请求数 | 写入速度 | 内存占用 |
|---------|-------|--------|---------|---------|
| Memory  | 1000  | 100万  | ~500K/s | 2GB |
| SQLite  | 1000  | 100万  | ~50K/s  | 500MB |
| BadgerDB| 1000  | 100万  | ~300K/s | 800MB |

### 查询性能测试

| 存储类型 | 全量查询 | 过滤查询 | 聚合查询 |
|---------|---------|---------|---------|
| Memory  | 10ms    | 5ms     | 3ms |
| SQLite  | 200ms   | 50ms    | 30ms |
| BadgerDB| 100ms   | 80ms    | N/A |

## 🎯 选择建议

### 场景 1：实时压测监控
**推荐**：Memory

```bash
go-stress -config test.yaml -storage memory -realtime
```

**优势**：
- 性能最高，延迟最低
- 适合实时监控面板
- 无磁盘 IO 瓶颈

---

### 场景 2：单机压测 + 报告生成
**推荐**：SQLite

```bash
go-stress -config test.yaml -storage sqlite -storage-path ./reports.db
```

**优势**：
- 持久化数据
- 支持 SQL 查询
- 轻量级，无需额外部署

**注意**：`CGO_ENABLED=0` 静态构建（容器化默认姿势）下 SQLite 为桩实现，创建时明确报错并提示改用 `badger` 或以 CGO 构建

---

### 场景 3：高并发分布式压测
**推荐**：BadgerDB

```bash
# Master
go-stress -mode master -storage badger -storage-path ./master-data

# Slave
go-stress -mode slave -master localhost:9090 \
  -storage badger -storage-path ./slave-data
```

**优势**：
- 高并发写入
- 纯 Go 实现，无 CGO 依赖
- 自动压缩，节省空间

---

### 场景 4：超大规模压测（亿级请求）
**推荐**：BadgerDB + 定期清理

```yaml
storage:
  mode: badger
  path: ./stress-data
  retention: 7d  # 保留 7 天数据
  auto_cleanup: true
```

## 🔧 高级配置

### 1. 批量写入优化

**SQLite**：
```yaml
storage:
  mode: sqlite
  params:
    batch_size: 500        # 每批写入条数
    flush_interval: 1s     # 强制刷新间隔
    wal_mode: true        # 启用 WAL 模式
    sync_mode: normal     # 同步模式
```

**BadgerDB**：
```yaml
storage:
  mode: badger
  params:
    batch_size: 1000           # 每批写入条数
    flush_interval: 1s         # 强制刷新间隔
    value_log_size: 67108864   # 64MB value log
    gc_interval: 5m            # GC 间隔
```

### 2. 内存限制

**Memory**：
```yaml
storage:
  mode: memory
  params:
    max_records: 1000000   # 最大记录数
    auto_cleanup: true     # 自动清理旧数据
```

### 3. 数据过滤

所有存储都支持按节点和任务过滤：

```go
// 查询特定节点的数据
results, _ := storage.Query(0, 100, statistics.StatusFilterAll, "node-1", "")

// 查询特定任务的数据
results, _ := storage.Query(0, 100, statistics.StatusFilterSuccess, "", "task-123")

// 查询特定节点的特定任务
results, _ := storage.Query(0, 100, statistics.StatusFilterFailed, "node-1", "task-123")
```

## 📈 监控与统计

### 获取存储统计信息

```go
stats := storage.GetStats()

fmt.Printf("存储类型: %s\n", stats["type"])
fmt.Printf("总记录数: %d\n", stats["total_count"])
fmt.Printf("成功数: %d\n", stats["success_count"])
fmt.Printf("失败数: %d\n", stats["failed_count"])

// BadgerDB 特有
if stats["type"] == "badger" {
    fmt.Printf("LSM 大小: %d\n", stats["lsm_size"])
    fmt.Printf("VLog 大小: %d\n", stats["vlog_size"])
}
```

### 实时监控

```bash
# 启动实时报告服务器
go-stress -config test.yaml -storage badger -realtime -realtime-port 8088

# 访问监控面板
curl http://localhost:8088/api/stats
```

## 🔄 存储迁移

### 从 SQLite 迁移到 BadgerDB

```go
package main

import (
    "github.com/kamalyes/go-stress/statistics"
)

func migrate() {
    factory := statistics.NewStorageFactory(logger)
    
    // 创建源存储（SQLite）
    source, _ := factory.CreateStorage(&statistics.StorageConfig{
        Type:   statistics.StorageTypeSQLite,
        Path:   "./old-data.db",
        NodeID: "migration",
    })
    
    // 创建目标存储（BadgerDB）
    target, _ := factory.CreateStorage(&statistics.StorageConfig{
        Type:   statistics.StorageTypeBadger,
        Path:   "./new-data",
        NodeID: "migration",
    })
    
    // 分批迁移数据
    offset := 0
    limit := 1000
    
    for {
        records, _ := source.Query(offset, limit, statistics.StatusFilterAll, "", "")
        if len(records) == 0 {
            break
        }
        
        for _, record := range records {
            target.Write(record)
        }
        
        offset += limit
        logger.Infof("已迁移 %d 条记录", offset)
    }
    
    source.Close()
    target.Close()
}
```

## 🛠️ 故障排查

### 1. BadgerDB 打开失败

**错误**：`Cannot acquire directory lock`

**解决**：确保没有其他进程占用数据目录

```bash
# 检查进程
lsof | grep badger

# 删除锁文件（谨慎）
rm -f ./stress-data/badger/LOCK
```

### 2. SQLite 写入慢

**优化**：启用 WAL 模式

```go
storage.db.Exec("PRAGMA journal_mode=WAL")
storage.db.Exec("PRAGMA synchronous=NORMAL")
storage.db.Exec("PRAGMA cache_size=-64000") // 64MB cache
```

### 3. 内存占用过高

**Memory 存储**：启用自动清理

```yaml
storage:
  mode: memory
  params:
    max_records: 500000
    auto_cleanup: true
```

**BadgerDB**：调整 GC 参数

```yaml
storage:
  mode: badger
  params:
    gc_interval: 1m
    gc_discard_ratio: 0.3  # 回收 30% 以上的空间
```

## 📝 最佳实践

1. **实时压测** → Memory 存储 + 定期导出报告
2. **单机压测** → SQLite 存储 + WAL 模式
3. **分布式压测** → BadgerDB 存储 + 节点隔离
4. **超大规模** → BadgerDB + 数据分片 + 定期清理
5. **监控数据** → 存储聚合指标，详情数据可选

## 🔗 相关文档

- [分布式模式配置](./DISTRIBUTED_MODE.md)
- [存储架构设计](./STORAGE_REPORT.md)
- [负载形态](./LOAD_PROFILE.md)
