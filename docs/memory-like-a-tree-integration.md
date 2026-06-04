# QuantClaw Memory-Like-A-Tree 集成指南

## 系统集成概述

本指南描述了如何将 Memory-Like-A-Tree 架构集成到 QuantClaw 的现有系统中，同时保持与现有功能的兼容性。

## 集成架构

```
┌─────────────────┐    ┌─────────────────────┐
│   QuantClaw      │    │ Memory-Like-A-Tree  │
│   主系统        │◄──►│      系统           │
└─────────────────┘    └─────────────────────┘
       │                       │
       ▼                       ▼
┌─────────────────┐    ┌─────────────────────┐
│   会话管理      │    │  核心记忆管理       │
│   (sessions)    │    │  (MemoryCore)      │
└─────────────────┘    └─────────────────────┘
       │                       │
       ▼                       ▼
┌─────────────────┐    ┌─────────────────────┐
│   消息历史      │    │  索引与搜索         │
│   (history)     │    │  (Indexer/Searcher) │
└─────────────────┘    └─────────────────────┘
       │                       │
       ▼                       ▼
┌─────────────────┐    ┌─────────────────────┐
│   上下文构建    │    │  知识沉淀与维护     │
│   (context)     │    │(Sedimenter/Maintainer)│
└─────────────────┘    └─────────────────────┘
```

## 集成步骤

### 1. 初始化 Memory Manager

```go
// 在 QuantClaw 启动时初始化 Memory-Like-A-Tree 系统
func initMemorySystem(workspace string) (*memory.MemoryManager, error) {
    memoryManager, err := memory.NewMemoryManager(workspace)
    if err != nil {
        return nil, fmt.Errorf("failed to initialize Memory-Like-A-Tree system: %v", err)
    }

    return memoryManager, nil
}
```

### 2. 与会话系统集成

```go
// 将会话历史保存到 Memory-Like-A-Tree
func saveSessionToMemory(sessionKey string, messages []Message, memoryManager *memory.MemoryManager) error {
    for i, msg := range messages {
        key := fmt.Sprintf("session:%s:message:%d", sessionKey, i)
        content := fmt.Sprintf("[%s] %s", msg.Role, msg.Content)

        if err := memoryManager.Save(key, content); err != nil {
            return err
        }
    }

    // 提取会话中的关键知识
    conversationContent := combineMessages(messages)
    return memoryManager.SedimentKnowledge(conversationContent, sessionKey)
}
```

### 3. 智能上下文构建

```go
// 基于 Memory-Like-A-Tree 构建智能上下文
func buildContextWithMemory(query string, memoryManager *memory.MemoryManager) (string, error) {
    var contextBuilder strings.Builder

    // 1. 获取直接相关的记忆
    directResults, err := memoryManager.Search(query, 5)
    if err != nil {
        return "", err
    }

    if len(directResults) > 0 {
        contextBuilder.WriteString("## 相关记忆:\n")
        for _, result := range directResults {
            contextBuilder.WriteString(fmt.Sprintf("- %s: %s\n", result.Key, result.Content))
        }
        contextBuilder.WriteString("\n")
    }

    // 2. 获取高置信度的长期知识
    highConfKeys, _, err := memoryManager.GetHighConfidenceItems(0.7, 3)
    if err != nil {
        return "", err
    }

    if len(highConfKeys) > 0 {
        contextBuilder.WriteString("## 长期知识:\n")
        for _, key := range highConfKeys {
            content, err := memoryManager.Read(key)
            if err != nil {
                continue
            }
            contextBuilder.WriteString(fmt.Sprintf("- %s: %s\n", key, content))
        }
        contextBuilder.WriteString("\n")
    }

    return contextBuilder.String(), nil
}
```

### 4. 与现有API兼容

为了保持向后兼容，MemoryManager 提供了与原系统相同的接口：

```go
// 保持与传统 API 的兼容性
func (mm *MemoryManager) ReadLongTerm() string {
    content, err := mm.Read("MEMORY.md")
    if err != nil {
        return ""
    }
    return content
}

func (mm *MemoryManager) WriteLongTerm(content string) error {
    return mm.Save("MEMORY.md", content)
}

func (mm *MemoryManager) AppendToday(content string) error {
    todayKey := fmt.Sprintf("memory/%s.md", time.Now().Format("2006-01-02"))

    existingContent, err := mm.Read(todayKey)
    if err != nil {
        existingContent = fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
    }

    updatedContent := existingContent + "\n" + content
    return mm.Save(todayKey, updatedContent)
}
```

## 配置集成

### 1. 添加到配置文件

```json
{
  "memory": {
    "enabled": true,
    "tree_enabled": true,
    "decay_rate": 0.05,
    "cleanup_threshold": 0.2,
    "archive_enabled": true,
    "confidence_boost_on_access": 0.05
  }
}
```

### 2. 配置加载

```go
// 在配置系统中加载 Memory-Like-A-Tree 设置
type MemoryConfig struct {
    Enabled              bool    `json:"enabled"`
    TreeEnabled          bool    `json:"tree_enabled"`
    DecayRate            float64 `json:"decay_rate"`
    CleanupThreshold     float64 `json:"cleanup_threshold"`
    ArchiveEnabled       bool    `json:"archive_enabled"`
    ConfidenceBoostOnAccess float64 `json:"confidence_boost_on_access"`
}
```

## 维护任务集成

Memory-Like-A-Tree 系统提供了自动维护功能，可以集成到 QuantClaw 的定期任务中：

```go
// 启动定时维护任务
func startMemoryMaintenance(memoryManager *memory.MemoryManager) {
    // 开始每日维护任务
    memoryManager.StartScheduledTasks()

    // 也可以手动触发维护
    go func() {
        ticker := time.NewTicker(24 * time.Hour) // 每天运行一次
        defer ticker.Stop()

        for range ticker.C {
            if err := memoryManager.RunPeriodicMaintenance(); err != nil {
                log.Printf("Memory maintenance error: %v", err)
            }
        }
    }()
}
```

## 性能监控

```go
// 获取内存系统统计信息
func monitorMemoryPerformance(memoryManager *memory.MemoryManager) {
    stats, err := memoryManager.GetCurrentStats()
    if err != nil {
        log.Printf("Failed to get memory stats: %v", err)
        return
    }

    log.Printf("Memory Stats - Total: %d, High Confidence: %d, Size: %d bytes",
        stats["total_items"],
        stats["high_confidence_items"],
        stats["estimated_size_bytes"])
}
```

## 错误处理与回退

为了确保系统的稳定性，实现错误处理和回退机制：

```go
// 带有错误处理的内存操作
func safeMemoryOperation(memoryManager *memory.MemoryManager, operation func() error) error {
    err := operation()
    if err != nil {
        log.Printf("Memory operation failed: %v", err)
        // 可以在这里实现回退到传统存储的逻辑
        return err
    }
    return nil
}
```

## 迁移策略

对于现有的 QuantClaw 用户，可以实现平滑迁移：

1. **双重写入阶段**：同时写入旧系统和新系统
2. **逐步迁移**：将历史数据迁移到新系统
3. **切换阶段**：切换到主要使用新系统
4. **清理阶段**：移除旧系统依赖

## 最佳实践

1. **渐进式集成**：先在非关键路径上使用新系统
2. **监控指标**：跟踪性能改进和稳定性
3. **用户透明**：对用户保持一致性体验
4. **备份策略**：确保迁移过程的安全性

通过这种集成方法，QuantClaw 能够充分利用 Memory-Like-A-Tree 架构的强大功能，同时保持与现有系统的兼容性，实现无缝过渡。