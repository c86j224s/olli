package workflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxWorkflowFile = 1 << 20
	maxInputBytes   = 1 << 20
	maxResultBytes  = 8 << 20
	maxLineBytes    = 64 << 10
	maxLogBytes     = 8 << 20
	terminalReserve = 64 << 10
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var refPattern = regexp.MustCompile(`^\{\{(inputs\.([a-z][a-z0-9_-]{0,63})|steps\.([a-z][a-z0-9_-]{0,63})\.(result\.([A-Za-z0-9_.-]+)|status|attempt))\}\}$`)
var embeddedRefPattern = regexp.MustCompile(`\{\{(inputs\.[a-z][a-z0-9_-]{0,63}|steps\.[a-z][a-z0-9_-]{0,63}\.(?:result\.[A-Za-z0-9_.-]+|status|attempt))\}\}`)
var runIDPattern = regexp.MustCompile(`^oaw_[0-9a-f]{32}$`)

// ToolDefinition is immutable tool metadata and its JSON Schema.
type ToolDefinition struct {
	Name             string
	Schema           any
	RetrySafe        bool
	WorkflowCallable bool
}

// ToolError marks a handler-local tool failure eligible for tool_error retry.
type ToolError struct{ Err error }

func (e ToolError) Error() string {
	if e.Err == nil {
		return "tool error"
	}
	return e.Err.Error()
}
func (e ToolError) Unwrap() error { return e.Err }

// HandlerTimeout marks a handler-local timeout eligible for timeout retry.
type HandlerTimeout struct{ Err error }

func (e HandlerTimeout) Error() string {
	if e.Err == nil {
		return "handler timeout"
	}
	return e.Err.Error()
}
func (e HandlerTimeout) Unwrap() error { return e.Err }

// ToolCatalog supplies the fixed tool catalog. Implementations must not mutate it after Engine creation.
type ToolCatalog interface{ ListTools() []ToolDefinition }

// ToolExecutor runs one already-authorized attempt.
type ToolExecutor interface {
	ExecuteContext(context.Context, string, map[string]any) (string, error)
}

// Authorizer is evaluated for every individual tool attempt. It can only allow or deny.
// Implementations must return when the supplied context is cancelled.
type Authorizer func(context.Context, string, map[string]any, string, int) bool

// Engine runs workflows beneath an immutable workspace root.
type Engine struct {
	root           string
	rootFD         *os.File
	catalog        map[string]ToolDefinition
	toolSchemas    map[string]*jsonschema.Schema
	executor       ToolExecutor
	closeMu        sync.RWMutex
	closed         bool
	workflowSchema *jsonschema.Schema
	eventSchema    *jsonschema.Schema
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Close releases the stable workspace descriptor.
func (e *Engine) Close() error {
	e.closeMu.Lock()
	defer e.closeMu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.rootFD != nil {
		return e.rootFD.Close()
	}
	return nil
}

// NewEngine constructs an engine. Tool names and metadata are snapshotted.
func NewEngine(root string, catalog ToolCatalog, executor ToolExecutor) (*Engine, error) {
	if catalog == nil || executor == nil {
		return nil, errors.New("workflow: catalog and executor are required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(abs, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	rootFD := os.NewFile(uintptr(fd), abs)
	if rootFD == nil {
		_ = unix.Close(fd)
		return nil, errors.New("workflow: root open failed")
	}
	st, err := rootFD.Stat()
	if err != nil || !st.IsDir() {
		_ = rootFD.Close()
		return nil, errors.New("workflow: root must be a directory")
	}
	m := map[string]ToolDefinition{}
	toolSchemas := map[string]*jsonschema.Schema{}
	for _, d := range catalog.ListTools() {
		if !namePattern.MatchString(d.Name) {
			_ = rootFD.Close()
			return nil, fmt.Errorf("invalid tool name %q", d.Name)
		}
		if _, ok := m[d.Name]; ok {
			_ = rootFD.Close()
			return nil, fmt.Errorf("duplicate tool %q", d.Name)
		}
		clonedSchema, err := cloneJSONValue(d.Schema)
		if err != nil {
			_ = rootFD.Close()
			return nil, fmt.Errorf("tool %q schema: %w", d.Name, err)
		}
		d.Schema = clonedSchema
		compiled, err := compileToolSchema(d)
		if err != nil {
			_ = rootFD.Close()
			return nil, fmt.Errorf("tool %q schema: %w", d.Name, err)
		}
		m[d.Name] = d
		toolSchemas[d.Name] = compiled
	}
	e := &Engine{root: abs, rootFD: rootFD, catalog: m, toolSchemas: toolSchemas, executor: executor}
	if err := e.ensureDirs([]string{"sessions", "workflows"}); err != nil {
		_ = e.Close()
		return nil, err
	}
	if err := e.compileWorkflowSchema(); err != nil {
		_ = e.Close()
		return nil, err
	}
	if err := e.compileEventSchema(); err != nil {
		_ = e.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) Root() string { return e.root }
func (e *Engine) List() ([]string, error) {
	d, err := e.openRelativeDir([]string{"workflows", "agent"})
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.Readdir(-1)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range entries {
		if ent.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("workflow: symlink entry %q", ent.Name())
		}
		if strings.HasPrefix(ent.Name(), ".") || !strings.HasSuffix(ent.Name(), ".oaw.json") {
			continue
		}
		name := strings.TrimSuffix(ent.Name(), ".oaw.json")
		if !namePattern.MatchString(name) {
			continue
		}
		if ent.IsDir() || !ent.Mode().IsRegular() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (e *Engine) Show(name string) (map[string]any, error) { return e.load(name) }
func (e *Engine) Load(name string) (map[string]any, error) { return e.load(name) }
func (e *Engine) Validate(name string) error               { _, err := e.validate(name); return err }

func (e *Engine) load(name string) (map[string]any, error) {
	if !namePattern.MatchString(name) {
		return nil, errors.New("invalid workflow name")
	}
	b, err := e.readRelative([]string{"workflows", "agent", name + ".oaw.json"}, maxWorkflowFile)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) {
		return nil, errors.New("workflow is not valid UTF-8")
	}
	decoded, err := decodeJSONDocument(b)
	if err != nil {
		return nil, fmt.Errorf("workflow JSON: %w", err)
	}
	doc, ok := decoded.(map[string]any)
	if !ok || doc == nil {
		return nil, errors.New("workflow must be an object")
	}
	if got, _ := doc["name"].(string); got != name {
		return nil, errors.New("workflow name does not match filename")
	}
	return doc, nil
}
func ensureEOF(d *json.Decoder) error {
	var x any
	if err := d.Decode(&x); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}

func decodeJSONDocument(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("JSON object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON object key %q", key)
			}
			child, err := decodeJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = child
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			child, err := decodeJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, child)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func cloneJSONValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, string, bool, json.Number,
		float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		if _, err := json.Marshal(typed); err != nil {
			return nil, err
		}
		return typed, nil
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			value, err := cloneJSONValue(child)
			if err != nil {
				return nil, err
			}
			cloned[key] = value
		}
		return cloned, nil
	case []any:
		cloned := make([]any, len(typed))
		for index, child := range typed {
			value, err := cloneJSONValue(child)
			if err != nil {
				return nil, err
			}
			cloned[index] = value
		}
		return cloned, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value type %T", value)
	}
}

