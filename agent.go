package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Capability func(context.Context, *Run) error
type ToolHandler func(context.Context, Call) (string, error)
type ToolMiddleware func(ToolHandler) ToolHandler

// Run is the per-invocation configuration. Hooks run in registration order.
// Tools execute sequentially in model order so later calls observe prior writes.
type Run struct {
	// FirstRequest is true during preparation of the first top-level model request.
	FirstRequest       bool
	promptWasFiltered  bool
	hasPromptFilter    bool
	self               Agent[string]
	childOptions       func(string, bool, bool) Options
	editAnsweredResult func(string, func(Call, string) (string, error)) error
	invoke             func(context.Context, Tool, Call) (string, error)
	// Authorize is the application policy inherited by delegated agents.
	Authorize     func(context.Context, Call) (Decision, error)
	Step          int
	saved         map[string]json.RawMessage
	bindings      map[string]any
	Prompt        string
	Model         Model
	Instructions  []string
	Limits        Limits
	PrepareCall   []func(Call) (Call, error)
	AfterResponse []func(context.Context, Response) error
	// TransformText sanitizes assistant text before history, output decoding and
	// observer delivery. Streaming deltas are held until transforms finish.
	TransformText []func(context.Context, string) (string, error)
	ReviewOutput  []func(context.Context, string) (string, error)
	CheckDelta    []func(context.Context, string) error
	// RewriteHistory processes durable conversation history before request-only hooks.
	// Return nil for no change. Processors receive an isolated copy.
	RewriteHistory []func(context.Context, Request, Usage) ([]Message, error)
	BeforeRequest  []func(context.Context, *Request, Usage) error
	AroundTool     []ToolMiddleware
	CheckOutput    []func(context.Context, string) error
	OnClose        []func() error
	tools          map[string]Tool
	defs           []Definition
}

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (r *Run) Register(tools ...Tool) error {
	for _, t := range tools {
		if !toolName.MatchString(t.Name) || t.Execute == nil || !json.Valid(t.Schema) {
			return fmt.Errorf("invalid tool definition %q", t.Name)
		}
		if _, ok := r.tools[t.Name]; ok {
			return fmt.Errorf("duplicate tool %q", t.Name)
		}
		r.tools[t.Name] = t
		r.defs = append(r.defs, t.Definition)
	}
	return nil
}
func Tools(tools ...Tool) Capability {
	list := slices.Clone(tools)
	return func(_ context.Context, r *Run) error { return r.Register(list...) }
}
func Combine(caps ...Capability) Capability {
	list := slices.Clone(caps)
	return func(ctx context.Context, r *Run) error {
		for _, cap := range list {
			if cap == nil {
				return errors.New("nil capability")
			}
			if err := cap(ctx, r); err != nil {
				return err
			}
		}
		return nil
	}
}

type Limits struct {
	// Zero uses 50 requests. Other zero limits are disabled.
	Requests, ToolCalls, InputTokens, OutputTokens, TotalTokens int
}
type Agent[T any] struct {
	Model        Model
	Output       Output[T]
	Instructions string
	Capabilities []Capability
	Limits       Limits
	// MaxRetries defaults to one model correction. Set a negative value for none.
	MaxRetries int
}

// Decision is the application policy for one tool call.
type Decision uint8

const (
	Approve Decision = iota
	Deny
	Defer
)

var ErrDeferred = errors.New("tool call awaits approval")

type Event struct {
	// Delegates lists the child agents through which this event was forwarded.
	Delegates []string
	Kind      string
	Text      string
	Call      Call
	Usage     Usage
}
type Options struct {
	ancestors []usageAccount
	// Images accompany the new user prompt passed to Run. Resume uses the saved history.
	Images    []Image
	Authorize func(context.Context, Call) (Decision, error)
	History   []Message
	Observe   func(context.Context, Event) error
	// Checkpoint receives a complete turn before the next model request.
	Checkpoint func(context.Context, Snapshot) error
}
type Snapshot struct {
	PromptFiltered bool `json:"prompt_filtered,omitempty"`
	// RequestStep identifies the parent request that produced the pending calls.
	RequestStep      int                        `json:"request_step,omitempty"`
	Offered          []string                   `json:"offered,omitempty"`
	Pending          []Call                     `json:"pending,omitempty"`
	AwaitingApproval bool                       `json:"awaiting_approval,omitempty"`
	Correction       string                     `json:"correction,omitempty"`
	Complete         bool                       `json:"complete,omitempty"`
	Prompt           string                     `json:"prompt"`
	State            map[string]json.RawMessage `json:"state,omitempty"`
	History          []Message                  `json:"history"`
	Usage            Usage                      `json:"usage"`
	Retries          int                        `json:"retries"`
}
type Result[T any] struct {
	Output T
	Snapshot
}

