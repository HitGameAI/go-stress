# 存储与报告

## 存储模式

支持三种存储模式，详细取舍见 [存储适配器](STORAGE_ADAPTERS.md)：

| 特性 | Memory | SQLite | BadgerDB |
|:-----|:-------|:-------|:---------|
| **速度** | 极快（纯内存） | 快（异步批量写入） | 很快（LSM-Tree） |
| **内存占用** | 随请求数增长 | 固定 | 固定 |
| **持久化** | ❌ | ✅ | ✅ |
| **查询方式** | 进程内 | SQL | 键前缀扫描 |
| **是否需要 cgo** | 否 | **是** | 否 |
| **适用场景** | 短时快速测试 | 需要事后 SQL 分析 | 高写入量、无 cgo 环境 |

### Memory 模式（默认）

```bash
# 默认使用内存模式
go-stress -url https://api.example.com -c 100 -n 10000

# 显式指定
go-stress -config config.yaml -storage memory
```

**特点**：
- 零配置，开箱即用
- 性能最优，无 I/O 开销
- 适合短时测试（< 100 万请求）

### SQLite 模式

```bash
go-stress -config config.yaml -storage sqlite
```

**特点**：
- 持久化到磁盘
- 固定内存占用
- 支持海量数据
- 异步批量写入

**优化措施**：
- WAL 模式：写入不阻塞读取
- 批量写入：每 100 条或 1 秒提交
- 异步写入：10000 条缓冲通道
- 索引优化：关键字段建立索引

## 相关文档

- [快速开始](GETTING_STARTED.md) - 基础使用
- [命令行参考](CLI_REFERENCE.md) - 存储相关参数
- [分布式压测](DISTRIBUTED_MODE.md) - 分布式报告汇总
