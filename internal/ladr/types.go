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

// Package ladr implements the ladder configuration DSL — types,
// parsing, validation, and knob resolution.
// Ported from packages/ladr/schemas.ts.
package ladr

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// LoopRef / NodeRef
// ---------------------------------------------------------------------------

// LoopRefKind discriminates the LoopRef union.
type LoopRefKind int

const (
	LoopRefString LoopRefKind = iota // "accumulate" | "previous" | "current"
	LoopRefNumber                    // numeric
)

// LoopRef is a union of ("accumulate" | "previous" | "current") or a number.
// When Kind==LoopRefString, Str holds the keyword; when LoopRefNumber, Num holds
// the value.
type LoopRef struct {
	Kind LoopRefKind
	Str  string
	Num  float64
}

// MarshalJSON encodes the LoopRef back to its JSON representation.
func (l LoopRef) MarshalJSON() ([]byte, error) {
	switch l.Kind {
	case LoopRefString:
		return json.Marshal(l.Str)
	case LoopRefNumber:
		return json.Marshal(l.Num)
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON implements the union: keyword or number. An empty string is
// tolerated as the zero value — configs stored by older builds carry
// "loopRef": "" on unset references.
func (l *LoopRef) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch s {
		case "accumulate", "previous", "current":
			l.Kind = LoopRefString
			l.Str = s
			return nil
		case "":
			l.Kind = LoopRefString
			l.Str = ""
			return nil
		default:
			return fmt.Errorf("loopRef: invalid keyword %q", s)
		}
	}
	// Try number.
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		l.Kind = LoopRefNumber
		l.Num = n
		return nil
	}
	return fmt.Errorf("loopRef: expected keyword or number")
}

// IsZero reports the unset loop reference (no keyword, no number).
func (l LoopRef) IsZero() bool {
	return l.Kind == LoopRefString && l.Str == ""
}

// NodeRef is one of "current" | "previous" | "accumulate".
type NodeRef string

const (
	NodeRefCurrent    NodeRef = "current"
	NodeRefPrevious   NodeRef = "previous"
	NodeRefAccumulate NodeRef = "accumulate"
)

// ---------------------------------------------------------------------------
// NodesFrom — { stepId, loopRef, nodeRef, pruned }
// ---------------------------------------------------------------------------

// NodesFrom describes a reference into a previous step's node set.
type NodesFrom struct {
	StepID  string  `json:"stepId"`
	LoopRef LoopRef `json:"loopRef"`
	NodeRef NodeRef `json:"nodeRef,omitempty"`
	Pruned  bool    `json:"pruned,omitempty"`
}

// MarshalJSON omits an unset loopRef. The default marshal would emit
// "loopRef": "" for a zero LoopRef, which the parser rejects — so configs
// that round-tripped through storage failed to re-parse.
func (n NodesFrom) MarshalJSON() ([]byte, error) {
	m := map[string]any{"stepId": n.StepID}
	if !n.LoopRef.IsZero() {
		m["loopRef"] = n.LoopRef
	}
	if n.NodeRef != "" {
		m["nodeRef"] = n.NodeRef
	}
	if n.Pruned {
		m["pruned"] = n.Pruned
	}
	return json.Marshal(m)
}

// ---------------------------------------------------------------------------
// Knob inputs
// ---------------------------------------------------------------------------

// SliderStep is one tick on a slider input.
type SliderStep struct {
	Title   string  `json:"title"`
	Value   float64 `json:"value"`
	Default bool    `json:"default,omitempty"`
}

// SliderInput is an input with 3-5 discrete steps.
type SliderInput struct {
	Input string       `json:"input"` // always "slider"
	Steps []SliderStep `json:"steps"`
}