func (a Agent[T]) Run(ctx context.Context, prompt string, opts Options) (Result[T], error) {
	return a.run(ctx, Start(prompt, opts.History, opts.Images...), opts, false)
}

// Start constructs the initial state of a conversation for Advance.
func Start(prompt string, previous []Message, images ...Image) Snapshot {
	history := append(cloneMessages(previous), Message{Role: User, Text: prompt, Images: cloneImages(images)})
	return Snapshot{Prompt: prompt, History: history}
}

// Advance runs at most one model request or one pending tool call. Persist the
// returned snapshot before invoking it again. Like Resume, it reinstalls
// capabilities from saved state and closes run resources before returning.
func (a Agent[T]) Advance(ctx context.Context, s Snapshot, opts Options) (Result[T], error) {
	return a.run(ctx, s, opts, true)
}

// Resume continues from a saved snapshot. Run-scoped capabilities are
// installed again. Applications can supply durable capability stores.
func (a Agent[T]) Resume(ctx context.Context, s Snapshot, opts Options) (Result[T], error) {
	return a.run(ctx, s, opts, false)
}

// execution owns a single run, including any tool batch in progress.
type execution[T any] struct {
	agent       Agent[T]
	run         *Run
	opts        Options
	result      Result[T]
	handler     ToolHandler
	singleStep  bool
	nestedDepth int
}