func rejectExternalRefs(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "$ref" {
				ref, ok := child.(string)
				if !ok || !strings.HasPrefix(ref, "#") {
					return fmt.Errorf("external schema reference %q is not allowed", ref)
				}
			}
			if err := rejectExternalRefs(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rejectExternalRefs(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) compileWorkflowSchema() error {
	data, err := e.readRelative([]string{"workflows", "agent", "oaw.schema.json"}, maxWorkflowFile)
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if !utf8.Valid(data) {
		return errors.New("schema is not valid UTF-8")
	}
	document, err := decodeJSONDocument(data)
	if err != nil {
		return fmt.Errorf("schema JSON: %w", err)
	}
	if err := rejectExternalRefs(document); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const resource = "https://olli.local/schemas/oaw.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return fmt.Errorf("compile schema resource: %w", err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	e.workflowSchema = schema
	return nil
}

func (e *Engine) validate(name string) (*plan, error) {
	doc, err := e.load(name)
	if err != nil {
		return nil, err
	}
	if e.workflowSchema == nil {
		return nil, errors.New("workflow schema is unavailable")
	}
	if err := e.workflowSchema.Validate(doc); err != nil {
		return nil, fmt.Errorf("schema validation: %w", err)
	}
	return semanticPlan(doc, e.catalog)
}

type inputSpec struct {
	Type           string
	Required       bool
	Default        any
	HasDefault     bool
	Enum           []any
	Min, Max       *float64
	MinLen, MaxLen *int
}
type plan struct {
	doc       map[string]any
	inputs    map[string]inputSpec
	steps     []step
	byID      map[string]int
	kinds     map[string]string
	limits    limits
	reachable map[int]bool
	maxCalls  int
	onFailure string
}
type limits struct{ maxSteps, maxCalls, maxAttempts, timeout int }
type step struct {
	ID, Kind, Tool string
	Args           map[string]any
	Retry          retrySpec
	Cond           condition
	True, False    string
}
type retrySpec struct {
	Max  int
	When map[string]bool
}
type condition struct {
	Ref   string
	Op    string
	Value any
}

func semanticPlan(doc map[string]any, tools map[string]ToolDefinition) (*plan, error) {
	p := &plan{doc: doc, inputs: map[string]inputSpec{}, byID: map[string]int{}, kinds: map[string]string{}}
	p.onFailure, _ = doc["on_failure"].(string)
	for n, raw := range doc["inputs"].(map[string]any) {
		x, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input %s is not object", n)
		}
		in := inputSpec{Type: x["type"].(string)}
		in.Required, _ = x["required"].(bool)
		in.Default, in.HasDefault = x["default"]
		if ev, ok := x["enum"].([]any); ok {
			in.Enum = ev
			for i := range ev {
				for j := 0; j < i; j++ {
					if equalJSON(ev[i], ev[j]) {
						return nil, fmt.Errorf("input %s enum has duplicates", n)
					}
				}
			}
		}
		for _, k := range []string{"minimum", "maximum"} {
			if v, ok := x[k].(json.Number); ok {
				f, _ := v.Float64()
				if k == "minimum" {
					in.Min = &f
				} else {
					in.Max = &f
				}
			}
		}
		for _, k := range []string{"min_length", "max_length"} {
			if v, ok := x[k].(json.Number); ok {
				z, _ := strconv.Atoi(string(v))
				if k == "min_length" {
					in.MinLen = &z
				} else {
					in.MaxLen = &z
				}
			}
		}
		if in.Min != nil && in.Max != nil && *in.Min > *in.Max || in.MinLen != nil && in.MaxLen != nil && *in.MinLen > *in.MaxLen {
			return nil, fmt.Errorf("input %s has inverted bounds", n)
		}
		if in.HasDefault {
			if err := validateInputValue(n, in, in.Default); err != nil {
				return nil, err
			}
		}
		p.inputs[n] = in
	}
	lm, ok := doc["limits"].(map[string]any)
	if !ok {
		return nil, errors.New("limits missing")
	}
	p.limits.maxSteps = numberInt(lm["max_steps"])
	p.limits.maxCalls = numberInt(lm["max_tool_calls"])
	p.limits.maxAttempts = numberInt(lm["max_attempts_per_step"])
	p.limits.timeout = numberInt(lm["timeout_seconds"])
	arr, _ := doc["steps"].([]any)
	if p.limits.maxSteps < len(arr) {
		return nil, errors.New("max_steps is less than declared steps")
	}
	for i, raw := range arr {
		x, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("step is not object")
		}
		id, _ := x["id"].(string)
		if _, ok := p.byID[id]; ok {
			return nil, fmt.Errorf("duplicate step id %s", id)
		}
		p.byID[id] = i
		s := step{ID: id}
		s.Kind, _ = x["kind"].(string)
		p.kinds[id] = s.Kind
		s.Tool, _ = x["tool"].(string)
		if a, ok := x["arguments"].(map[string]any); ok {
			s.Args = a
		}
		if r, ok := x["retry"].(map[string]any); ok {
			s.Retry.Max = numberInt(r["max_attempts"])
			s.Retry.When = map[string]bool{}
			if w, ok := r["when"].([]any); ok {
				for _, v := range w {
					s.Retry.When[v.(string)] = true
				}
			}
		} else {
			s.Retry.Max = 1
		}
		if c, ok := x["condition"].(map[string]any); ok {
			s.Cond.Ref, _ = c["ref"].(string)
			s.Cond.Op, _ = c["operator"].(string)
			s.Cond.Value = c["value"]
		}
		s.True, _ = x["on_true"].(string)
		s.False, _ = x["on_false"].(string)
		p.steps = append(p.steps, s)
		if s.Kind == "tool" {
			d, ok := tools[s.Tool]
			if !ok || !d.WorkflowCallable {
				return nil, fmt.Errorf("tool %q is not workflow callable", s.Tool)
			}
			if err := validateRefs(s.Args, p.inputs, p.byID, i); err != nil {
				return nil, err
			}
		} else if s.Kind == "decision" {
			if !refPattern.MatchString(s.Cond.Ref) {
				return nil, fmt.Errorf("invalid decision reference")
			}
			if err := validateRef(s.Cond.Ref, p.inputs, p.byID, i); err != nil {
				return nil, err
			}
		}
	}
	for i, s := range p.steps {
		if s.Kind == "decision" {
			for _, target := range []string{s.True, s.False} {
				if target == "stop" {
					continue
				}
				j, ok := p.byID[target]
				if !ok || j <= i {
					return nil, fmt.Errorf("decision %s target is not later", s.ID)
				}
			}
		}
		if err := validateResultReferenceKinds(s.Args, p.kinds); err != nil {
			return nil, err
		}
		if s.Kind == "tool" {
			if err := validateToolArgumentTemplate(tools[s.Tool], s.Args); err != nil {
				return nil, fmt.Errorf("step %s arguments: %w", s.ID, err)
			}
		}
		if s.Kind == "decision" {
			if err := validateResultReferenceKinds(s.Cond.Ref, p.kinds); err != nil {
				return nil, err
			}
		}
	}
	p.reachable = reachable(p)
	p.maxCalls = maxPathToolCalls(p, tools, 0, map[int]int{})
	if p.maxCalls > p.limits.maxCalls {
		return nil, errors.New("max_tool_calls is below conservative reachable calls")
	}
	if !hasReachableReturn(p) {
		return nil, errors.New("workflow has no reachable return")
	}
	if !allPathsTerminate(p, 0, map[int]bool{}) {
		return nil, errors.New("workflow has a reachable path without return or stop")
	}
	for _, o := range doc["outputs"].(map[string]any) {
		if err := validateOutputBindings(o); err != nil {
			return nil, err
		}
		if err := validateRefs(o, p.inputs, p.byID, len(p.steps)); err != nil {
			return nil, err
		}
		if err := validateResultReferenceKinds(o, p.kinds); err != nil {
			return nil, err
		}
		for _, stepID := range resultReferenceSteps(o) {
			if !resultAvailableOnEveryReturn(p, p.byID[stepID], 0, false) {
				return nil, fmt.Errorf("output result from step %s is not available on every return path", stepID)
			}
		}
	}
	return p, nil
}
func numberInt(v any) int {
	if n, ok := v.(json.Number); ok {
		i, _ := strconv.Atoi(string(n))
		return i
	}
	return 0
}
func validateInputValue(name string, in inputSpec, v any) error {
	if !valueType(in.Type, v) {
		return fmt.Errorf("input %s has wrong type", name)
	}
	for _, x := range in.Enum {
		if equalJSON(x, v) {
			goto bounds
		}
	}
	if len(in.Enum) > 0 {
		return fmt.Errorf("input %s is not in enum", name)
	}
bounds:
	if in.Type == "string" {
		s := v.(string)
		if in.MinLen != nil && len([]rune(s)) < *in.MinLen || in.MaxLen != nil && len([]rune(s)) > *in.MaxLen {
			return fmt.Errorf("input %s violates length bounds", name)
		}
	} else if in.Type == "integer" || in.Type == "number" {
		f := toFloat(v)
		if in.Min != nil && f < *in.Min || in.Max != nil && f > *in.Max {
			return fmt.Errorf("input %s violates numeric bounds", name)
		}
	}
	return nil
}
func valueType(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		switch n := v.(type) {
		case json.Number:
			_, err := strconv.ParseInt(string(n), 10, 64)
			return err == nil
		case float64:
			return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
		case float32:
			value := float64(n)
			return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	case "number":
		return isNumber(v)
	}
	return false
}
func isNumber(v any) bool {
	switch v.(type) {
	case json.Number, float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}
func toFloat(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err == nil {
			return f
		}
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint8:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	}
	return math.NaN()
}
func equalJSON(a, b any) bool {
	ba, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ba, bb)
}