// NumericalInput is an input with a continuous min/default/max range.
type NumericalInput struct {
	Input   string   `json:"input"` // always "numerical"
	Default *float64 `json:"default,omitempty"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
}

// UnmarshalJSON enforces that "min" and "max" are present. Go zero-values
// non-pointer floats, so without this check a missing bound would silently
// become 0 — unlike the TS Zod schema which requires both fields.
func (n *NumericalInput) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, ok := raw["min"]; !ok {
		return fmt.Errorf(`numerical: missing required field "min"`)
	}
	if _, ok := raw["max"]; !ok {
		return fmt.Errorf(`numerical: missing required field "max"`)
	}
	type alias NumericalInput
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*n = NumericalInput(a)
	return nil
}

// KnobInputKind discriminates KnobInput.
type KnobInputKind int

const (
	KnobInputSlider KnobInputKind = iota
	KnobInputNumerical
)

// KnobInput is a union of SliderInput | NumericalInput.
type KnobInput struct {
	Kind      KnobInputKind
	Slider    SliderInput
	Numerical NumericalInput
}

// MarshalJSON encodes the knob input as its flat JSON form (input + fields).
func (k KnobInput) MarshalJSON() ([]byte, error) {
	switch k.Kind {
	case KnobInputSlider:
		return json.Marshal(k.Slider)
	case KnobInputNumerical:
		return json.Marshal(k.Numerical)
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON peeks at "input" to decide which variant to decode.
func (k *KnobInput) UnmarshalJSON(data []byte) error {
	var probe struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Input {
	case "slider":
		k.Kind = KnobInputSlider
		return json.Unmarshal(data, &k.Slider)
	case "numerical":
		k.Kind = KnobInputNumerical
		return json.Unmarshal(data, &k.Numerical)
	default:
		return fmt.Errorf("knob input: unknown or missing \"input\" field: %q", probe.Input)
	}
}

// KnobType enumerates knob categories.
type KnobType string

const (
	KnobTypeLoops     KnobType = "loops"
	KnobTypeRecursion KnobType = "recursion"
	KnobTypeNodes     KnobType = "nodes"
	KnobTypeGeneric   KnobType = "generic"
)

// Knob is the intersection of { name, type } and a KnobInput.
type Knob struct {
	Name  string    `json:"name"`
	Type  KnobType  `json:"type"`
	Input KnobInput `json:"-"`
}

// rawKnob is the flat wire form used for (un)marshalling.
type rawKnob struct {
	Name  string          `json:"name"`
	Type  KnobType        `json:"type"`
	Input json.RawMessage `json:"-"`
}

// MarshalJSON flattens Knob into the same JSON shape Zod's intersection
// produces: { name, type, input, ... }.
func (k Knob) MarshalJSON() ([]byte, error) {
	inputJSON, err := k.Input.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(inputJSON, &m); err != nil {
		return nil, err
	}
	m["name"] = mustJSON(k.Name)
	m["type"] = mustJSON(k.Type)
	return json.Marshal(m)
}

// UnmarshalJSON flattens from the wire form.
func (k *Knob) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if v, ok := m["name"]; ok {
		_ = json.Unmarshal(v, &k.Name)
	}
	if v, ok := m["type"]; ok {
		_ = json.Unmarshal(v, &k.Type)
	}
	// Everything except name/type goes to KnobInput via the raw map.
	// We reconstruct a sub-object containing only input-specific fields.
	sub := make(map[string]json.RawMessage)
	for key, val := range m {
		if key == "name" || key == "type" {
			continue
		}
		sub[key] = val
	}
	subBytes, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	return json.Unmarshal(subBytes, &k.Input)
}

// ---------------------------------------------------------------------------
// Fields
// ---------------------------------------------------------------------------

// FieldType discriminates Field variants.
type FieldType string

const (
	FieldText        FieldType = "text"
	FieldIngest      FieldType = "ingest"
	FieldMultiIngest FieldType = "multi_ingest"
	FieldNodeInfo    FieldType = "nodeInfo"
	FieldKnobInfo    FieldType = "knobInfo"
	FieldRelikRead   FieldType = "relikRead"
	FieldManifest    FieldType = "manifest"
	FieldIndexSearch FieldType = "indexSearch"
)

// FieldSourceRef is a stepId/loopRef/nodeRef triple used inside IngestField
// and MultiIngestField.
type FieldSourceRef struct {
	StepID  string  `json:"stepId"`
	LoopRef LoopRef `json:"loopRef"`
	NodeRef NodeRef `json:"nodeRef,omitempty"`
}

// MarshalJSON omits an unset loopRef — same reason as NodesFrom: the zero
// LoopRef must not round-trip back as "loopRef": "" (the stored form the
// parser only tolerates for legacy rows, and the TS side rejects outright).
func (f FieldSourceRef) MarshalJSON() ([]byte, error) {
	m := map[string]any{"stepId": f.StepID}
	if !f.LoopRef.IsZero() {
		m["loopRef"] = f.LoopRef
	}
	if f.NodeRef != "" {
		m["nodeRef"] = f.NodeRef
	}
	return json.Marshal(m)
}

// TextField references a free-form text source by string key.
type TextField struct {
	Name string `json:"name"`
	Type string `json:"type"` // "text"
	From string `json:"from"`
	// Fallback, if declared, is consulted when the primary From source misses
	// (empty value or missing key). Uses the unified FromSpec vocabulary — same
	// shape as write/dynamic from: declarations.
	Fallback *FromSpec `json:"fallback,omitempty"`
}

// IngestField pulls data from a single step/loopRef/nodeRef.
type IngestField struct {
	Name          string         `json:"name"`
	Type          string         `json:"type"` // "ingest"
	From          FieldSourceRef `json:"from"`
	SkipFirstNode bool           `json:"skipFirstNode,omitempty"`
	// Fallback, if declared, is consulted when the primary From source misses
	// (step didn't run, output empty, etc.). Uses the unified FromSpec vocabulary.
	Fallback *FromSpec `json:"fallback,omitempty"`
}

// MultiIngestField pulls data from multiple step/loopRef/nodeRef sources.
type MultiIngestField struct {
	Name string           `json:"name"`
	Type string           `json:"type"` // "multi_ingest"
	From []FieldSourceRef `json:"from"`
}

// NodeInfoField injects runtime node metadata.
type NodeInfoField struct {
	Name string `json:"name"`
	Type string `json:"type"` // "nodeInfo"
	// From is captured at parse time so validation can reject it (spec §5.7:
	// nodeInfo takes no source). It's never used for resolution.
	From string `json:"from,omitempty"`
}

// KnobInfoField injects knob metadata.
type KnobInfoField struct {
	Name string `json:"name"`
	Type string `json:"type"` // "knobInfo"
	From string `json:"from"`
}

// IndexSearchField runs grep-grade full-text search over the owner's
// indexed chunks and injects the hits. Navigation by metadata + FTS — the
// text-RAG bet; no vectors.
type IndexSearchField struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // "indexSearch"
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// Field is a union of all field types.
type Field struct {
	Kind         FieldType
	Text         TextField
	Ingest       IngestField
	MultiIngest  MultiIngestField
	NodeInfo     NodeInfoField
	KnobInfo     KnobInfoField
	RelikRead    RelikReadField
	Manifest     ManifestField
	IndexSearch  IndexSearchField
}

// MarshalJSON encodes the active variant.
func (f Field) MarshalJSON() ([]byte, error) {
	switch f.Kind {
	case FieldText:
		return json.Marshal(f.Text)
	case FieldIngest:
		return json.Marshal(f.Ingest)
	case FieldMultiIngest:
		return json.Marshal(f.MultiIngest)
	case FieldNodeInfo:
		return json.Marshal(f.NodeInfo)
	case FieldKnobInfo:
		return json.Marshal(f.KnobInfo)
	case FieldRelikRead:
		return json.Marshal(f.RelikRead)
	case FieldManifest:
		return json.Marshal(f.Manifest)
	case FieldIndexSearch:
		return json.Marshal(f.IndexSearch)
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON dispatches on "type".
func (f *Field) UnmarshalJSON(data []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Type {
	case "text":
		f.Kind = FieldText
		return json.Unmarshal(data, &f.Text)
	case "ingest":
		f.Kind = FieldIngest
		return json.Unmarshal(data, &f.Ingest)
	case "multi_ingest":
		f.Kind = FieldMultiIngest
		return json.Unmarshal(data, &f.MultiIngest)
	case "nodeInfo":
		f.Kind = FieldNodeInfo
		return json.Unmarshal(data, &f.NodeInfo)
	case "knobInfo":
		f.Kind = FieldKnobInfo
		return json.Unmarshal(data, &f.KnobInfo)
	case "relikRead":
		f.Kind = FieldRelikRead
		return json.Unmarshal(data, &f.RelikRead)
	case "manifest":
		f.Kind = FieldManifest
		return json.Unmarshal(data, &f.Manifest)
	case "indexSearch":
		f.Kind = FieldIndexSearch
		return json.Unmarshal(data, &f.IndexSearch)
	default:
		return fmt.Errorf("field: unknown type %q", probe.Type)
	}
}

// ---------------------------------------------------------------------------
// Nodes (number | string | { from: NodesFrom })
// ---------------------------------------------------------------------------

// NodesKind discriminates Nodes.
type NodesKind int

const (
	NodesNumber NodesKind = iota
	NodesString
	NodesFromObj
)

// Nodes is a union of number | string | { from: NodesFrom }.
type Nodes struct {
	Kind NodesKind
	Num  float64
	Str  string
	From NodesFrom
}

// MarshalJSON encodes the active variant.
func (n Nodes) MarshalJSON() ([]byte, error) {
	switch n.Kind {
	case NodesNumber:
		return json.Marshal(n.Num)
	case NodesString:
		return json.Marshal(n.Str)
	case NodesFromObj:
		return json.Marshal(struct {
			From NodesFrom `json:"from"`
		}{From: n.From})
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON dispatches by JSON type.
func (n *Nodes) UnmarshalJSON(data []byte) error {
	// Number?
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		n.Kind = NodesNumber
		n.Num = num
		return nil
	}
	// String?
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		n.Kind = NodesString
		n.Str = s
		return nil
	}
	// Object with "from"?
	var obj struct {
		From NodesFrom `json:"from"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		n.Kind = NodesFromObj
		n.From = obj.From
		return nil
	}
	return fmt.Errorf("nodes: expected number, string, or { from: ... }")
}

