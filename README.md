# go-llm

**A Go harness for deep agents.** Give an agent a workspace, a task, and a budget. It can break the task into steps, send a subagent to investigate, act on the findings, and pick up where it left off after an approval or a process restart. Your Go application owns the tools, the permission decision for each call, and the storage for each run.

For example, a coding agent investigating a regression can send a read-only explorer through the repository, keep the relevant file paths as older tool output is compacted, run a focused test, and return a report that your application decodes into a Go struct.

## Try the coding agent

With Go 1.25.8 or later, set `OPENAI_API_KEY` and `OPENAI_MODEL` to use a Chat Completions model with tool calling. Run this command from a local Go repository.

```sh
go run github.com/natexcvi/go-llm/go-llm-cli@v1.0.0 run \
  --preset coder --workspace . --program go \
  "Trace one public API from its entry point to its tests. Run the most relevant test and report what it proves."
```

The Coder preset brings repository instructions, file search and editing, planning, shell execution, context compaction, and a read-only explorer. This command gives the agent write access to the current workspace and permits the `go` executable, including programs it starts. For a read-only investigation, the library example below exposes only reading tools. For isolated execution, supply a container or SSH workspace.

## Build a repository investigator

```sh
go get github.com/natexcvi/go-llm@v1.0.0
```

This program gives the investigator a plan and a named delegate. The investigator can decide which questions to delegate. The explorer gets a fresh conversation, can read files, and returns its findings to the parent. Both draw on the same request and tool-call budget.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
	"github.com/natexcvi/go-llm/models"
	"github.com/natexcvi/go-llm/workspace"
)

type Report struct {
	Answer string   `json:"answer"`
	Files  []string `json:"files"`
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: investigator <repository>")
	}
	repo, err := workspace.Open(os.Args[1], nil)
	if err != nil {
		log.Fatal(err)
	}
	defer repo.Close()

	read := capabilities.FileSystem(repo, false)
	explorer := capabilities.Delegate{
		Name: "explorer", Description: "Trace one code path and cite the files you read.",
		Agent: h.Agent[string]{Capabilities: []h.Capability{read}},
	}
	agent := h.Agent[Report]{
		Model: models.OpenAIResponses{
			Model: os.Getenv("OPENAI_MODEL"), APIKey: os.Getenv("OPENAI_API_KEY"),
		},
		Instructions: "Plan the investigation. Delegate one path to the explorer, then verify its citations.",
		Output:       h.JSON[Report](),
		Limits:       h.Limits{Requests: 16, ToolCalls: 40},
		Capabilities: []h.Capability{
			read, capabilities.Planning(),
			capabilities.SubAgents{Agents: []capabilities.Delegate{explorer}}.Capability(),
		},
	}
	result, err := agent.Run(context.Background(), "How does this project handle a failed operation?", h.Options{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Output.Answer)
	for _, file := range result.Output.Files {
		fmt.Println(file)
	}
}
```

Save it as `main.go` in a Go module and run `go run . /path/to/repository` with a Responses API model that supports tools and structured output. The harness derives the schema from `Report` and validates the model's answer before returning it. To test the same loop offline, provide a scripted `h.ModelFunc`. [Runnable offline example](agent_test.go)

## What survives a long run?

| Mechanism | What it keeps | When to use it |
| --- | --- | --- |
| **Planning** | Task IDs, status, parent tasks, and prerequisites | Track what remains when the work spans many tool calls. |
| **Compaction** | A smaller active conversation, including retained tool calls and their results | Reclaim context from long logs and repeated file reads before paying for a summary. |
| **Memory** | A scoped notebook across runs. A bounded excerpt of changed notes from `MEMORY.md` accompanies the next user prompt. | Carry project conventions and decisions into a new conversation. |
| **Checkpointing** | A serializable snapshot of history, pending calls, usage, and bound capability state | Resume the same run after an approval or a worker restart. |

A compaction policy can clear old tool results first, then summarize only if the request still exceeds its target. This example uses a 128,000-token window. Set the window for the model you deploy.

```go
policy := capabilities.CompactionPolicy{
	Budget:         capabilities.ContextBudget{Window: 128_000},
	TargetFraction: 0.7,
	OnRequest:      true,
	Strategies: []capabilities.CompactionStrategy{
		capabilities.ClearOldResults(4, 3),
		capabilities.RetainRecent(6, capabilities.ModelSummary(model)),
	},
}
```

Add `policy.Capability()` to the agent. `OnRequest` also gives the model a `compact_context` tool. Compaction changes the history that later requests and checkpoints carry. OpenAI Responses and Anthropic Messages also support provider-native compaction. [Compaction policies](capabilities/compaction.go) · [Notebook options](capabilities/memory.go)

## Pause for approval and resume

An `Options.Authorize` callback sees the tool name and arguments before execution. Return `h.Defer` for a change that needs review. The run returns `h.ErrDeferred`, and its snapshot identifies the pending call. Authorization also applies to delegated and programmatic tool calls.

Use `storage.Checkpoint(store, runID)` as `Options.Checkpoint` to save progress. After review, load the snapshot and pass an authorizer that checks the recorded decision for that call.

```go
snapshot, err := storage.Load(ctx, store, runID)
if err != nil {
	return err
}
result, err := agent.Resume(ctx, snapshot, h.Options{
	Authorize:  authorizeReviewedCall,
	Checkpoint: storage.Checkpoint(store, runID),
})
```

Use one writer per run ID and an idempotency key for external writes. A crash between a tool's side effect and its checkpoint can replay that call. For queue-driven work, `Agent.Advance` executes one top-level model request or one pending tool call per step. [Storage backends](storage) · [Durable adapters for Lambda, Azure Durable Task, Absurd, and PGTask](durable)

## Let the model call tools from code

For tasks that need loops, comparisons, or aggregation, code mode exposes selected Go tools as functions in a Starlark REPL running inside WebAssembly. One snippet can call several tools and return a small result to the outer conversation. Each nested call goes through the parent's authorization, middleware, events, and tool-call limit.

Build the interpreter from a checkout of this repository.

```sh
GOOS=wasip1 GOARCH=wasm go build -o harness-code.wasm ./codemode/cmd/harness-code
```

Load the bytes with `codemode.Open(ctx, guest)`, install `engine.Capability(tools...)`, and close the engine when the application shuts down. Variables and functions survive successive snippets within a run. When a nested call pauses for approval, resuming reconstructs the program from recorded results so completed calls keep their values. [Code mode](codemode) · [Approval and replay tests](codemode/code_test.go)

## Choose the pieces your agent needs

| Package | Use it for |
| --- | --- |
| [harness](agent.go) | Typed tools and outputs, model and tool events, retries, budgets, and resumable runs. |
| [capabilities](capabilities) | Coder and Researcher presets, named subagents, context policies, guardrails, human input, and skill libraries. |
| [models](models) | Streaming OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and model fallback. |
| [workspace](workspace) and [browser](browser) | Rooted files, local or isolated command execution, and browser interaction through Chromium. |
| [integrations](integrations) | MCP tool discovery and execution, hosted service adapters, and search providers for research agents. |

The native harness lives at the module root. Earlier imports such as `agents`, `engines`, and `tools` remain available. For a mechanism-by-mechanism comparison with Pydantic AI Harness 0.73.0, including differences and outstanding ports, see the [coverage matrix](docs/PARITY.md).
