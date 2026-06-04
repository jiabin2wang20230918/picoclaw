package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/quantclaw/pkg/bus"
	"github.com/sipeed/quantclaw/pkg/config"
	"github.com/sipeed/quantclaw/pkg/providers"
	"github.com/sipeed/quantclaw/pkg/tools"
)

type concurrencyProbeTool struct {
	name      string
	policy    tools.ToolExecutionPolicy
	sleep     time.Duration
	active    *int32
	maxActive *int32
}

func (t *concurrencyProbeTool) Name() string        { return t.name }
func (t *concurrencyProbeTool) Description() string { return "probe tool" }
func (t *concurrencyProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *concurrencyProbeTool) ExecutionPolicy() tools.ToolExecutionPolicy { return t.policy }
func (t *concurrencyProbeTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	current := atomic.AddInt32(t.active, 1)
	for {
		max := atomic.LoadInt32(t.maxActive)
		if current <= max || atomic.CompareAndSwapInt32(t.maxActive, max, current) {
			break
		}
	}
	time.Sleep(t.sleep)
	atomic.AddInt32(t.active, -1)
	return tools.SilentResult(t.name + "_done")
}

func newToolHandlerForTest() *ToolHandler {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{},
		},
	}
	return NewToolHandler(cfg, bus.NewMessageBus())
}

func TestToolHandler_SerialPolicyRunsDeterministically(t *testing.T) {
	registry := tools.NewToolRegistry()
	var active int32
	var maxActive int32

	registry.Register(&concurrencyProbeTool{
		name:      "serial_one",
		policy:    tools.ToolExecutionSerial,
		sleep:     20 * time.Millisecond,
		active:    &active,
		maxActive: &maxActive,
	})
	registry.Register(&concurrencyProbeTool{
		name:      "serial_two",
		policy:    tools.ToolExecutionSerial,
		sleep:     20 * time.Millisecond,
		active:    &active,
		maxActive: &maxActive,
	})

	handler := newToolHandlerForTest()
	_, err := handler.ProcessToolCalls(context.Background(), registry, []providers.ToolCall{
		{ID: "1", Name: "serial_one", Arguments: map[string]any{}},
		{ID: "2", Name: "serial_two", Arguments: map[string]any{}},
	}, "", "", nil, "test-session")
	if err != nil {
		t.Fatalf("ProcessToolCalls failed: %v", err)
	}

	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("expected serial tools to run one at a time, max concurrency = %d", got)
	}
}

func TestToolHandler_ParallelSafePolicyRunsConcurrently(t *testing.T) {
	registry := tools.NewToolRegistry()
	var active int32
	var maxActive int32

	registry.Register(&concurrencyProbeTool{
		name:      "parallel_one",
		policy:    tools.ToolExecutionParallelSafe,
		sleep:     30 * time.Millisecond,
		active:    &active,
		maxActive: &maxActive,
	})
	registry.Register(&concurrencyProbeTool{
		name:      "parallel_two",
		policy:    tools.ToolExecutionParallelSafe,
		sleep:     30 * time.Millisecond,
		active:    &active,
		maxActive: &maxActive,
	})

	handler := newToolHandlerForTest()
	_, err := handler.ProcessToolCalls(context.Background(), registry, []providers.ToolCall{
		{ID: "1", Name: "parallel_one", Arguments: map[string]any{}},
		{ID: "2", Name: "parallel_two", Arguments: map[string]any{}},
	}, "", "", nil, "test-session")
	if err != nil {
		t.Fatalf("ProcessToolCalls failed: %v", err)
	}

	if got := atomic.LoadInt32(&maxActive); got < 2 {
		t.Fatalf("expected parallel-safe tools to overlap, max concurrency = %d", got)
	}
}

func TestToolHandler_MixedPoliciesPreserveResultOrder(t *testing.T) {
	registry := tools.NewToolRegistry()
	var active int32
	var maxActive int32
	var orderMu sync.Mutex
	var seen []string

	makeTool := func(name string, policy tools.ToolExecutionPolicy) *concurrencyProbeTool {
		return &concurrencyProbeTool{
			name:      name,
			policy:    policy,
			sleep:     10 * time.Millisecond,
			active:    &active,
			maxActive: &maxActive,
		}
	}

	registry.Register(makeTool("serial_before", tools.ToolExecutionSerial))
	registry.Register(makeTool("parallel_one", tools.ToolExecutionParallelSafe))
	registry.Register(makeTool("parallel_two", tools.ToolExecutionParallelSafe))

	handler := newToolHandlerForTest()
	results, err := handler.ProcessToolCalls(context.Background(), registry, []providers.ToolCall{
		{ID: "1", Name: "serial_before", Arguments: map[string]any{}},
		{ID: "2", Name: "parallel_one", Arguments: map[string]any{}},
		{ID: "3", Name: "parallel_two", Arguments: map[string]any{}},
	}, "", "", func(progress float64, message string, metadata map[string]interface{}) error {
		orderMu.Lock()
		defer orderMu.Unlock()
		seen = append(seen, metadata["tool_name"].(string))
		return nil
	}, "test-session")
	if err != nil {
		t.Fatalf("ProcessToolCalls failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].ToolCallID != "1" || results[1].ToolCallID != "2" || results[2].ToolCallID != "3" {
		t.Fatalf("expected result order to match tool call order, got %+v", results)
	}
	if len(seen) != 0 {
		t.Fatalf("expected no progress callbacks when SendToolHints is disabled, got %v", seen)
	}
}
