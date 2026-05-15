# PicoClaw Refactor Roadmap

This roadmap is based on the current runtime architecture in the repository today.

It focuses on reducing structural risk around:

- context assembly
- `AgentLoop` complexity
- tool execution semantics
- session and memory consistency
- provider inference policy

The goal is not a full rewrite. The goal is to stabilize the current production path while preserving momentum.

## Refactor Principles

1. Keep the existing runtime working while refactoring the internals.
2. Prefer narrowing responsibilities over introducing more abstraction layers.
3. Move one responsibility at a time out of `AgentLoop`.
4. Preserve compatibility for existing workspaces, sessions, tools, and channel integrations.
5. Add tests around behavior before changing policy-heavy paths.

## Current Problem Statement

The current system is workable, but the production path has accumulated too many responsibilities in one place.

The highest-risk issues are:

- `AgentLoop` is the effective god object
- context construction has multiple overlapping paths
- session access has two paths (`agent.Sessions` and `al.sessionManager`)
- tool calls are executed concurrently by default even when order may matter
- context compression and retry behavior are spread across multiple fallback code paths
- memory usage is text-heavy rather than structured
- experimental subpackages exist but are not the default runtime path

## Phase 1: 1 Week

These tasks are low-to-medium risk and should be done first because they reduce ambiguity without changing the external product model.

### 1. Unify Session Access

Problem:

- The loop sometimes uses `al.sessionManager` and sometimes uses `agent.Sessions`.
- This creates drift risk and makes behavior harder to reason about.

Actions:

- Make `AgentLoop` depend on one session interface only.
- Route all session reads/writes through `pkg/agent/session_manager.go`.
- Remove direct `agent.Sessions` usage from the loop.

Success criteria:

- No direct session storage calls remain inside `pkg/agent/loop.go`.
- Session read/write behavior is unchanged from the user's perspective.

### 2. Define One Context Assembly Path

Problem:

- `ContextBuilder` and `MessageBuilder` both participate in building provider messages.
- The system prompt and runtime message composition are split across overlapping helpers.

Actions:

- Introduce a single internal `ContextAssembler` or equivalent struct.
- Make it responsible for building the final provider message array.
- Keep `ContextBuilder` only if it becomes a pure bootstrap/system-prompt helper.
- Otherwise merge it into the new assembler.

Success criteria:

- There is one authoritative code path for provider message construction.
- All provider calls use that same path.

### 3. Document Tool Execution Semantics

Problem:

- Tools are currently executed concurrently by default.
- The code does not clearly encode which tools are safe to parallelize.

Actions:

- Add explicit tool execution policy documentation in code and docs.
- Introduce a minimal tool metadata flag such as:
  - `Serial`
  - `ParallelSafe`
- Do not change runtime behavior yet unless a tool is clearly unsafe.

Success criteria:

- Every built-in tool has an explicit execution policy.
- Future changes no longer rely on implicit assumptions.

### 4. Add Baseline Regression Tests Around AgentLoop

Problem:

- Refactoring policy-heavy runtime code without behavioral tests is too risky.

Actions:

- Add integration-oriented tests covering:
  - normal single-turn response
  - tool call followed by tool result
  - session reload after tool use
  - context overflow compression path
  - truncated output continuation path

Success criteria:

- The current loop behavior is reproducible in tests before deeper refactors begin.

## Phase 2: 2-4 Weeks

These changes reduce long-term maintenance cost and move policy out of the giant loop file.

### 5. Extract an Inference Service

Problem:

- Provider call, fallback, retry, overflow handling, and continuation logic are mixed into `AgentLoop`.

Actions:

- Create an `InferenceService` responsible for:
  - provider invocation
  - fallback chain handling
  - context-overflow retry
  - output continuation
- Keep `AgentLoop` as the orchestrator, not the policy container.

Success criteria:

- `AgentLoop` no longer contains direct provider retry/fallback state logic.
- Inference behavior is testable independently.

### 6. Introduce a Context Budget Manager

Problem:

- Compression, pruning, retry-after-overflow, and memory flush are split across multiple recovery paths.

Actions:

- Create a `ContextBudgetManager` responsible for:
  - token estimation
  - preflight budget checks
  - selecting compression strategy
  - deciding whether to collapse, summarize, prune, or retry
- Run this before provider invocation instead of treating overflow mainly as an exception path.

Success criteria:

- Context management policy is centralized.
- Overflow becomes an edge case rather than the main control mechanism.

### 7. Change Tool Execution Default From Parallel To Policy-Driven

Problem:

- Default parallel execution is unsafe for tools with order dependencies or side effects.

Actions:

- Make tool execution respect per-tool policy.
- Default unknown tools to serial execution.
- Allow explicit parallel execution only for tools marked safe.

Suggested initial categorization:

