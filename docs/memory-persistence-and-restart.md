# PicoClaw Memory System: 数据持久化和重启支持

## 概述

本文档说明了PicoClaw记忆系统的数据持久化和重启功能。系统现在具备以下能力：

1. **自动迁移传统数据**：首次启动时自动将现有文件系统数据迁移到新系统
2. **数据持久化**：数据存储在SQLite数据库中，确保重启后数据保留
3. **重启后数据加载**：系统重启后可以正确加载之前保存的数据
4. **向后兼容**：与传统PicoClaw数据格式兼容

## 数据存储结构

### 传统格式
```
workspace/
├── MEMORY.md                 # 长期记忆
└── memory/
    ├── YYYYMM/              # 按月组织
    │   ├── YYYYMMDD.md      # 每日笔记
    │   └── ...
    └── ...
```

### 新格式（生命周期模型）
```
workspace/
├── MEMORY.md                 # 长期记忆（用于迁移）
├── memory.db                 # SQLite数据库（新存储方式）
├── memory/
│   └── YYYY-MM/             # 按月组织（新格式）
│       └── YYYY-MM-DD.md    # 每日笔记（新格式）
└── archive/
    └── YYYY-MM/
        └── highlights_YYYY-MM-DD.md  # 归档和精华提取
```

## 自动迁移功能

### 触发条件
- 首次创建 `MemoryManager` 实例时
- 检测到存在传统格式文件且新数据库中尚不存在对应数据

### 迁移过程
1. **MEMORY.md迁移**：
   - 检查 `workspace/MEMORY.md` 是否存在
   - 如果新系统中不存在同名数据，则导入并设置0.85置信度

2. **每日笔记迁移**：
   - 检查过去30天的 `workspace/memory/YYYYMM/YYYYMMDD.md` 文件
   - 按日期顺序导入到新系统（格式转换为 `memory/YYYY-MM-DD.md`）
   - 根据日期设置置信度（较新笔记置信度较高）

### 文件格式转换
- **传统**：`memory/202603/20260308.md`
- **新格式**：`memory/2026-03-08.md`

## 重启数据加载

### 启动流程
```
1. NewMemoryManager(workspace)
2.   ↓
3. 检查并迁移传统数据 (migrateFromLegacyStorage)
4.   ↓
5. 初始化数据库连接
6.   ↓
7. 启动计划任务 (每日维护等)
8.   ↓
9. 系统就绪，数据可访问
```

### 数据恢复机制
- 所有数据存储在 `memory.db` SQLite数据库中
- 重启后自动重新建立数据库连接
- 所有记忆项目、置信度、关系保持不变

## API 接口兼容性

### 传统API（保持向后兼容）
```go
// 保持原有接口不变
ReadLongTerm() string                    // 读取长期记忆
WriteLongTerm(content string) error      // 写入长期记忆
ReadToday() string                       // 读取今日笔记
AppendToday(content string) error        // 追加今日笔记
```

### 新API（生命周期模型）
```go
// 新增的生命周期查询接口
GetSproutItems(limit int) ([]string, []float64, error)
GetGreenLeafItems(limit int) ([]string, []float64, error)
GetYellowLeafItems(limit int) ([]string, []float64, error)
GetDeadLeafItems(limit int) ([]string, []float64, error)
GetSoilItems(limit int) ([]string, []float64, error)
```

## 测试验证

### 迁移测试
- ✅ 传统 `MEMORY.md` 成功迁移
- ✅ 每日笔记成功迁移（格式转换正确）
- ✅ 置信度正确设置

### 持久化测试
- ✅ 数据在重启后保留
- ✅ 数据库连接正常恢复
- ✅ 记忆状态（置信度、关系等）保持不变

### 功能测试
- ✅ 读写操作正常
- ✅ 搜索功能正常
- ✅ 维护任务正常执行

## 注意事项

1. **首次启动**：第一次运行新系统会自动迁移现有数据
2. **数据安全**：迁移过程中不会删除原始文件
3. **性能优化**：数据存储在数据库中，访问更快
4. **扩展性**：支持未来更多的记忆管理和分析功能

## 最佳实践

### 对于用户
- 现有数据会自动迁移，无需额外操作
- 重启后所有记忆数据保持完整
- 可以使用新的生命周期查询功能获取更精确的结果

### 对于开发者
- 使用 `MemoryManager` 初始化，自动处理迁移
- 通过 `Get*Items` 方法按生命周期状态查询
- 利用置信度机制进行智能记忆管理