// ---------------------------------------------------------------------------
// Recursion, Checkpoint, TraceConfig
// ---------------------------------------------------------------------------

// RecursionStep is an inline recursion config: maxDepth can be number or string.
type RecursionStep struct {
	MaxDepthNumOrStr MaxDepth `json:"maxDepth"`
}

// MaxDepth is a union of number | string.
type MaxDepth struct {
	IsStr bool
	Num   float64
	Str   string
}

// MarshalJSON encodes MaxDepth.
func (m MaxDepth) MarshalJSON() ([]byte, error) {
	if m.IsStr {
		return json.Marshal(m.Str)
	}
	return json.Marshal(m.Num)
}

// UnmarshalJSON dispatches by JSON type.
func (m *MaxDepth) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.IsStr = true
		m.Str = s
		return nil
	}
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		m.IsStr = false
		m.Num = n
		return nil
	}
	return fmt.Errorf("maxDepth: expected number or string")
}

// Checkpoint controls heading text at execution boundaries.
type Checkpoint struct {
	Heading     string `json:"heading,omitempty"`
	NodeHeading string `json:"nodeHeading,omitempty"`
}

// TraceMode controls what trace data is emitted.
type TraceMode string

const (
	TraceModeReasoning TraceMode = "reasoning"
	TraceModeInline    TraceMode = "inline"
	TraceModeOff       TraceMode = "off"
)

