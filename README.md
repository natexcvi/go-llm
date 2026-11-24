# go-llm

Build tool-using agents in Go. The root package runs the conversation, validates model output against Go types, and gives your application control over every tool call. It requires Go 1.25.8 or later.

```sh
go get github.com/natexcvi/go-llm
```

## A typed agent

This agent looks up inventory in application code and returns a validated Go struct. The model name and credentials come from the calling application.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	harness "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/models"
)

type Lookup struct {
	SKU string `json:"sku"`
}

type Stock struct {
	SKU   string `json:"sku"`
	Units int    `json:"units"`
}

func main() {
	lookup := harness.NewTool("lookup_stock", "Look up the available units for a SKU.",
		func(ctx context.Context, in Lookup) (Stock, error) {
			if in.SKU != "A42" {
				return Stock{}, harness.Retry(errors.New("unknown SKU"))
			}
			return Stock{SKU: in.SKU, Units: 7}, nil
		})

	agent := harness.Agent[Stock]{
		Model: models.OpenAI{
			Model:  os.Getenv("OPENAI_MODEL"),
			APIKey: os.Getenv("OPENAI_API_KEY"),
		},
		Output:       harness.JSON[Stock](),
		Instructions: "Check inventory before answering.",
		Capabilities: []harness.Capability{harness.Tools(lookup)},
		Limits:       harness.Limits{Requests: 5, ToolCalls: 2},
	}
	result, err := agent.Run(context.Background(), "How many A42 units are available?", harness.Options{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s has %d units\n", result.Output.SKU, result.Output.Units)
}
```

Use `harness.Text()` for free text. For deterministic tests, supply a `harness.ModelFunc` that returns model responses directly. The runnable example in [agent_test.go](agent_test.go) needs no network or API key.

## Workspaces and capabilities

`capabilities.Coder` combines file reads and edits, command execution, repository instructions, planning, and a read-only explorer. Choose the files and executor when constructing it.

```go
w, err := workspace.Open("./repo", []string{"PATH=/usr/local/go/bin:/usr/bin:/bin"})
if err != nil {
	return err
}
defer w.Close()

agent := harness.Agent[string]{
	Model: model, Output: harness.Text(),
	Capabilities: []harness.Capability{
		capabilities.Coder(w, w, "go", "gofmt"),
		capabilities.ReportRemainingBudget(),
	},
}
```

Local file operations use `os.Root`. Commands run with the host user's privileges and the environment supplied to `workspace.Open`. A program allowlist checks the executable the agent requests. Programs such as `go` can launch their own subprocesses. For container execution, pass a workspace from `workspace.OpenContainer`. For a remote POSIX host, use `workspace.OpenSSH` with an authenticated, host-key-verified SSH connection.

`capabilities.Researcher` combines a search and page-fetch provider, bounded tool output and a separate research agent. The integrations package includes Keenable, Exa and You.com implementations. `integrations.MCP` discovers remote tools through the Go MCP SDK. `integrations.Hosted` selects a service endpoint and accepts an application-authenticated HTTP client.

The `browser` package drives a stateful Chromium tab through the DevTools Protocol. Configure `browser.Chromium{Origins: []string{"https://example.org"}}.Capability()` to expose navigation, form interaction and reading tools. Its integration test runs against a local form. Use `browser.BrowserUse` to delegate a browsing goal to a child agent with its own tab and step budget.

Pass `Options.Images` to attach image URLs or bytes to the current prompt. The OpenAI Chat Completions, OpenAI Responses and Anthropic Messages transports support image input. For checkpoints with large binary images, `storage.MediaArchive` stores the bytes by SHA-256 and validates the digest on restore.

## Long-running conversations

Install a compaction policy to keep the active conversation within a budget. This example tries clearing old tool output first. It calls the summarizer only if the conversation still exceeds 70% of the configured window. The most recent groups of messages retain their tool call/result pairs.

```go
budget := capabilities.ContextBudget{Window: 128_000}
policy := capabilities.CompactionPolicy{
    Budget: budget, TargetFraction: 0.7, OnRequest: true,
    Strategies: []capabilities.CompactionStrategy{
        capabilities.ClearOldResults(4, 3),
        capabilities.RetainRecent(6, capabilities.ModelSummary(model)),
    },
}
agent := harness.Agent[string]{
    Model: model, Output: harness.Text(),
    Capabilities: []harness.Capability{policy.Capability(), capabilities.PruneToolResults()},
}
```

The policy updates the conversation saved by `Run`, `Advance` and `Resume`. Use `Options.Checkpoint` to archive earlier versions when needed. `compact_context` asks for a pass before the next model request. `prune_tool_result` lets the model replace a specific result it just received with a shorter note. `ReportContextUsage` exposes the estimate and window size. Supply the model's window explicitly when using fractional targets. The fallback is 200,000 tokens.

For provider-native compaction, use `capabilities.NativeSummary(model, keepGroups)` with `models.OpenAIResponses` or `models.Anthropic`. Both transports also accept `CompactAtTokens` for server-managed compaction. Native blocks carry provider data that must be replayed on the originating transport and endpoint. Model-backed summarizers, judges and on-demand provider compaction count toward the run's request and token limits.

Use `capabilities.SubAgents{Agents: delegates}.Capability()` for a named roster with a shared usage budget. Each child gets fresh history and inherits authorization. `IncludeSelf` adds a fresh run of the original agent. `ForwardEvents` attaches the delegate path to child events. The single-tool `SubAgent` helper retains its independent accounting.

`capabilities.Memory(store)` records a bounded excerpt of `MEMORY.md` and filenames with the next user prompt when the notebook changes. `NotebookOptions` configures read and search limits. `SkillCatalog` layers skill libraries in precedence order and supports explicit inclusion, exclusion and duplicate handling. Enable `ReadFiles` to let loaded skills read bundled text through a rooted workspace or embedded filesystem.

## Calling tools from code

The optional `codemode` package lets a model call several Go tools in a single Starlark program. Build the interpreter from this repository with the Go toolchain.

```sh
GOOS=wasip1 GOARCH=wasm go build -o harness-code.wasm ./codemode/cmd/harness-code
```

Read the module with `os.ReadFile`, pass the bytes to `codemode.Open(ctx, module)`, and install `engine.Capability(lookup)`. Close the engine after the application finishes serving runs. The guest receives its source and prior tool results through standard input. Each evaluation gets a fresh instance, 64 MiB of linear memory and a five-second deadline. Programs can use loops, comprehensions and named tool arguments. Only the final expression and printed output enter the outer conversation. Approval pauses preserve completed nested calls. Use `engine.DynamicWorkflow` to expose named child agents as functions in a program with a shared call budget.

Code mode keeps a REPL by default. Variables and functions survive successive snippets and checkpoint restores. A call with `restart: true` clears the session. The guest reconstructs bindings by replaying completed snippets against their recorded tool results. The session holds up to 64 snippets and shares the guest input and execution limits. Each new agent run starts a fresh session. Use `engine.Stateless(lookup)` for independent snippets or to run a guest module built before REPL support.

Install `engine.CapabilityCreation(store)` to let an agent author, validate, list and disable named Starlark transformations. Install `engine.LoadActive(store)` to load enabled tools at run start. Each tool receives a `params` dictionary. Use a dedicated store per authoring scope.

## Runs, approvals and recovery

Configure an agent once before running it concurrently. Each run creates its own tools and capability state. A caller that supplies a store, job manager or peer network chooses which runs share that resource.

- Pass `Options.History` to continue a conversation. `Result.History` includes the supplied messages, the user prompt, model responses and correlated tool results.
- Pass `Options.Observe` for request, response, tool and final events. OpenAI and Anthropic also emit incremental text. Returning an error from the observer stops the run.
- Use `Options.Authorize` to approve, deny or defer a call before execution. On `errors.Is(err, harness.ErrDeferred)`, persist the snapshot and resume it with a decision. A deferred call stays paused if the resumed run has no authorizer.
- Connect `Options.Checkpoint` to a memory, directory or SQL store with `storage.Checkpoint`. Reload with `storage.Load` and call `Agent.Resume`. Saved pending calls, usage and bound state survive a restart. Resuming a completed checkpoint returns its recorded output.
- Checkpoints cover completed tool batches, approval pauses, output corrections and final responses. A process failure between an external side effect and its checkpoint can replay that effect. Use idempotency keys for such tools. Use one writer per run key.
- `harness.Retry(err)` sends a correctable handler error to the model. Other handler errors return to the application. Inspect `ErrRetries` and `ErrLimit` with `errors.Is`.

## Durable execution

Use `harness.Start` and `Agent.Advance` to drive one top-level model request or one pending tool call at a time. Compaction helper requests execute within the enclosing model step. The returned snapshot contains the next pending action. Persist it before advancing again.

The `durable/lambda` adapter uses the AWS durable execution SDK. Inside a Lambda durable handler, call `lambda.Run(ctx, agent, harness.Start(prompt, nil), options)`. The `durable/azure` adapter registers an orchestrator and activity with a Microsoft Durable Task worker. Both adapters can suspend for approval. For PostgreSQL task queues, use `durable/absurd` inside an Absurd task or `durable/pgtask` with a claimed pgtask lease. Each step reconstructs its capabilities from the snapshot, so use a shared store for data that needs to cross steps. Use idempotency keys for side effects that a worker can retry before recording completion.

## CLI

```sh
go run ./go-llm-cli run --model "$OPENAI_MODEL" "Explain Go interfaces"
go run ./go-llm-cli run --model "$OPENAI_MODEL" --preset coder --workspace . --program go "Run the tests and summarize failures"
```

The CLI accepts `OPENAI_API_KEY` and an optional `OPENAI_BASE_URL`. The researcher preset uses Keenable and accepts `KEENABLE_API_KEY`.

## Compatibility and verification

The comparison target is Pydantic AI Harness 0.73.0. The coverage table records native implementations, behavior checked against the pinned release, and outstanding ports. [Coverage and differences](docs/PARITY.md)

Existing imports of `agents`, `engines`, `tools`, `memory`, `evaluation` and `prebuilt` still work. Existing CLI commands use those packages. New applications can start at the module root without importing the legacy stack.

```sh
CGO_ENABLED=0 go build ./...
go test -race . ./capabilities ./models ./integrations ./storage ./workspace/... ./durable/... ./browser ./codemode/...
go test ./...
```

The native harness tests use local model fixtures, HTTP and SSH servers, SQLite, and the AWS durable SDK local runner. The Chromium test runs when a browser is installed. Set `GO_LLM_TEST_POSTGRES` to a dedicated database to exercise PostgreSQL storage and the Microsoft Durable Task worker. Set `GO_LLM_TEST_ABSURD` or `GO_LLM_TEST_PGTASK` to exercise a database with the respective queue schema installed. The Docker workspace test runs when `GO_LLM_DOCKER_TEST_IMAGE` names an available image containing `cat`. The legacy tool suite also exercises Chromium, Docker and Python.
