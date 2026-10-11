// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Package engine implements the pipeline execution engine — the orchestrator
// that receives a PipelineConfig and executes it: loops → steps → nodes → LLM
// calls → recursion.
// Ported from packages/engine-core.
package engine

import (
	"context"
	"sync"

	"github.com/ladr/ladr/internal/device"
	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/mailbox"
	"github.com/ladr/ladr/internal/relik"
)

// ProviderConfig describes an upstream LLM provider + model pair.
type ProviderConfig struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	APIKey      string  `json:"apiKey,omitempty"`
	BaseURL     string  `json:"baseURL,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	MaxTokens   int     `json:"maxTokens,omitempty"`
	TimeoutMs   int     `json:"timeoutMs,omitempty"`
}

// ExecutionRuntimeContext carries per-execution state through the pipeline.
// PrunedNodeMap is a concurrency-safe map of pruned node survivors.
// Multiple group children can write to it simultaneously.
type PrunedNodeMap struct {
	mu   sync.RWMutex
	data map[string][]int
}

// NewPrunedNodeMap creates an empty PrunedNodeMap.
func NewPrunedNodeMap() *PrunedNodeMap {
	return &PrunedNodeMap{data: make(map[string][]int)}
}

// Set stores the survivor list for a loop/step key.
func (p *PrunedNodeMap) Set(key string, survivors []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data[key] = survivors
}

// Get retrieves the survivor list for a key.
func (p *PrunedNodeMap) Get(key string) ([]int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.data[key]
	return v, ok
}

type ExecutionRuntimeContext struct {
	InstanceID        string
	LoopIndex         int
	Depth             int
	CurrentStepID     string
	CurrentGroupID    string
	CurrentNodeNumber *int
	PrunedNodeMap     *PrunedNodeMap
	Inputs            map[string]any
	Knobs             map[string]float64

	// Config is the ladder being executed. Available for manifest resolution.
	Config *ladr.PipelineConfig

	// Phase 1 extensions — optional, set by ExecutePipeline when available.
	RelikStore     relik.Store
	CeilingTracker *CeilingTracker

	// Owner/Session address the run's memory scopes (indexer search scopes
	// by owner; refs record the session). Set by the run wiring.
	OwnerID    string
	SessionID  string
	LadderName string

	// Mailbox is the run's handle for program-addressed messaging: drains
	// ({mailbox: true} sources, run-start drain) and posts (mail: blocks).
	Mailbox mailbox.Store

	// ConfigID addresses the stored ladder config (configEdit target).
	ConfigID string
	// ConfigEditor applies self-edits with version history (configEdit).
	// Wired by the run layer; nil on bare local runs.
	ConfigEditor ConfigEditor
	PendingJump    string // set by gate evaluation when a within-ladder jump action fires
	LastStepID     string // ID of the most recently executed step
	LastStepOutput string // output of the most recently executed step

	// Cross-ladder handoff (Phase 2). When a step's gate fires a cross-ladder
	// jump, PendingHandoff is populated and the pipeline loop terminates on
	// the next iteration, calling io.Handoff with FiringOutput = LastStepOutput.
	PendingHandoff *ladr.JumpTarget

	// passField overrides (Phase 3). When a jump (intra- or cross-ladder) fires
	// with passFields, the engine resolves each passField.from in the CALLER's
	// context and stores the result here keyed by target field name. When the
	// TARGET step's ResolveField runs, it checks PassFieldOverrides first —
	// if the field name is present, the override value wins outright (highest
	// priority in the resolution order: passField > from > fallback).
	//
	// The map is reset at every step boundary — overrides apply to ONE target
	// step only, then are cleared.
	PassFieldOverrides map[string]string

	// PendingPassFields is the unresolved passField list captured from a gate
	// outcome. Pipeline loop consumes this when repositioning the cursor,
	// resolving each PassField.From in the CALLER's runtime and writing the
	// resolved values into PassFieldOverrides for the target step.
	PendingPassFields []ladr.PassField
}

// CallUsage holds the token usage from a single LLM call. The engine fills in
// the UsageOut pointer on ExecuteLlmParams after the call completes so the
// caller (makeLLMCall in step.go) can calculate spend.
type CallUsage struct {
	PromptTokens     int
	CompletionTokens int
}

// ExecuteLlmParams holds the parameters for a single LLM call.
type ExecuteLlmParams struct {
	InstanceID        string
	StepID            string
	GroupID           string
	LoopIndex         int
	NodeNumber        *int
	UserPrompt        string
	Provider          ProviderConfig
	FallbackProviders []ProviderConfig
	Depth             int

	// UsageOut is an optional out-parameter. If non-nil, the engine fills it
	// with the token usage from the completed call. Used for spend tracking.
	UsageOut *CallUsage
}

// ExecuteCodeParams holds the parameters for a single code-execution tool call
// (the tools.codeExecution tool). SessionID-equivalent fields (InstanceID +
// StepID + LoopIndex + NodeNumber + Depth) identify the persistent interpreter
// session so REPL state/files persist across a tool loop's turns.
type ExecuteCodeParams struct {
	InstanceID string
	StepID     string
	GroupID    string
	LoopIndex  int
	NodeNumber *int
	Depth      int
	Runtime    string // python | node | bash (validated by grammar)
	Code       string
	TimeoutMs  int    // per-call wall-clock cap; 0 → executor default
	LadderID   string // ladder attribution (device-side per-ladder grant checks)
	Cwd        string // requested working dir (a device-side granted root; "" → session default)

	OwnerID string // device-routing key for federated execution (the instance owner)

	// Terminal lifecycle (v2) — codeExecution IS terminal access. Terminal
	// names the device-side terminal ("" → the ladder's default); Background
	// starts a long-running command (immediate receipt, output accumulates in
	// the terminal's scrollback); Action is "peek" | "kill" | "status" on the
	// terminal itself (Code empty). Federated path only — local runners
	// approximate Terminal as the session key and reject Background/Action.
	Terminal   string
	Background bool
	Action     string
}

// ExecuteCodeResult holds the outcome of a code-execution call. Output is the
// trimmed, prompt-injectable string the tool loop injects as the `as:` field.
type ExecuteCodeResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Output   string

	// Terminal lifecycle fields (v2) — populated for background spawns and
	// terminal actions; zero values on plain foreground runs.
	Terminal  string
	Running   bool
	Truncated bool
	Terminals []device.TerminalStatus
	Notice    string
}

// FilesOpParams holds the parameters for a single files: tool call. The
// coordinate fields identify the session workspace so files: and
// codeExecution: on one step share the same directory. Paths are relative;
// the IO implementation resolves them against its scope (a device-side
// granted folder on hosted runs, the session workspace on local runs).
type FilesOpParams struct {
	InstanceID string
	StepID     string
	GroupID    string
	LoopIndex  int
	NodeNumber *int
	Depth      int
	Op       string // "read" | "write" | "list"
	Path     string
	Content  string // write only
	Glob     string // list only (optional)
	Append   bool   // write only
	LadderID string // ladder attribution (device-side per-ladder grant checks)
}

// FilesOpResult holds the outcome of a files: tool call. Which fields are set
// depends on Op: read → Content/Truncated, write → Bytes, list → Entries.
type FilesOpResult struct {
	Content   string
	Entries   []string
	Truncated bool
	Bytes     int
}

// SourceRef identifies a specific node output by loop/step/node coordinates.
type SourceRef struct {
	LoopIndex  int
	StepID     string
	GroupID    string
	NodeNumber *int
}

// EventType enumerates the event types the engine publishes.
type EventType string

const (
	EventPipelineStart     EventType = "pipeline_start"
	EventLoopStart         EventType = "loop_start"
	EventStepStart         EventType = "step_start"
	EventStepComplete      EventType = "step_complete"
	EventStepPaused        EventType = "step_paused"
	EventLoopComplete      EventType = "loop_complete"
	EventRecursionStart    EventType = "recursion_start"
	EventRecursionComplete EventType = "recursion_complete"
	EventHandoffStart      EventType = "handoff_start"
	EventHandoffComplete   EventType = "handoff_complete"
	EventPipelineComplete  EventType = "pipeline_complete"
	EventPipelineResumed   EventType = "pipeline_resumed"
	EventNodeCountResolved EventType = "node_count_resolved"
	EventNodeSurvivors     EventType = "node_survivors"
	EventNodeFailed        EventType = "node_failed"
	EventToolCall          EventType = "tool_call"
	EventToolResult        EventType = "tool_result"
	EventError             EventType = "error"
)

// ReadOutputArgs holds the coordinates for reading a node output.
type ReadOutputArgs struct {
	InstanceID string
	LoopIndex  int
	StepID     string
	GroupID    string
	NodeNumber *int
}

// ReadOutputArrayArgs holds the coordinates for batch-reading node outputs.
type ReadOutputArrayArgs struct {
	InstanceID  string
	FromSources []SourceRef
}

// WriteOutputArgs holds the coordinates and content for writing a node output.
type WriteOutputArgs struct {
	InstanceID string
	LoopIndex  int
	StepID     string
	GroupID    string
	NodeNumber *int
	Output     string
}

// PublishEventArgs holds the parameters for publishing an engine event.
type PublishEventArgs struct {
	InstanceID string
	Type       EventType
	Data       map[string]any
}

// RecurseArgs holds the parameters for a recursion call.
type RecurseArgs struct {
	ParentInstanceID  string
	StepID            string
	Context           string
	Depth             int
	Knobs             map[string]float64
	Config            *ladr.PipelineConfig
	Provider          ProviderConfig
	FallbackProviders []ProviderConfig
}

// HandoffArgs holds the parameters for a cross-ladder handoff (tail-call).
//
// The firing step's combined output becomes the child ladder's input.context.
// The current execution terminates; the child's exit-step output becomes the
// user-visible result. Budget ceilings and provider chain carry over to the child.
type HandoffArgs struct {
	ParentInstanceID  string
	FromStepID        string          // step in the parent that fired the handoff
	FiringOutput      string          // combined output of the firing step → child's input.context
	Target            ladr.JumpTarget // { ladderId, stepId? }
	Knobs             map[string]float64
	Provider          ProviderConfig
	FallbackProviders []ProviderConfig

	// Phase 3: resolved passField overrides to ship to the child. The child's
	// target step will consult this map in its ResolveField before falling
	// through to its declared from:/fallback:.
	PassFieldOverrides map[string]string
}

// HandoffResult is the outcome of a cross-ladder handoff.
type HandoffResult struct {
	ChildInstanceID string // the spawned child instance ID (for trace linking)
	ExitOutput      string // the child's exit-step output — becomes the parent's final result
}

// LadderResolver fetches a target ladder config by ID. Implemented by the
// redis-backed engine (DB lookup for @author/name; local file lookup for local/).
// Gateway already has this capability via db.GetPipelineConfigByAuthorAndName;
// the engine needs its own resolver to fetch target configs mid-execution.
type LadderResolver interface {
	// ResolveLadder returns the parsed config + the resolved provider chain
	// for the target ladder. Returns an error if the ladder cannot be found.
	ResolveLadder(ctx context.Context, target ladr.JumpTarget) (*ladr.PipelineConfig, error)
}

// GetNodeCountArgs holds the coordinates for querying how many nodes a step has.
type GetNodeCountArgs struct {
	InstanceID string
	LoopIndex  int
	StepID     string
	GroupID    string
}

// EngineIO defines all the I/O operations the engine needs (typically backed
// by Redis).  Every method takes context.Context as its first argument for
// cancellation and timeout control.
type EngineIO interface {
	ReadOutput(ctx context.Context, args ReadOutputArgs) (string, error)
	ReadOutputArray(ctx context.Context, args ReadOutputArrayArgs) ([]string, error)
	WriteOutput(ctx context.Context, args WriteOutputArgs) error
	PublishEvent(ctx context.Context, args PublishEventArgs) error
	ExecuteLlmCall(ctx context.Context, args ExecuteLlmParams) (string, error)
	// ExecuteCode runs LLM-emitted code in a persistent interpreter session.
	// Location-agnostic: direct local runtimes (ladr self-hosted) or a call
	// routed over the device channel to a signed-in client (federated). The
	// engine core never knows which — this is the local-vs-federated seam.
	ExecuteCode(ctx context.Context, args ExecuteCodeParams) (ExecuteCodeResult, error)
	// FilesOp performs a files: tool operation (read/write/list) against the
	// run's file scope. Same location-agnostic seam as ExecuteCode: the local
	// engine resolves paths against the session workspace; a hosted engine
	// routes the op to the user's device, where consent is enforced per
	// ladder before anything touches disk. Denied ops return an error (the
	// adaptive loop surfaces it as tool feedback, not a run failure).
	FilesOp(ctx context.Context, args FilesOpParams) (FilesOpResult, error)
	Recurse(ctx context.Context, args RecurseArgs) (string, error)
	// Handoff performs a cross-ladder tail-call jump. The current execution
	// terminates; the returned ExitOutput becomes the user-visible result.
	Handoff(ctx context.Context, args HandoffArgs) (HandoffResult, error)
	SetPauseState(ctx context.Context, pos PausedPosition) error
	ShouldPause(ctx context.Context, stepID string) (bool, error)
	GetSystemPromptOverride(ctx context.Context, stepID string) (string, error)
	GetNodeCount(ctx context.Context, args GetNodeCountArgs) (int, error)
}

// ResourceBudget caps resource usage during pipeline execution.
type ResourceBudget struct {
	MaxLoops          int
	MaxNodesPerStep   int
	MaxRecursionDepth int
	MaxTotalLlmCalls  int
}

// ExecutePipelineOptions configures a single pipeline execution.
type ExecutePipelineOptions struct {
	Config            *ladr.PipelineConfig
	Context           ExecutionRuntimeContext
	IO                EngineIO
	Provider          ProviderConfig
	FallbackProviders []ProviderConfig
	MaxLoops          int
	Budget            *ResourceBudget
	DebugMode         bool
	ResumeFrom        *PausedPosition
	PauseRequestCheck func() bool // external pause request; honored at step boundaries

	// Phase 1 extensions.
	RelikStore     relik.Store     // persistent memory (reliks)
	CeilingTracker *CeilingTracker // resource ceilings (Path B safety net)
	WriteExecutor  *WriteExecutor  // schema-matched write executor
}

// ResolvedField is the resolved value of a step field, ready for prompt assembly.
type ResolvedField struct {
	Name  string
	Value any // string or []string
}