// TraceConfig controls pipeline trace output.
type TraceConfig struct {
	AllSteps bool      `json:"allSteps,omitempty"`
	Mode     TraceMode `json:"mode,omitempty"`
}

// ---------------------------------------------------------------------------
// Steps
// ---------------------------------------------------------------------------

// StepType discriminates step variants.
type StepType string

const (
	StepTypeNormal     StepType = "normal"
	StepTypeSequential StepType = "sequential"
	StepTypeGroup      StepType = "group"
)

// TimelineKind enumerates timeline annotations.
type TimelineKind string

const (
	TimelineCircle TimelineKind = "circle"
	TimelineInit   TimelineKind = "init"
)

// FieldsConfig is a union of []Field | string (clone reference or raw).
type FieldsConfig struct {
	IsString bool
	Str      string
	Fields   []Field
}

// MarshalJSON encodes FieldsConfig.
func (fc FieldsConfig) MarshalJSON() ([]byte, error) {
	if fc.IsString {
		return json.Marshal(fc.Str)
	}
	if fc.Fields == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(fc.Fields)
}

// UnmarshalJSON dispatches by JSON type.
func (fc *FieldsConfig) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		fc.IsString = true
		fc.Str = s
		return nil
	}
	var fields []Field
	if err := json.Unmarshal(data, &fields); err == nil {
		fc.IsString = false
		fc.Fields = fields
		return nil
	}
	return fmt.Errorf("fields: expected string or array")
}

// IsSet returns true if the FieldsConfig has any value assigned.
func (fc FieldsConfig) IsSet() bool {
	return fc.IsString || len(fc.Fields) > 0
}