- Serial:
  - `write_file`
  - `append_file`
  - `edit_file`
  - `exec`
  - `spawn`
  - `cron`
- Parallel-safe:
  - `read_file`
  - `list_dir`
  - `web_search`
  - `web_fetch`
  - browser read-only operations where applicable

Success criteria:

- Tool execution order is deterministic where it matters.
- Parallelism remains available for safe workloads.

### 8. Stabilize the Runtime Surface

Problem:

- The repo contains packages that look like the intended architecture, but the runtime still centers on `AgentLoop`.

Actions:

- Decide which packages are:
  - active production path
  - internal support code
  - experimental/future refactor targets
- Reflect that in package comments and docs.
- Avoid keeping multiple equivalent runtime paths alive.

Success criteria:

- A new engineer can tell which code path is real within minutes.
- The architecture docs match the runtime.

## Phase 3: 1-2 Months

These are deeper design improvements that should only happen after the runtime has been stabilized.

### 9. Make Memory Structured Instead of Mostly Textual

Problem:

- Relevant memory is injected as formatted text.
- Sedimentation mostly stores textual observations rather than typed memory records.

Actions:

- Introduce memory record types such as:
  - `fact`
  - `preference`
  - `artifact`
  - `tool_observation`
  - `conversation_summary`
- Make retrieval return structured items with:
  - source key
  - confidence
  - recency
  - memory type
- Render them to prompt text only at the final boundary.

Success criteria:

- Memory ranking and injection policy become explainable and tunable.
- Sedimentation quality improves over time instead of becoming a text dump.

### 10. Introduce a Workspace Context Manifest

Problem:

- Bootstrap context still depends on hardcoded filenames and conventions.

Actions:

- Add a manifest such as `workspace/context.json` or similar.
- Define:
  - bootstrap file order
  - optional vs required files
  - future extensibility for project-specific context files
- Preserve current filename convention as a fallback for compatibility.

Success criteria:

- Context bootstrapping no longer depends entirely on file naming conventions.
- Workspace evolution becomes safer.

### 11. Separate Runtime State From Persistent Knowledge More Cleanly

Problem:

- Session state, memory sedimentation, channel state, and operational artifacts are conceptually distinct but still loosely mixed.

Actions:

- Clarify system boundaries:
  - session history = short-term conversational state
  - state store = operational runtime state
  - memory store = durable knowledge
  - workspace files = user-controlled artifacts
- Make these categories explicit in code comments and APIs.

Success criteria:

- Persistence decisions become more consistent.
- Future features do not overload one storage mechanism for every purpose.

## High-Risk / Defer Until Later

These ideas may be worthwhile, but should not be attempted until the earlier phases are complete.

### A. Full Orchestrator Migration

Do not move the production runtime wholesale onto `pkg/agent/orchestrator/*` until:

- session access is unified
- context assembly is unified
- inference policy is extracted
- tool execution policy is stabilized

Reason:

- Doing this too early creates a second unstable runtime instead of improving the first one.

### B. Fine-Grained Tool Permissions System

Do not prioritize per-agent/per-channel permissions in the main runtime yet.

Reason:

- The system's bigger problem today is runtime complexity and consistency, not the absence of a permission matrix.
- A permission layer added too early will harden the wrong abstractions.

### C. Large-Scale Pluginization Of Core Runtime

Do not convert core runtime responsibilities into a plugin system before the execution model is stable.

Reason:

- Pluginizing unstable boundaries locks in accidental design decisions.

## Recommended Execution Order

Recommended order of implementation:

1. Unify session access
2. Unify context assembly
3. Add regression tests around current loop behavior
4. Document tool execution policies
5. Extract inference service
6. Introduce context budget manager
7. Make tool execution policy-driven
8. Clean up runtime surface and architecture ownership
9. Structure memory records
10. Add workspace context manifest

## Suggested Ownership Split

If multiple engineers work on this roadmap, split by write scope:

- Engineer 1:
  - `pkg/agent/loop.go`
  - `pkg/agent/session_manager.go`
  - `pkg/agent/message_builder.go`
  - `pkg/agent/context.go`
- Engineer 2:
  - `pkg/agent/tool_handler.go`
  - `pkg/tools/*`
  - tool execution policy metadata
- Engineer 3:
  - `pkg/providers/*`
  - fallback/retry extraction
  - inference service
- Engineer 4:
  - `pkg/agent/memory/*`
  - memory record structure
  - retrieval/rendering boundary

## Definition Of Done

This roadmap is complete when:

- `AgentLoop` is primarily orchestration, not policy aggregation
- provider message construction has one authoritative path
- session storage has one authoritative access path
- tool execution order is explicit and deterministic
- context budget behavior is centralized
- architecture docs match the actual runtime

At that point PicoClaw will still feel like the same product, but it will be much easier to evolve safely.