func validateRefs(v any, ins map[string]inputSpec, ids map[string]int, cur int) error {
	switch x := v.(type) {
	case string:
		if refPattern.MatchString(x) {
			return validateRef(x, ins, ids, cur)
		}
		for _, m := range embeddedRefPattern.FindAllStringSubmatch(x, -1) {
			if err := validateRef(m[0], ins, ids, cur); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, z := range x {
			if err := validateRefs(z, ins, ids, cur); err != nil {
				return err
			}
		}
	case []any:
		for _, z := range x {
			if err := validateRefs(z, ins, ids, cur); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateRef(r string, ins map[string]inputSpec, ids map[string]int, cur int) error {
	m := refPattern.FindStringSubmatch(r)
	if m == nil {
		return errors.New("invalid reference")
	}
	if m[2] != "" {
		if _, ok := ins[m[2]]; !ok {
			return fmt.Errorf("unknown input %s", m[2])
		}
		return nil
	}
	j, ok := ids[m[3]]
	if !ok || j >= cur {
		return fmt.Errorf("future or unknown step reference %s", r)
	}
	return nil
}

func resultReferenceSteps(value any) []string {
	seen := map[string]bool{}
	var collect func(any)
	collect = func(current any) {
		switch typed := current.(type) {
		case string:
			for _, match := range embeddedRefPattern.FindAllStringSubmatch(typed, -1) {
				whole := refPattern.FindStringSubmatch(match[0])
				if whole != nil && whole[5] != "" {
					seen[whole[3]] = true
				}
			}
		case map[string]any:
			for _, child := range typed {
				collect(child)
			}
		case []any:
			for _, child := range typed {
				collect(child)
			}
		}
	}
	collect(value)
	steps := make([]string, 0, len(seen))
	for stepID := range seen {
		steps = append(steps, stepID)
	}
	return steps
}

func resultAvailableOnEveryReturn(p *plan, requiredStep, index int, executed bool) bool {
	if index < 0 || index >= len(p.steps) {
		return false
	}
	if index == requiredStep {
		executed = true
	}
	step := p.steps[index]
	if step.Kind == "return" {
		return executed
	}
	if step.Kind == "decision" {
		branchAvailable := func(target string) bool {
			return target == "stop" || resultAvailableOnEveryReturn(p, requiredStep, p.byID[target], executed)
		}
		return branchAvailable(step.True) && branchAvailable(step.False)
	}
	return resultAvailableOnEveryReturn(p, requiredStep, index+1, executed)
}

func validateOutputBindings(value any) error {
	switch typed := value.(type) {
	case string:
		matches := embeddedRefPattern.FindAllStringSubmatch(typed, -1)
		if whole := refPattern.FindStringSubmatch(typed); whole != nil {
			matches = [][]string{whole}
		}
		if len(matches) == 0 {
			return errors.New("output value must reference an input or tool result")
		}
		for _, match := range matches {
			whole := refPattern.FindStringSubmatch(match[0])
			if whole == nil || whole[2] == "" && whole[5] == "" {
				return errors.New("output value may reference only inputs or tool results")
			}
		}
		return nil
	case map[string]any:
		if len(typed) == 0 {
			return errors.New("output object must contain bindings")
		}
		for _, child := range typed {
			if err := validateOutputBindings(child); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if len(typed) == 0 {
			return errors.New("output array must contain bindings")
		}
		for _, child := range typed {
			if err := validateOutputBindings(child); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("output value must be a binding")
	}
}

func validateResultReferenceKinds(value any, kinds map[string]string) error {
	switch typed := value.(type) {
	case string:
		matches := embeddedRefPattern.FindAllStringSubmatch(typed, -1)
		if whole := refPattern.FindStringSubmatch(typed); whole != nil {
			matches = [][]string{whole}
		}
		for _, match := range matches {
			if match == nil {
				continue
			}
			whole := refPattern.FindStringSubmatch(match[0])
			if whole != nil && whole[5] != "" && kinds[whole[3]] != "tool" {
				return fmt.Errorf("result reference %s must target a tool step", match[0])
			}
		}
	case map[string]any:
		for _, child := range typed {
			if err := validateResultReferenceKinds(child, kinds); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateResultReferenceKinds(child, kinds); err != nil {
				return err
			}
		}
	}
	return nil
}
func reachable(p *plan) map[int]bool {
	r := map[int]bool{}
	var walk func(int)
	walk = func(i int) {
		if i < 0 || i >= len(p.steps) || r[i] {
			return
		}
		r[i] = true
		s := p.steps[i]
		if s.Kind == "decision" {
			if s.True != "stop" {
				walk(p.byID[s.True])
			}
			if s.False != "stop" {
				walk(p.byID[s.False])
			}
		} else if s.Kind != "return" {
			walk(i + 1)
		}
	}
	walk(0)
	return r
}
func hasReachableReturn(p *plan) bool {
	for i := range p.steps {
		if p.reachable[i] && p.steps[i].Kind == "return" {
			return true
		}
	}
	return false
}

func maxPathToolCalls(p *plan, tools map[string]ToolDefinition, index int, memo map[int]int) int {
	if index < 0 || index >= len(p.steps) {
		return 0
	}
	if calls, ok := memo[index]; ok {
		return calls
	}
	step := p.steps[index]
	if step.Kind == "return" {
		memo[index] = 0
		return 0
	}
	if step.Kind == "decision" {
		branchCalls := func(target string) int {
			if target == "stop" {
				return 0
			}
			return maxPathToolCalls(p, tools, p.byID[target], memo)
		}
		trueCalls := branchCalls(step.True)
		falseCalls := branchCalls(step.False)
		if falseCalls > trueCalls {
			trueCalls = falseCalls
		}
		memo[index] = trueCalls
		return trueCalls
	}
	attempts := step.Retry.Max
	if attempts > p.limits.maxAttempts {
		attempts = p.limits.maxAttempts
	}
	if !tools[step.Tool].RetrySafe {
		attempts = 1
	}
	calls := attempts + maxPathToolCalls(p, tools, index+1, memo)
	memo[index] = calls
	return calls
}

func allPathsTerminate(p *plan, index int, memo map[int]bool) bool {
	if index < 0 || index >= len(p.steps) {
		return false
	}
	if terminated, ok := memo[index]; ok {
		return terminated
	}
	step := p.steps[index]
	if step.Kind == "return" {
		memo[index] = true
		return true
	}
	if step.Kind == "decision" {
		branchTerminates := func(target string) bool {
			return target == "stop" || allPathsTerminate(p, p.byID[target], memo)
		}
		terminated := branchTerminates(step.True) && branchTerminates(step.False)
		memo[index] = terminated
		return terminated
	}
	terminated := allPathsTerminate(p, index+1, memo)
	memo[index] = terminated
	return terminated
}

// RunResult is the structured outcome of a workflow run.
type RunResult struct {
	RunID          string         `json:"run_id"`
	Status         string         `json:"status"`
	Outputs        map[string]any `json:"outputs,omitempty"`
	Failure        *Failure       `json:"failure,omitempty"`
	Error          string         `json:"error,omitempty"`
	LogPath        string         `json:"log_path,omitempty"`
	LogUnavailable bool           `json:"log_unavailable,omitempty"`
}

func bounded(s string) string {
	if len(s) > 2048 {
		return s[:2048]
	}
	return s
}

func (e *Engine) finalizePreflightFailure(runID, name, outcome string, result RunResult) RunResult {
	if !namePattern.MatchString(name) {
		return result
	}
	writer, err := e.openEventWriter(runID, name)
	if err != nil {
		e.closeMu.RLock()
		closed := e.closed
		e.closeMu.RUnlock()
		if closed {
			return result
		}
		return RunResult{RunID: runID, Status: "failed", Error: "log_unavailable", LogUnavailable: true}
	}
	defer writer.abort()
	if err := writer.append(eventBase(runID, name, "workflow_started", "started", 0), false); err != nil {
		return RunResult{RunID: runID, Status: "failed", Error: "log_unavailable", LogUnavailable: true}
	}
	terminal := eventBase(runID, name, "workflow_completed", "failed", 1)
	terminal["outcome_category"] = outcome
	terminal["summary"] = "workflow failed before execution"
	if err := writer.append(terminal, true); err != nil {
		return RunResult{RunID: runID, Status: "failed", Error: "log_unavailable", LogUnavailable: true}
	}
	logPath, err := writer.finalize(runID)
	if err != nil {
		return RunResult{RunID: runID, Status: "failed", Error: "log_unavailable", LogUnavailable: true}
	}
	writer.close()
	result.LogPath = logPath
	return result
}

func (e *Engine) Run(ctx context.Context, name string, supplied map[string]any, authorize Authorizer) RunResult {
	if ctx == nil {
		return RunResult{Status: "failed", Error: "nil context"}
	}
	runID, err := newRunID()
	if err != nil {
		return RunResult{Status: "failed", Error: err.Error()}
	}
	p, err := e.validate(name)
	if err != nil {
		result := RunResult{RunID: runID, Status: "failed", Error: err.Error()}
		return e.finalizePreflightFailure(runID, name, "validation", result)
	}
	failureResult := func(status, code string, failure error) RunResult {
		message := bounded(failure.Error())
		result := RunResult{RunID: runID, Status: status, Error: message}
		if p.onFailure == "report" {
			result.Failure = &Failure{Code: code, Message: message}
		}
		return result
	}
	inputs, err := resolveInputs(p.inputs, supplied)
	if err != nil {
		result := failureResult("failed", "validation", err)
		return e.finalizePreflightFailure(runID, name, "validation", result)
	}
	if authorize == nil && p.maxCalls > 0 {
		result := failureResult("failed", "denied", errors.New("authorizer is required"))
		return e.finalizePreflightFailure(runID, name, "denied", result)
	}
	writer, err := e.openEventWriter(runID, name)
	if err != nil {
		return RunResult{RunID: runID, Status: "failed", Error: "log_unavailable", LogUnavailable: true}
	}
	defer writer.abort()
	sequence := int64(0)
	emit := func(event map[string]any, terminal bool) error {
		event["sequence"] = sequence
		if err := writer.append(event, terminal); err != nil {
			return err
		}
		sequence++
		return nil
	}
	logUnavailable := func() RunResult {
		writer.abort()
		return RunResult{RunID: runID, Status: "failed", Error: "log_unavailable", LogUnavailable: true}
	}
	finalizeLogLimit := func() RunResult {
		result := failureResult("failed", "log_limit", errLogLimit)
		event := eventBase(runID, name, "workflow_completed", "failed", sequence)
		event["summary"] = bounded(errLogLimit.Error())
		event["outcome_category"] = "log_limit"
		if err := emit(event, true); err != nil {
			return logUnavailable()
		}
		logPath, err := writer.finalize(runID)
		if err != nil {
			return logUnavailable()
		}
		writer.close()
		result.LogPath = logPath
		return result
	}
	handleLogError := func(err error) RunResult {
		if errors.Is(err, errLogLimit) {
			return finalizeLogLimit()
		}
		return logUnavailable()
	}
	if err := emit(eventBase(runID, name, "workflow_started", "started", sequence), false); err != nil {
		return handleLogError(err)
	}

	state := map[string]map[string]any{}
	retainedResultBytes := 0
	executed := make([]bool, len(p.steps))
	skipped := make([]bool, len(p.steps))
	recordSkipped := func(index int) error {
		if index < 0 || index >= len(p.steps) || executed[index] || skipped[index] {
			return nil
		}
		skipped[index] = true
		step := p.steps[index]
		state[step.ID] = map[string]any{"status": "skipped", "attempt": 1}
		event := eventBase(runID, name, "step_skipped", "skipped", sequence)
		event["step_id"] = step.ID
		event["attempt"] = 1
		return emit(event, false)
	}
	finish := func(result RunResult, terminalEvent, outcome string) RunResult {
		for index := range p.steps {
			if err := recordSkipped(index); err != nil {
				return handleLogError(err)
			}
		}
		event := eventBase(runID, name, terminalEvent, result.Status, sequence)
		if result.Error != "" {
			event["summary"] = "workflow " + result.Status
		}
		event["outcome_category"] = outcome
		if err := emit(event, true); err != nil {
			return handleLogError(err)
		}
		logPath, err := writer.finalize(runID)
		if err != nil {
			return logUnavailable()
		}
		writer.close()
		result.LogPath = logPath
		return result
	}
	failStep := func(step step, attempt int, code string, failure error) RunResult {
		event := eventBase(runID, name, "step_failed", "failed", sequence)
		event["step_id"] = step.ID
		event["attempt"] = attempt
		event["outcome_category"] = code
		event["summary"] = "step failed: " + code
		if err := emit(event, false); err != nil {
			return handleLogError(err)
		}
		return finish(failureResult("failed", code, failure), "workflow_completed", code)
	}

	deadline, cancel := context.WithTimeout(ctx, time.Duration(p.limits.timeout)*time.Second)
	defer cancel()
	index, calls, stepsRun := 0, 0, 0
	for index < len(p.steps) {
		if err := deadline.Err(); err != nil {
			status := statusForContext(ctx, deadline)
			terminalEvent := "workflow_completed"
			code := "timeout"
			if status == "cancelled" {
				terminalEvent = "workflow_cancelled"
				code = "cancelled"
			}
			return finish(failureResult(status, code, err), terminalEvent, code)
		}
		stepsRun++
		if stepsRun > p.limits.maxSteps {
			return finish(failureResult("failed", "validation", errors.New("step limit exceeded")), "workflow_completed", "validation")
		}
		current := p.steps[index]
		executed[index] = true
		started := eventBase(runID, name, "step_started", "started", sequence)
		started["step_id"] = current.ID
		started["attempt"] = 1
		if err := emit(started, false); err != nil {
			return handleLogError(err)
		}

		switch current.Kind {
		case "tool":
			resolved, missing, err := resolveValue(current.Args, inputs, state, true)
			if err != nil {
				return failStep(current, 1, "validation", err)
			}
			if missing {
				return failStep(current, 1, "validation", errors.New("missing tool argument"))
			}
			arguments, ok := resolved.(map[string]any)
			if !ok {
				return failStep(current, 1, "validation", errors.New("resolved tool arguments are not an object"))
			}
			if len(mustJSON(arguments)) > maxWorkflowFile {
				return failStep(current, 1, "validation", errors.New("resolved arguments exceed cap"))
			}
			definition := e.catalog[current.Tool]
			if err := e.toolSchemas[current.Tool].Validate(arguments); err != nil {
				return failStep(current, 1, "validation", err)
			}
			attempts := current.Retry.Max
			if attempts > p.limits.maxAttempts {
				attempts = p.limits.maxAttempts
			}
			if !definition.RetrySafe {
				attempts = 1
			}
			var lastErr error
			lastAttempt := 1
			for attempt := 1; attempt <= attempts; attempt++ {
				lastAttempt = attempt
				if err := deadline.Err(); err != nil {
					lastErr = err
					break
				}
				authorizationValue, err := cloneJSONValue(arguments)
				if err != nil {
					return failStep(current, attempt, "validation", errors.New("tool arguments could not be isolated"))
				}
				authorizationArgs, ok := authorizationValue.(map[string]any)
				if !ok {
					return failStep(current, attempt, "validation", errors.New("tool arguments are not an object"))
				}
				permission := eventBase(runID, name, "permission_requested", "allowed", sequence)
				permission["step_id"] = current.ID
				permission["attempt"] = attempt
				permission["tool"] = current.Tool
				if !authorizeAttempt(deadline, authorize, current.Tool, authorizationArgs, current.ID, attempt) {
					permission["status"] = "denied"
					if err := emit(permission, false); err != nil {
						return handleLogError(err)
					}
					if ctx.Err() != nil {
						return finish(failureResult("cancelled", "cancelled", ctx.Err()), "workflow_cancelled", "cancelled")
					}
					if deadline.Err() != nil {
						return finish(failureResult("timed_out", "timeout", deadline.Err()), "workflow_completed", "timeout")
					}
					return failStep(current, attempt, "denied", errors.New("tool attempt denied"))
				}
				if err := emit(permission, false); err != nil {
					return handleLogError(err)
				}
				if ctx.Err() != nil {
					return finish(failureResult("cancelled", "cancelled", ctx.Err()), "workflow_cancelled", "cancelled")
				}
				if deadline.Err() != nil {
					return finish(failureResult("timed_out", "timeout", deadline.Err()), "workflow_completed", "timeout")
				}
				if calls >= p.limits.maxCalls {
					return failStep(current, attempt, "validation", errors.New("tool call limit exceeded"))
				}
				calls++
				executionValue, err := cloneJSONValue(arguments)
				if err != nil {
					return failStep(current, attempt, "validation", errors.New("tool arguments could not be isolated"))
				}
				executionArgs, ok := executionValue.(map[string]any)
				if !ok {
					return failStep(current, attempt, "validation", errors.New("tool arguments are not an object"))
				}
				raw, callErr := e.executor.ExecuteContext(deadline, current.Tool, executionArgs)
				status, outcome := toolOutcome(callErr, ctx, deadline)
				if callErr == nil && len(raw) > maxResultBytes {
					callErr = errors.New("retained result exceeds cap")
					status, outcome = "failed", "tool_error"
				}
				var parsed map[string]any
				if callErr == nil {
					parsed, _ = parseResult(raw)
					parsedBytes := len(mustJSON(parsed))
					if parsedBytes > maxResultBytes {
						callErr = errors.New("parsed result exceeds cap")
						status, outcome = "failed", "tool_error"
					} else if retainedResultBytes > maxResultBytes-parsedBytes {
						callErr = errors.New("retained workflow results exceed cap")
						status, outcome = "failed", "tool_error"
					} else {
						retainedResultBytes += parsedBytes
					}
				}
				completed := eventBase(runID, name, "tool_completed", status, sequence)
				completed["step_id"] = current.ID
				completed["attempt"] = attempt
				completed["tool"] = current.Tool
				completed["outcome_category"] = outcome
				if callErr != nil {
					completed["summary"] = "tool attempt " + outcome
				}
				if err := emit(completed, false); err != nil {
					return handleLogError(err)
				}
				if callErr == nil {
					state[current.ID] = map[string]any{"result": parsed, "status": "succeeded", "attempt": attempt}
					lastErr = nil
					break
				}
				lastErr = callErr
				if ctx.Err() != nil {
					return finish(failureResult("cancelled", "cancelled", ctx.Err()), "workflow_cancelled", "cancelled")
				}
				if deadline.Err() != nil {
					return finish(failureResult("timed_out", "timeout", deadline.Err()), "workflow_completed", "timeout")
				}
				if retryable(callErr, current.Retry.When, deadline, ctx) && attempt < attempts {
					retried := eventBase(runID, name, "step_retried", "retrying", sequence)
					retried["step_id"] = current.ID
					retried["attempt"] = attempt
					retried["tool"] = current.Tool
					retried["outcome_category"] = outcome
					if err := emit(retried, false); err != nil {
						return handleLogError(err)
					}
					continue
				}
				break
			}
			if lastErr != nil {
				if ctx.Err() != nil {
					return finish(failureResult("cancelled", "cancelled", ctx.Err()), "workflow_cancelled", "cancelled")
				}
				if deadline.Err() != nil {
					return finish(failureResult("timed_out", "timeout", deadline.Err()), "workflow_completed", "timeout")
				}
				_, outcome := toolOutcome(lastErr, ctx, deadline)
				return failStep(current, lastAttempt, outcome, lastErr)
			}
		case "decision":
			value, found, err := resolveValueFound(current.Cond.Ref, inputs, state)
			if err != nil {
				return failStep(current, 1, "validation", err)
			}
			matched, err := compareFound(current.Cond.Op, value, found, current.Cond.Value)
			if err != nil {
				return failStep(current, 1, "validation", err)
			}
			state[current.ID] = map[string]any{"status": "succeeded", "attempt": 1}
			target := current.False
			if matched {
				target = current.True
			}
			if target == "stop" {
				return finish(failureResult("failed", "validation", errors.New("decision stopped workflow")), "workflow_completed", "validation")
			}
			targetIndex := p.byID[target]
			for skippedIndex := index + 1; skippedIndex < targetIndex; skippedIndex++ {
				if err := recordSkipped(skippedIndex); err != nil {
					return handleLogError(err)
				}
			}
			index = targetIndex
			continue
		case "return":
			outputs := map[string]any{}
			for key, value := range p.doc["outputs"].(map[string]any) {
				resolved, missing, err := resolveValue(value, inputs, state, false)
				if err != nil || missing {
					return failStep(current, 1, "validation", errors.New("output resolution failed"))
				}
				outputs[key] = resolved
			}
			if len(mustJSON(outputs)) > maxResultBytes {
				return failStep(current, 1, "validation", errors.New("outputs exceed cap"))
			}
			state[current.ID] = map[string]any{"status": "succeeded", "attempt": 1}
			return finish(RunResult{RunID: runID, Status: "succeeded", Outputs: outputs}, "workflow_completed", "success")
		}
		index++
	}
	return finish(failureResult("failed", "validation", errors.New("workflow terminated without return")), "workflow_completed", "validation")
}

func authorizeAttempt(ctx context.Context, authorize Authorizer, toolName string, args map[string]any, stepID string, attempt int) bool {
	if ctx.Err() != nil {
		return false
	}
	allowed := authorize(ctx, toolName, args, stepID, attempt)
	return allowed && ctx.Err() == nil
}

func toolOutcome(err error, caller context.Context, workflowContext context.Context) (string, string) {
	if err == nil {
		return "succeeded", "success"
	}
	if caller.Err() != nil {
		return "cancelled", "cancelled"
	}
	if workflowContext.Err() != nil {
		return "timed_out", "timeout"
	}
	var timeout HandlerTimeout
	if errors.As(err, &timeout) {
		return "timed_out", "timeout"
	}
	return "failed", "tool_error"
}

func statusForContext(c context.Context, w context.Context) string {
	if c.Err() != nil {
		return "cancelled"
	}
	if w.Err() != nil {
		return "timed_out"
	}
	return "failed"
}
func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "oaw_" + hex.EncodeToString(b[:]), nil
}
func resolveInputs(spec map[string]inputSpec, sup map[string]any) (map[string]any, error) {
	if sup == nil {
		sup = map[string]any{}
	}
	encoded, err := json.Marshal(sup)
	if err != nil {
		return nil, fmt.Errorf("supplied inputs are not valid JSON: %w", err)
	}
	if len(encoded) > maxInputBytes {
		return nil, errors.New("supplied inputs too large")
	}
	for k := range sup {
		if _, ok := spec[k]; !ok {
			return nil, fmt.Errorf("unknown input %s", k)
		}
	}
	out := map[string]any{}
	for k, s := range spec {
		v, ok := sup[k]
		if !ok && s.HasDefault {
			v = s.Default
			ok = true
		}
		if !ok {
			if s.Required {
				return nil, fmt.Errorf("missing required input %s", k)
			}
			continue
		}
		if err := validateInputValue(k, s, v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func resolveValue(v any, inputs map[string]any, state map[string]map[string]any, wholeOptional bool) (any, bool, error) {
	switch x := v.(type) {
	case string:
		m := refPattern.FindStringSubmatch(x)
		if m != nil {
			z, ok, err := lookupRef(m, inputs, state)
			if err != nil {
				return nil, false, err
			}
			if !ok && wholeOptional {
				return nil, true, nil
			}
			if !ok {
				return nil, false, errors.New("missing reference")
			}
			return z, false, nil
		}
		var firstErr error
		out := embeddedRefPattern.ReplaceAllStringFunc(x, func(s string) string {
			z, ok, err := lookupRef(refPattern.FindStringSubmatch(s), inputs, state)
			if err != nil {
				firstErr = err
				return ""
			}
			if !ok {
				firstErr = errors.New("missing reference")
				return ""
			}
			b, _ := json.Marshal(z)
			if len(b) >= 2 && b[0] == '"' {
				var text string
				if json.Unmarshal(b, &text) == nil {
					return text
				}
			}
			return string(b)
		})
		if firstErr != nil {
			return nil, false, firstErr
		}
		return out, false, nil
	case map[string]any:
		out := map[string]any{}
		for k, z := range x {
			rv, miss, err := resolveValue(z, inputs, state, true)
			if err != nil {
				return nil, false, err
			}
			if !miss {
				out[k] = rv
			}
		}
		return out, false, nil
	case []any:
		out := make([]any, len(x))
		for i, z := range x {
			rv, miss, err := resolveValue(z, inputs, state, false)
			if err != nil || miss {
				return nil, false, errors.New("missing reference in array")
			}
			out[i] = rv
		}
		return out, false, nil
	}
	return v, false, nil
}
func resolveValueFound(ref string, inputs map[string]any, state map[string]map[string]any) (any, bool, error) {
	m := refPattern.FindStringSubmatch(ref)
	if m == nil {
		return nil, false, errors.New("invalid reference")
	}
	return lookupRef(m, inputs, state)
}
func compareFound(op string, a any, found bool, b any) (bool, error) {
	if op == "exists" {
		return found, nil
	}
	if !found {
		return false, errors.New("missing reference")
	}
	return compare(op, a, b)
}

func lookupRef(m []string, inputs map[string]any, state map[string]map[string]any) (any, bool, error) {
	if m == nil {
		return nil, false, errors.New("invalid reference")
	}
	if m[2] != "" {
		v, ok := inputs[m[2]]
		return v, ok, nil
	}
	st, ok := state[m[3]]
	if !ok {
		return nil, false, nil
	}
	if m[5] != "" {
		var v any = st["result"]
		for _, p := range strings.Split(m[5], ".") {
			o, ok := v.(map[string]any)
			if !ok {
				return nil, false, nil
			}
			v, ok = o[p]
			if !ok {
				return nil, false, nil
			}
		}
		return v, true, nil
	}
	return st[m[4]], true, nil
}
func parseResult(raw string) (map[string]any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		if o, ok := v.(map[string]any); ok {
			return o, nil
		}
		return map[string]any{"text": raw}, nil
	}
	return map[string]any{"text": raw}, nil
}
func retryable(err error, when map[string]bool, w, c context.Context) bool {
	if c.Err() != nil || w.Err() != nil {
		return false
	}
	var te ToolError
	var ht HandlerTimeout
	if errors.As(err, &te) {
		return when["tool_error"]
	}
	if errors.As(err, &ht) {
		return when["timeout"]
	}
	return false
}

func compileToolSchema(definition ToolDefinition) (*jsonschema.Schema, error) {
	if definition.Schema == nil {
		return nil, errors.New("tool argument schema is required")
	}
	if err := rejectExternalRefs(definition.Schema); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	resource := "tool://" + definition.Name
	if err := compiler.AddResource(resource, definition.Schema); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func validateToolArgumentTemplate(definition ToolDefinition, args map[string]any) error {
	if args == nil {
		return errors.New("tool arguments are required")
	}
	schemaObject, ok := definition.Schema.(map[string]any)
	if !ok {
		return errors.New("tool argument schema must be an object")
	}
	properties, ok := schemaObject["properties"].(map[string]any)
	if !ok {
		return errors.New("tool argument schema properties are required")
	}
	if required, ok := schemaObject["required"].([]any); ok {
		for _, rawName := range required {
			name, ok := rawName.(string)
			if !ok {
				return errors.New("tool argument schema required list is invalid")
			}
			if _, exists := args[name]; !exists {
				return fmt.Errorf("missing required argument %s", name)
			}
		}
	}
	for name, value := range args {
		propertySchema, exists := properties[name]
		if !exists {
			return fmt.Errorf("unknown argument %s", name)
		}
		if containsReference(value) {
			continue
		}
		propertyObject, ok := propertySchema.(map[string]any)
		if !ok {
			return fmt.Errorf("argument %s schema is invalid", name)
		}
		if expected, _ := propertyObject["type"].(string); expected != "" && !valueType(expected, value) {
			return fmt.Errorf("argument %s has wrong literal type", name)
		}
		if enum, ok := propertyObject["enum"].([]any); ok && len(enum) > 0 {
			matched := false
			for _, allowed := range enum {
				if equalJSON(value, allowed) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("argument %s is not in enum", name)
			}
		}
	}
	return nil
}

func containsReference(value any) bool {
	switch typed := value.(type) {
	case string:
		return embeddedRefPattern.MatchString(typed)
	case map[string]any:
		for _, child := range typed {
			if containsReference(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsReference(child) {
				return true
			}
		}
	}
	return false
}
func compare(op string, a, b any) (bool, error) {
	if op == "exists" {
		return a != nil, nil
	}
	switch op {
	case "eq":
		return equalJSON(a, b), nil
	case "neq":
		return !equalJSON(a, b), nil
	case "contains":
		switch x := a.(type) {
		case string:
			y, ok := b.(string)
			if !ok {
				return false, errors.New("contains requires string")
			}
			return strings.Contains(x, y), nil
		case []any:
			for _, z := range x {
				if equalJSON(z, b) {
					return true, nil
				}
			}
			return false, nil
		default:
			return false, errors.New("contains requires string or array")
		}
	case "in":
		x, ok := b.([]any)
		if !ok {
			return false, errors.New("in requires array")
		}
		for _, z := range x {
			if equalJSON(a, z) {
				return true, nil
			}
		}
		return false, nil
	case "gt", "gte", "lt", "lte":
		x, y := toFloat(a), toFloat(b)
		if math.IsNaN(x) || math.IsNaN(y) {
			return false, errors.New("ordered comparison requires numbers")
		}
		switch op {
		case "gt":
			return x > y, nil
		case "gte":
			return x >= y, nil
		case "lt":
			return x < y, nil
		default:
			return x <= y, nil
		}
	}
	return false, errors.New("unsupported operator")
}

func validComponent(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.ContainsRune(p, filepath.Separator) && !strings.ContainsRune(p, '/')
}

func openNoFollowDir(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if f == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("cannot open directory")
	}
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		_ = f.Close()
		return nil, errors.New("path component is not directory")
	}
	return f, nil
}

func (e *Engine) openRelativeDir(parts []string) (*os.File, error) {
	if len(parts) == 0 {
		return nil, errors.New("empty path")
	}
	f, err := e.duplicateRoot()
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		_ = f.Close()
		return nil, errors.New("root is not a directory")
	}
	for _, p := range parts {
		if !validComponent(p) {
			_ = f.Close()
			return nil, errors.New("invalid path component")
		}
		n, err := openNoFollowDir(f, p)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		f = n
	}
	return f, nil
}
func (e *Engine) readRelative(parts []string, max int) ([]byte, error) {
	if len(parts) < 1 {
		return nil, errors.New("empty path")
	}
	for _, p := range parts {
		if !validComponent(p) {
			return nil, errors.New("invalid path component")
		}
	}
	d, err := e.openRelativeDir(parts[:len(parts)-1])
	if err != nil {
		return nil, err
	}
	defer d.Close()
	fd, err := unix.Openat(int(d.Fd()), parts[len(parts)-1], syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), parts[len(parts)-1])
	if f == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("cannot open file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("file is not regular")
	}
	if info.Size() > int64(max) {
		return nil, errors.New("file exceeds size limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		return nil, errors.New("file exceeds size limit")
	}
	return b, nil
}