func (a Agent[T]) run(ctx context.Context, s Snapshot, opts Options, singleStep bool) (result Result[T], err error) {
	s.History = cloneMessages(s.History)
	result.Snapshot = s
	if ctx == nil || a.Model == nil || a.Output.Decode == nil {
		return result, errors.New("context, model and output decoder are required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if s.Complete {
		if len(s.Pending) > 0 || len(s.History) == 0 || s.History[len(s.History)-1].Role != Assistant {
			return result, errors.New("invalid completed snapshot")
		}
		result.Output, err = a.Output.Decode(s.History[len(s.History)-1].Text)
		return result, err
	}
	if s.AwaitingApproval && len(s.Pending) == 0 {
		return result, errors.New("snapshot missing deferred call")
	}
	s.Pending = slices.Clone(s.Pending)
	if a.Limits.Requests == 0 {
		a.Limits.Requests = 50
	}
	r := &Run{promptWasFiltered: s.PromptFiltered, Authorize: opts.Authorize, Limits: a.Limits, Prompt: s.Prompt, Model: a.Model, tools: map[string]Tool{}, saved: s.State, bindings: map[string]any{}}
	r.self = Agent[string]{Instructions: a.Instructions, Capabilities: slices.Clone(a.Capabilities), Limits: a.Limits, MaxRetries: a.MaxRetries, Output: Output[string]{Schema: slices.Clone(a.Output.Schema), Decode: func(text string) (string, error) {
		_, err := a.Output.Decode(text)
		return text, err
	}}}
	if a.Instructions != "" {
		r.Instructions = []string{a.Instructions}
	}
	defer func() {
		for _, close := range slices.Backward(r.OnClose) {
			if e := close(); e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	if err := Combine(a.Capabilities...)(ctx, r); err != nil {
		return result, err
	}
	s.PromptFiltered = s.PromptFiltered || r.hasPromptFilter
	if r.Prompt != s.Prompt {
		for i := len(s.History) - 1; i >= 0; i-- {
			m := &s.History[i]
			if m.Role == User && m.ContextKind == "" && m.Text == s.Prompt {
				m.Text = r.Prompt
				break
			}
		}
		s.Prompt = r.Prompt
	}
	if r.Model == nil {
		return result, errors.New("capabilities selected a nil model")
	}
	a.Limits = r.Limits
	if a.MaxRetries == 0 {
		a.MaxRetries = 1
	}
	x := execution[T]{agent: a, run: r, opts: opts, result: Result[T]{Snapshot: s}, handler: r.execute, singleStep: singleStep}
	r.childOptions = x.childOptions
	r.invoke = x.invokeNested
	r.editAnsweredResult = x.editAnsweredResult
	for _, wrap := range slices.Backward(r.AroundTool) {
		x.handler = wrap(x.handler)
	}
	for !x.result.Complete {
		if err = ctx.Err(); err != nil {
			break
		}
		if len(x.result.Pending) > 0 {
			err = x.tools(ctx)
		} else {
			err = x.request(ctx)
		}
		if err != nil || singleStep {
			break
		}
	}
	return x.result, err
}
func (r *Run) execute(ctx context.Context, c Call) (string, error) {
	t, ok := r.tools[c.Name]
	if !ok {
		return "", Retry(fmt.Errorf("unknown tool %q", c.Name))
	}
	return t.Execute(ctx, c)
}
func (x *execution[T]) emit(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Usage = x.result.Usage
	if x.opts.Observe != nil {
		return x.opts.Observe(ctx, e)
	}
	return nil
}
func (x *execution[T]) checkpoint(ctx context.Context) error {
	state, err := x.run.saveState()
	if err != nil {
		return err
	}
	x.result.State = state
	if x.opts.Checkpoint == nil {
		return nil
	}
	s := x.result.Snapshot
	s.History = cloneMessages(s.History)
	s.Pending = slices.Clone(s.Pending)
	s.Offered = slices.Clone(s.Offered)
	s.State = make(map[string]json.RawMessage, len(state))
	for k, v := range state {
		s.State[k] = slices.Clone(v)
	}
	return x.opts.Checkpoint(ctx, s)
}
func (x *execution[T]) correction(err error) error {
	x.result.Retries++
	if x.result.Retries > x.agent.MaxRetries {
		return fmt.Errorf("%w: %w", ErrRetries, err)
	}
	return nil
}
func (x *execution[T]) request(ctx context.Context) error {
	u := &x.result.Usage
	x.run.FirstRequest = x.result.RequestStep == 0
	if err := x.requestAllowed(); err != nil {
		return err
	}
	req := Request{Messages: cloneMessages(x.result.History), Tools: slices.Clone(x.run.defs), OutputSchema: slices.Clone(x.agent.Output.Schema)}
	for i := range req.Tools {
		req.Tools[i].Schema = slices.Clone(req.Tools[i].Schema)
	}
	for _, m := range req.Messages {
		if len(m.Images) > 0 && m.Role != User {
			return errors.New("images require a user message")
		}
		for _, img := range m.Images {
			if err := img.Validate(); err != nil {
				return err
			}
		}
	}
	for _, rewrite := range x.run.RewriteHistory {
		updated, err := rewrite(context.WithValue(ctx, auxiliaryKey{}, auxiliaryAccount(x.auxiliaryRequest)), req, *u)
		if err != nil {
			return err
		}
		if updated != nil {
			x.result.History = cloneMessages(updated)
			if err := x.checkpoint(ctx); err != nil {
				return err
			}
		}
		req.Messages = cloneMessages(x.result.History)
	}
	if len(x.run.Instructions) > 0 {
		req.Messages = append([]Message{{Role: System, Text: strings.Join(x.run.Instructions, "\n\n")}}, req.Messages...)
	}
	for _, hook := range x.run.BeforeRequest {
		if err := hook(ctx, &req, *u); err != nil {
			return err
		}
	}
	if err := x.requestAllowed(); err != nil {
		return err
	}
	if err := x.emit(ctx, Event{Kind: "request"}); err != nil {
		return err
	}
	x.charge(Usage{Requests: 1})
	x.result.RequestStep = u.Requests
	var resp Response
	var err error
	if stream, ok := x.run.Model.(StreamingModel); ok && (x.opts.Observe != nil || len(x.run.CheckDelta) > 0) {
		resp, err = stream.Stream(ctx, req, func(s string) error {
			for _, check := range x.run.CheckDelta {
				if err := check(ctx, s); err != nil {
					return err
				}
			}
			if len(x.run.TransformText) > 0 {
				return nil
			}
			return x.emit(ctx, Event{Kind: "delta", Text: s})
		})
	} else {
		resp, err = x.run.Model.Generate(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("model request: %w", err)
	}
	if resp.Model == "" {
		if m, ok := x.run.Model.(NamedModel); ok {
			resp.Model = m.ModelName()
		}
	}
	x.charge(Usage{InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens})
	for _, transform := range x.run.TransformText {
		resp.Text, err = transform(ctx, resp.Text)
		if err != nil {
			return err
		}
	}
	if len(x.run.TransformText) > 0 && resp.Text != "" {
		if _, ok := x.run.Model.(StreamingModel); ok && x.opts.Observe != nil {
			if err := x.emit(ctx, Event{Kind: "delta", Text: resp.Text}); err != nil {
				return err
			}
		}
	}
	for _, hook := range x.run.AfterResponse {
		if err := hook(ctx, resp); err != nil {
			return err
		}
	}
	if err := x.tokensAllowed(); err != nil {
		return err
	}
	if err := validateCalls(resp.Calls); err != nil {
		return err
	}
	x.result.History = append(x.result.History, Message{Role: Assistant, Native: resp.Native, Reasoning: cloneReasoning(resp.Reasoning), ContextTokens: contextTokens(resp), Model: resp.Model, Text: resp.Text, Calls: slices.Clone(resp.Calls)})
	if err := x.emit(ctx, Event{Kind: "response", Text: resp.Text}); err != nil {
		return err
	}
	if len(resp.Calls) > 0 {
		x.result.Offered = make([]string, 0, len(req.Tools))
		for _, t := range req.Tools {
			x.result.Offered = append(x.result.Offered, t.Name)
		}
		x.result.Pending = slices.Clone(resp.Calls)
		for i, call := range x.result.Pending {
			if !slices.Contains(x.result.Offered, call.Name) {
				continue
			}
			for _, prepare := range x.run.PrepareCall {
				call, err = prepare(call)
				if err != nil {
					return err
				}
			}
			x.result.Pending[i] = call
		}
		if err := validateCalls(x.result.Pending); err != nil {
			return err
		}
		if x.singleStep {
			return x.checkpoint(ctx)
		}
		return nil
	}
	if resp.Native != nil && resp.Text == "" {
		return x.checkpoint(ctx)
	}
	output, err := x.agent.Output.Decode(resp.Text)
	if err == nil {
		for _, check := range x.run.CheckOutput {
			if err = check(ctx, resp.Text); err != nil {
				if !errors.As(err, new(retryError)) {
					return err
				}
				break
			}
		}
	}
	if err != nil {
		if e := x.correction(err); e != nil {
			return e
		}
		x.result.History = append(x.result.History, Message{Role: User, Text: "Correct the output: " + err.Error()})
		return x.checkpoint(ctx)
	}
	for _, review := range x.run.ReviewOutput {
		feedback, err := review(ctx, resp.Text)
		if err != nil {
			return err
		}
		if feedback != "" {
			x.result.History = append(x.result.History, Message{Role: User, Text: feedback})
			return x.checkpoint(ctx)
		}
	}
	x.result.Complete = true
	x.result.Output = output
	if err := x.checkpoint(ctx); err != nil {
		return err
	}
	return x.emit(ctx, Event{Kind: "final", Text: resp.Text})
}
func (x *execution[T]) tools(ctx context.Context) error {
	if err := x.checkAncestors(0, len(x.result.Pending)); err != nil {
		return err
	}
	if exceeds(x.result.Usage.ToolCalls+len(x.result.Pending), x.agent.Limits.ToolCalls) {
		return fmt.Errorf("%w: tool calls", ErrLimit)
	}
	x.run.Step = x.result.RequestStep
	if x.run.Step == 0 {
		x.run.Step = x.result.Usage.Requests
	}
	for len(x.result.Pending) > 0 {
		call := x.result.Pending[0]
		if err := x.emit(ctx, Event{Kind: "tool_call", Call: call}); err != nil {
			return err
		}
		decision := Approve
		if x.opts.Authorize != nil {
			d, err := x.opts.Authorize(ctx, call)
			if err != nil {
				return err
			}
			decision = d
		} else if x.result.AwaitingApproval {
			decision = Defer
		}
		protected := decision == Deny
		var text string
		switch decision {
		case Defer:
			x.result.AwaitingApproval = true
			if err := x.checkpoint(ctx); err != nil {
				return err
			}
			return fmt.Errorf("%w: %s", ErrDeferred, call.ID)
		case Deny:
			text = "Tool call denied by application policy."
		case Approve:
			var err error
			originalName := x.result.originalCallName(call.ID)
			if !slices.Contains(x.result.Offered, originalName) {
				err = Retry(fmt.Errorf("tool %q was not offered for this request", call.Name))
			} else {
				text, err = x.executeTool(ctx, call)
			}
			if err != nil {
				if errors.Is(err, ErrDeferred) {
					x.result.AwaitingApproval = true
					if saveErr := x.checkpoint(ctx); saveErr != nil {
						return saveErr
					}
					return err
				}
				if !errors.As(err, new(retryError)) {
					return fmt.Errorf("tool %s: %w", call.Name, err)
				}
				protected = true
				x.result.Correction = err.Error()
				text = err.Error()
			}
		default:
			return errors.New("invalid authorization decision")
		}
		x.charge(Usage{ToolCalls: 1})
		x.result.AwaitingApproval = false
		x.result.Pending = x.result.Pending[1:]
		x.result.History = append(x.result.History, Message{Role: ToolResult, Pinned: protected, Text: text, CallID: call.ID})
		if err := x.emit(ctx, Event{Kind: "tool_result", Call: call, Text: text}); err != nil {
			return err
		}
		if x.singleStep && len(x.result.Pending) > 0 {
			return x.checkpoint(ctx)
		}
	}
	x.result.Offered = nil
	if x.result.Correction != "" {
		if err := x.correction(errors.New(x.result.Correction)); err != nil {
			return err
		}
		x.result.Correction = ""
	}
	return x.checkpoint(ctx)
}

// Tool results follow their assistant message. Preparation may change the
// tool name, while the original call ID still correlates with model history.
func (s Snapshot) originalCallName(id string) string {
	for i := len(s.History) - 1; i >= 0; i-- {
		if s.History[i].Role != Assistant {
			continue
		}
		for _, c := range s.History[i].Calls {
			if c.ID == id {
				return c.Name
			}
		}
		break
	}
	return ""
}
func exceeds(value, limit int) bool { return limit > 0 && value > limit }

func (x *execution[T]) requestAllowed() error {
	if err := x.checkAncestors(1, 0); err != nil {
		return err
	}
	if x.result.Usage.Requests >= x.agent.Limits.Requests {
		return fmt.Errorf("%w: requests", ErrLimit)
	}
	return x.tokensAllowed()
}
func (x *execution[T]) tokensAllowed() error {
	if err := x.checkAncestors(0, 0); err != nil {
		return err
	}
	u, l := x.result.Usage, x.agent.Limits
	if exceeds(u.InputTokens, l.InputTokens) || exceeds(u.OutputTokens, l.OutputTokens) || exceeds(u.InputTokens+u.OutputTokens, l.TotalTokens) {
		return fmt.Errorf("%w: tokens", ErrLimit)
	}
	return nil
}

// SelfAgent returns a fresh-history delegate with the original agent's tools,
// instructions, capabilities and output validation, using the current model.
func (r *Run) SelfAgent() Agent[string] {
	child := r.self
	child.Model = r.Model
	return child
}

// FilterPrompt applies a prompt filter during capability installation. A
// restored checkpoint already contains the filtered prompt. Multiple filters
// on a fresh run see the previous filter's result.
func (r *Run) FilterPrompt(f func(string) (string, error)) error {
	if f == nil {
		return errors.New("nil prompt filter")
	}
	r.hasPromptFilter = true
	if r.promptWasFiltered {
		return nil
	}
	text, err := f(r.Prompt)
	if err == nil {
		r.Prompt = text
	}
	return err
}
