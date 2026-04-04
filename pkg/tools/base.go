package tools

import "context"

// Tool is the interface that all tools must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) *ToolResult
}

// ContextualTool is an optional interface that tools can implement
// to receive the current message context (channel, chatID)
type ContextualTool interface {
	Tool
	SetContext(channel, chatID string)
}

// AsyncCallback is a function type that async tools use to notify completion.
// When an async tool finishes its work, it calls this callback with the result.
//
// The ctx parameter allows the callback to be canceled if the agent is shutting down.
// The result parameter contains the tool's execution result.
//
// Example usage in an async tool:
//
//	func (t *MyAsyncTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
//	    // Start async work in background
//	    go func() {
//	        result := doAsyncWork()
//	        if t.callback != nil {
//	            t.callback(ctx, result)
//	        }
//	    }()
//	    return AsyncResult("Async task started")
//	}
type AsyncCallback func(ctx context.Context, result *ToolResult)

// AsyncTool is an optional interface that tools can implement to support
// asynchronous execution with completion callbacks.
//
// Async tools return immediately with an AsyncResult, then notify completion
// via the callback set by SetCallback.
//
// This is useful for:
// - Long-running operations that shouldn't block the agent loop
// - Subagent spawns that complete independently
// - Background tasks that need to report results later
//
// Example:
//
//	type SpawnTool struct {
//	    callback AsyncCallback
//	}
//
//	func (t *SpawnTool) SetCallback(cb AsyncCallback) {
//	    t.callback = cb
//	}
//
//	func (t *SpawnTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
//	    go t.runSubagent(ctx, args)
//	    return AsyncResult("Subagent spawned, will report back")
//	}
type AsyncTool interface {
	Tool
	// SetCallback registers a callback function to be invoked when the async operation completes.
	// The callback will be called from a goroutine and should handle thread-safety if needed.
	SetCallback(cb AsyncCallback)
}

// ValidatingTool is an optional interface that tools can implement to validate
// their arguments before execution. When validation fails, the ToolHandler
// returns an error tool_result so the model can self-correct and retry,
// rather than crashing or silently passing bad arguments to the tool.
type ValidatingTool interface {
	Tool
	ValidateArgs(args map[string]any) error
}

// ExecutionPolicyAware allows a tool to override the default documented
// execution policy used by the agent runtime.
type ExecutionPolicyAware interface {
	Tool
	ExecutionPolicy() ToolExecutionPolicy
}

// ToolExecutionPolicy documents whether a tool is safe to run in parallel with
// sibling tool calls from the same model turn. Phase 1 keeps runtime behavior
// unchanged, but makes execution intent explicit in code for future refactors.
type ToolExecutionPolicy string

const (
	ToolExecutionSerial       ToolExecutionPolicy = "serial"
	ToolExecutionParallelSafe ToolExecutionPolicy = "parallel_safe"
)

// DefaultExecutionPolicy returns the documented execution policy for built-in tools.
// Unknown tools default to serial semantics.
func DefaultExecutionPolicy(toolName string) ToolExecutionPolicy {
	switch toolName {
	case "read_file",
		"list_dir",
		"find_skills",
		"web_search",
		"web_fetch",
		"browser_get_text",
		"browser_screenshot":
		return ToolExecutionParallelSafe
	case "write_file",
		"edit_file",
		"append_file",
		"exec",
		"message",
		"i2c",
		"spi",
		"install_skill",
		"cron",
		"subagent",
		"spawn",
		"browser_navigate",
		"browser_click",
		"browser_fill_input",
		"browser_execute_script":
		return ToolExecutionSerial
	default:
		return ToolExecutionSerial
	}
}

func ToolToSchema(tool Tool) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		},
	}
}