// BaseStep contains the fields common to NormalStep and SequentialStep.
type BaseStep struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Type         StepType       `json:"-"`
	Nodes        *Nodes         `json:"nodes,omitempty"`
	Fields       *FieldsConfig  `json:"fields,omitempty"`
	SystemPrompt string         `json:"systemPrompt,omitempty"`
	ContinueIf   string         `json:"continueIf,omitempty"` // deprecated alias for If
	Timeline     *TimelineKind  `json:"timeline,omitempty"`
	Recursion    *RecursionStep `json:"recursion,omitempty"`
	Checkpoint   *Checkpoint    `json:"checkpoint,omitempty"`

	// Phase 1 extensions (LADR grammar).
	If          *If         `json:"if,omitempty"`
	RelikWrite  *RelikWrite `json:"relikWrite,omitempty"`
	Dynamic     bool        `json:"dynamic,omitempty"`
	DynamicFrom *FromSpec   `json:"from,omitempty"` // used when dynamic: true
	// DynamicFromFallback, if declared, is consulted when DynamicFrom resolves
	// to an empty/invalid config. Used to declare a fallback step config source.
	DynamicFromFallback *FromSpec `json:"fromFallback,omitempty"`
	OnInvalid           *Action   `json:"onInvalid,omitempty"` // fallback for dynamic step load failure

	// Flat step-level adaptive keys (the redesign). Each enables a bounded
	// multi-turn loop. When multiple are declared on one step, they share ONE
	// loop (one maxRounds, one requestMatch). See adaptive.go.
	CodeExecution *CodeExecutionConfig `json:"codeExecution,omitempty"`
	RelikRead     *RelikReadConfig     `json:"relikRead,omitempty"`
	RelikEdit     *RelikEditConfig     `json:"relikEdit,omitempty"`
	Files         *FilesConfig         `json:"files,omitempty"`
	ConfigEdit    *ConfigEditConfig    `json:"configEdit,omitempty"`

	// Model overrides the run's model for this step (e.g. a cheap step in
	// an expensive ladder). Validated against the config's allowedTargets;
	// the engine charges spend against the model actually used.
	Model string `json:"model,omitempty"`

	// Mail posts an envelope after the step completes — the declarative
	// send side of agent-to-agent messaging. Payloads stay in reliks;
	// RelikRef points at where the payload lives.
	Mail *MailBlock `json:"mail,omitempty"`
}

// MailBlock is a post-step send: one small envelope addressed to a ladder
// (program address, resolved per owner at run time).
type MailBlock struct {
	To            string        `json:"to"`                // recipient ladder name
	Type          string        `json:"type,omitempty"`    // e.g. "notice", "handoff", "alert"
	CorrelationID string        `json:"correlationId,omitempty"`
	RelikRef      *RelikTarget  `json:"relikRef,omitempty"` // payload pointer; nil = pure signal
}

// NormalStep runs its nodes in parallel.
type NormalStep struct {
	BaseStep
}

// SequentialStep runs its nodes sequentially.
type SequentialStep struct {
	BaseStep
}

// GroupStep wraps a list of child steps.  Groups cannot be nested.
type GroupStep struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Type  StepType `json:"type"` // "group"
	Steps []Step   `json:"steps"`
}

// Step is a union of NormalStep | SequentialStep | GroupStep.
type Step struct {
	Kind       StepType
	Normal     *NormalStep
	Sequential *SequentialStep
	Group      *GroupStep
}

// ID returns the step ID regardless of which variant is active.
func (s Step) ID() string {
	switch s.Kind {
	case StepTypeGroup:
		if s.Group != nil {
			return s.Group.ID
		}
	case StepTypeNormal:
		if s.Normal != nil {
			return s.Normal.ID
		}
	case StepTypeSequential:
		if s.Sequential != nil {
			return s.Sequential.ID
		}
	}
	return ""
}

// Name returns the step name regardless of which variant is active.
func (s Step) Name() string {
	switch s.Kind {
	case StepTypeGroup:
		if s.Group != nil {
			return s.Group.Name
		}
	case StepTypeNormal:
		if s.Normal != nil {
			return s.Normal.Name
		}
	case StepTypeSequential:
		if s.Sequential != nil {
			return s.Sequential.Name
		}
	}
	return ""
}

// Base returns the *BaseStep for normal/sequential steps, or nil for groups.
func (s Step) Base() *BaseStep {
	if s.Normal != nil {
		return &s.Normal.BaseStep
	}
	if s.Sequential != nil {
		return &s.Sequential.BaseStep
	}
	return nil
}

