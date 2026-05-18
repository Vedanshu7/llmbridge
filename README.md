# llmbridge

A unified Go interface to multiple LLM providers.

Switch between OpenAI, Anthropic, Ollama, LM Studio, Groq, or any OpenAI-compatible
endpoint by changing one line. Your application code never changes.

## Supported Providers

| Provider | Constructor | Notes |
|----------|-------------|-------|
| OpenAI | `NewOpenAI(model, apiKey)` | GPT-4o, GPT-4o-mini, etc. |
| Anthropic | `NewAnthropic(model, apiKey)` | Claude Opus/Sonnet/Haiku |
| Ollama | `NewOllama(model)` | Local, requires `ollama` running |
| LM Studio | `NewLMStudio(model)` | Local, requires LM Studio server |
| Groq | `NewGroq(model, apiKey)` | Fast inference for open models |
| Together AI | `NewTogetherAI(model, apiKey)` | Hosted open-source models |
| Any OpenAI-compat | `NewOpenAICompatible(name, url, key, model)` | Generic adapter |

## Quick Start

```go
import "github.com/Vedanshu7/llmbridge"

// Pick any provider
p := llmbridge.NewOpenAI("gpt-4o-mini", os.Getenv("OPENAI_API_KEY"))
// p := llmbridge.NewAnthropic("claude-sonnet-4-6", os.Getenv("ANTHROPIC_API_KEY"))
// p := llmbridge.NewOllama("llama3.2")

resp, err := p.Complete(ctx, llmbridge.Request{
    System:   "You are a helpful assistant.",
    Messages: []llmbridge.Message{
        {Role: "user", Content: "What is the capital of France?"},
    },
})
fmt.Println(resp.Content) // "Paris"
```

## Tool Use

```go
tools := []llmbridge.Tool{{
    Name:        "get_weather",
    Description: "Get current weather for a city.",
    Parameters: llmbridge.Schema{
        Type: "object",
        Properties: map[string]llmbridge.Property{
            "city": {Type: "string", Description: "City name"},
        },
        Required: []string{"city"},
    },
}}

resp, err := p.Complete(ctx, llmbridge.Request{
    Messages: []llmbridge.Message{{Role: "user", Content: "Weather in Tokyo?"}},
    Tools:    tools,
})

if len(resp.ToolCalls) > 0 {
    tc := resp.ToolCalls[0]
    // tc.Name == "get_weather"
    // tc.Arguments == `{"city": "Tokyo"}`

    result := callWeatherAPI(tc.Arguments)

    // Send result back
    resp, err = p.Complete(ctx, llmbridge.Request{
        Messages: []llmbridge.Message{
            {Role: "user",      Content: "Weather in Tokyo?"},
            {Role: "assistant", ToolCalls: resp.ToolCalls},
            {Role: "tool",      ToolCallID: tc.ID, Content: result},
        },
        Tools: tools,
    })
}
```

## Session Persistence

```go
// Start a new session
session := llmbridge.NewSession("anthropic", "claude-sonnet-4-6")
session.Add(llmbridge.Message{Role: "user", Content: "Hello!"})
session.Add(llmbridge.Message{Role: "assistant", Content: "Hi there!"})
session.Save()

// Later, in a new process:
session, err := llmbridge.LoadLatestSession()
// session.Messages contains the full history
```

## Local Models via Docker

```bash
# Ollama
docker run -d -p 11434:11434 ollama/ollama
docker exec <id> ollama pull llama3.2
```

```go
p := llmbridge.NewOllama("llama3.2")
```

## Adding a New Provider

Implement the `Provider` interface:

```go
type MyProvider struct{}

func (p *MyProvider) Name() string { return "myprovider" }

func (p *MyProvider) Complete(ctx context.Context, req llmbridge.Request) (*llmbridge.Response, error) {
    // translate req to your API's wire format
    // call your API
    // translate response back to llmbridge.Response
}
```

That's it. No registration, no framework changes.

## License

MIT