// MarshalJSON encodes the active step variant.
func (s Step) MarshalJSON() ([]byte, error) {
	switch s.Kind {
	case StepTypeGroup:
		return json.Marshal(s.Group)
	default:
		// Normal and Sequential share BaseStep; encode via a wrapper that
		// injects the correct "type" literal.
		if s.Normal != nil {
			return marshalBase(s.Normal.BaseStep, "normal")
		}
		if s.Sequential != nil {
			return marshalBase(s.Sequential.BaseStep, "sequential")
		}
		return json.Marshal(nil)
	}
}

// UnmarshalJSON dispatches on "type".
func (s *Step) UnmarshalJSON(data []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Type {
	case "normal":
		s.Kind = StepTypeNormal
		s.Normal = &NormalStep{}
		return unmarshalBase(data, "normal", &s.Normal.BaseStep)
	case "sequential":
		s.Kind = StepTypeSequential
		s.Sequential = &SequentialStep{}
		return unmarshalBase(data, "sequential", &s.Sequential.BaseStep)
	case "group":
		s.Kind = StepTypeGroup
		s.Group = &GroupStep{}
		return json.Unmarshal(data, s.Group)
	default:
		return fmt.Errorf("step: unknown type %q", probe.Type)
	}
}

// ---------------------------------------------------------------------------
// AllowedTargets
// ---------------------------------------------------------------------------

// AllowedTargetsStrategy enumerates the strategies.
type AllowedTargetsStrategy string

const (
	AllowedTargetsUniversal   AllowedTargetsStrategy = "universal"
	AllowedTargetsConstrained AllowedTargetsStrategy = "constrained"
)

// AllowedTargetsPolicy is a discriminated union.
type AllowedTargetsPolicy struct {
	Strategy  AllowedTargetsStrategy
	Providers []string // constrained only
	Models    []string // constrained only
}

// MarshalJSON encodes AllowedTargetsPolicy.
func (a AllowedTargetsPolicy) MarshalJSON() ([]byte, error) {
	switch a.Strategy {
	case AllowedTargetsUniversal:
		return json.Marshal(struct {
			Strategy string `json:"strategy"`
		}{Strategy: "universal"})
	case AllowedTargetsConstrained:
		return json.Marshal(struct {
			Strategy  string   `json:"strategy"`
			Providers []string `json:"providers"`
			Models    []string `json:"models"`
		}{
			Strategy:  "constrained",
			Providers: a.Providers,
			Models:    a.Models,
		})
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON dispatches on "strategy".
func (a *AllowedTargetsPolicy) UnmarshalJSON(data []byte) error {
	var probe struct {
		Strategy  string   `json:"strategy"`
		Providers []string `json:"providers"`
		Models    []string `json:"models"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Strategy {
	case "universal", "constrained":
		// ok
	default:
		return fmt.Errorf("allowedTargets: invalid strategy %q", probe.Strategy)
	}
	a.Strategy = AllowedTargetsStrategy(probe.Strategy)
	a.Providers = probe.Providers
	a.Models = probe.Models
	return nil
}

// ---------------------------------------------------------------------------
// PipelineConfig + ValidationResult
// ---------------------------------------------------------------------------

// PipelineConfig is the root configuration object for a ladder.
type PipelineConfig struct {
	ID             string               `json:"id,omitempty"`
	Name           string               `json:"name"`
	Description    string               `json:"description,omitempty"`
	LadrVersion    string               `json:"ladrVersion,omitempty"`
	AllowedTargets AllowedTargetsPolicy `json:"allowedTargets"`
	Exit           string               `json:"exit"`
	Knobs          map[string]Knob      `json:"knobs"`
	Steps          []Step               `json:"steps"`
	Trace          *TraceConfig         `json:"trace,omitempty"`

	// Phase 1 extensions (LADR grammar).
	Reliks          []RelikDecl      `json:"reliks,omitempty"`
	ExecutionBudget *ExecutionBudget `json:"executionBudget,omitempty"`

	// Indexer: content-addressed session memory. Enabled turns on the
	// input/output tee (identity dedup — identical content stores zero new
	// bytes) and makes indexSearch fields resolvable.
	Indexer *IndexerConfig `json:"indexer,omitempty"`

	// Mailbox: durable, program-addressed messaging. Drain consumes the
	// ladder's pending messages at run start and injects them as an input
	// (the as: name) for steps to read.
	Mailbox *MailboxConfig `json:"mailbox,omitempty"`

	// Schedule: this ladder on a clock. Fixed entries (cron + explicit tz)
	// or the pointer form — {from: {relik: Self, folder: config, file:
	// schedule.json}} — self-editable via relikEdit; invalid edits keep the
	// last-known-good. A fire is a normal run stamped origin: schedule.
	Schedule *ScheduleConfig `json:"schedule,omitempty"`

	// DefaultManifest is the bundle of views used when a step declares a bare
	// type: manifest field (no from:). If nil, bare manifest fields fall back
	// to the full dump (backward compat).
	DefaultManifest []ManifestView `json:"defaultManifest,omitempty"`
}

// IndexerConfig controls the session-memory indexer for a ladder.
type IndexerConfig struct {
	Enabled     bool   `json:"enabled"`
	ChunkTokens int    `json:"chunkTokens,omitempty"` // default 512
	Overlap     int    `json:"overlap,omitempty"`     // trailing paragraphs carried into next chunk
	Scope       string `json:"scope,omitempty"`       // "session" (default) | "run"
}

// MailboxConfig controls the run-start drain.
type MailboxConfig struct {
	Drain *DrainConfig `json:"drain,omitempty"`
}

// DrainConfig names the injection slot for drained envelopes.
type DrainConfig struct {
	As    string `json:"as"`
	Limit int    `json:"limit,omitempty"` // default 50
}

// ScheduleConfig declares the ladder's clock.
type ScheduleConfig struct {
	Entries []ScheduleEntry `json:"entries,omitempty"`
	// From points at a relik file holding {"entries":[...]} — the
	// self-editable form (uses the FromSpec vocabulary).
	From *FromSpec `json:"from,omitempty"`
}

// ScheduleEntry is one fixed schedule slot.
type ScheduleEntry struct {
	ID      string         `json:"id"`
	Cron    string         `json:"cron"`
	Tz      string         `json:"tz,omitempty"` // explicit timezone; default UTC
	Inputs  map[string]any `json:"inputs,omitempty"`
	Knobs   map[string]any `json:"knobs,omitempty"`
	Enabled *bool          `json:"enabled,omitempty"`
	// RequireDevice overrides the device pre-flight (nil = auto).
	RequireDevice *bool `json:"requireDevice,omitempty"`
}

// ValidationResult is the return type of all validation functions.
type ValidationResult struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// marshalBase serialises a BaseStep with the given type literal.  We use a
// map to preserve only set fields and the correct "type" value.
func marshalBase(b BaseStep, typeName string) ([]byte, error) {
	m := map[string]any{
		"id":   b.ID,
		"name": b.Name,
		"type": typeName,
	}
	if b.Nodes != nil {
		m["nodes"] = b.Nodes
	}
	if b.Fields != nil {
		m["fields"] = b.Fields
	}
	if b.SystemPrompt != "" {
		m["systemPrompt"] = b.SystemPrompt
	}
	if b.ContinueIf != "" {
		m["continueIf"] = b.ContinueIf
	}
	if b.Timeline != nil {
		m["timeline"] = *b.Timeline
	}
	if b.Recursion != nil {
		m["recursion"] = b.Recursion
	}
	if b.Checkpoint != nil {
		m["checkpoint"] = b.Checkpoint
	}
	if b.If != nil {
		m["if"] = b.If
	}
	if b.RelikWrite != nil {
		m["relikWrite"] = b.RelikWrite
	}
	if b.Dynamic {
		m["dynamic"] = b.Dynamic
	}
	if b.DynamicFrom != nil {
		m["from"] = b.DynamicFrom
	}
	if b.DynamicFromFallback != nil {
		m["fromFallback"] = b.DynamicFromFallback
	}
	if b.Model != "" {
		m["model"] = b.Model
	}
	if b.Mail != nil {
		m["mail"] = b.Mail
	}
	if b.OnInvalid != nil {
		m["onInvalid"] = b.OnInvalid
	}
	if b.CodeExecution != nil {
		m["codeExecution"] = b.CodeExecution
	}
	if b.RelikRead != nil {
		m["relikRead"] = b.RelikRead
	}
	if b.RelikEdit != nil {
		m["relikEdit"] = b.RelikEdit
	}
	if b.Files != nil {
		m["files"] = b.Files
	}
	if b.ConfigEdit != nil {
		m["configEdit"] = b.ConfigEdit
	}
	return json.Marshal(m)
}

// unmarshalBase decodes raw JSON into a BaseStep and validates the type literal.
func unmarshalBase(data []byte, expectedType string, b *BaseStep) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if v, ok := m["type"]; ok {
		var t string
		if err := json.Unmarshal(v, &t); err != nil {
			return err
		}
		if t != expectedType {
			return fmt.Errorf("step: type mismatch, expected %q got %q", expectedType, t)
		}
	}
	b.Type = StepType(expectedType)
	if v, ok := m["id"]; ok {
		_ = json.Unmarshal(v, &b.ID)
	}
	if v, ok := m["name"]; ok {
		_ = json.Unmarshal(v, &b.Name)
	}
	if v, ok := m["nodes"]; ok && string(v) != "null" {
		var n Nodes
		if err := json.Unmarshal(v, &n); err != nil {
			return err
		}
		b.Nodes = &n
	}
	if v, ok := m["fields"]; ok && string(v) != "null" {
		var fc FieldsConfig
		if err := json.Unmarshal(v, &fc); err != nil {
			return err
		}
		b.Fields = &fc
	}
	if v, ok := m["systemPrompt"]; ok {
		_ = json.Unmarshal(v, &b.SystemPrompt)
	}
	if v, ok := m["model"]; ok {
		_ = json.Unmarshal(v, &b.Model)
	}
	if v, ok := m["continueIf"]; ok {
		_ = json.Unmarshal(v, &b.ContinueIf)
	}
	if v, ok := m["timeline"]; ok && string(v) != "null" {
		var tl TimelineKind
		if err := json.Unmarshal(v, &tl); err != nil {
			return err
		}
		b.Timeline = &tl
	}
	if v, ok := m["recursion"]; ok && string(v) != "null" {
		var r RecursionStep
		if err := json.Unmarshal(v, &r); err != nil {
			return err
		}
		b.Recursion = &r
	}
	if v, ok := m["checkpoint"]; ok && string(v) != "null" {
		var c Checkpoint
		if err := json.Unmarshal(v, &c); err != nil {
			return err
		}
		b.Checkpoint = &c
	}
	// Phase 1 extensions
	if v, ok := m["if"]; ok && string(v) != "null" {
		var ifBlock If
		if err := json.Unmarshal(v, &ifBlock); err != nil {
			return err
		}
		b.If = &ifBlock
	}
	if v, ok := m["relikWrite"]; ok && string(v) != "null" {
		var sw RelikWrite
		if err := json.Unmarshal(v, &sw); err != nil {
			return err
		}
		b.RelikWrite = &sw
	}
	if v, ok := m["mail"]; ok && string(v) != "null" {
		var mb MailBlock
		if err := json.Unmarshal(v, &mb); err != nil {
			return err
		}
		b.Mail = &mb
	}
	if v, ok := m["dynamic"]; ok && string(v) != "null" {
		_ = json.Unmarshal(v, &b.Dynamic)
	}
	if v, ok := m["from"]; ok && string(v) != "null" {
		var fs FromSpec
		if err := json.Unmarshal(v, &fs); err != nil {
			return err
		}
		b.DynamicFrom = &fs
	}
	if v, ok := m["fromFallback"]; ok && string(v) != "null" {
		var fs FromSpec
		if err := json.Unmarshal(v, &fs); err != nil {
			return err
		}
		b.DynamicFromFallback = &fs
	}
	if v, ok := m["onInvalid"]; ok && string(v) != "null" {
		var act Action
		if err := json.Unmarshal(v, &act); err != nil {
			return err
		}
		b.OnInvalid = &act
	}
	if v, ok := m["codeExecution"]; ok && string(v) != "null" {
		var ce CodeExecutionConfig
		if err := json.Unmarshal(v, &ce); err != nil {
			return err
		}
		b.CodeExecution = &ce
	}
	if v, ok := m["relikRead"]; ok && string(v) != "null" {
		var sr RelikReadConfig
		if err := json.Unmarshal(v, &sr); err != nil {
			return err
		}
		b.RelikRead = &sr
	}
	if v, ok := m["relikEdit"]; ok && string(v) != "null" {
		var se RelikEditConfig
		if err := json.Unmarshal(v, &se); err != nil {
			return err
		}
		b.RelikEdit = &se
	}
	if v, ok := m["files"]; ok && string(v) != "null" {
		var fc FilesConfig
		if err := json.Unmarshal(v, &fc); err != nil {
			return err
		}
		b.Files = &fc
	}
	if v, ok := m["configEdit"]; ok && string(v) != "null" {
		var cec ConfigEditConfig
		if err := json.Unmarshal(v, &cec); err != nil {
			return err
		}
		b.ConfigEdit = &cec
	}
	return nil
}
