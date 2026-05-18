// Command llmbridge is a CLI for running and managing an llmbridge proxy server.
//
// Usage:
//
//	llmbridge server [--config config.json] [--addr :8080]
//	llmbridge key generate [--scope completion] [--scope admin] [--spend-limit 10.00]
//	llmbridge key list
//	llmbridge key delete <key>
//	llmbridge version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Vedanshu7/llmbridge/llms/anthropic"
	"github.com/Vedanshu7/llmbridge/llms/base"
	"github.com/Vedanshu7/llmbridge/llms/compatible"
	"github.com/Vedanshu7/llmbridge/llms/gemini"
	"github.com/Vedanshu7/llmbridge/llms/openai"
	"github.com/Vedanshu7/llmbridge/proxy"
	"github.com/Vedanshu7/llmbridge/proxy/auth"
	"github.com/Vedanshu7/llmbridge/proxy/config"
)

const version = "0.2.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "server":
		cmdServer(os.Args[2:])
	case "key":
		cmdKey(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("llmbridge", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  llmbridge server   [--config config.json] [--addr :8080] [--provider openai] [--model gpt-4o] [--key KEY]
  llmbridge key generate [--scope SCOPE] [--spend-limit DOLLARS]
  llmbridge key list
  llmbridge key delete KEY
  llmbridge version`)
}

// ---- server command ----

func cmdServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	cfgFile := fs.String("config", "", "path to JSON config file")
	addr := fs.String("addr", ":8080", "listen address")
	provider := fs.String("provider", "openai", "provider: openai, anthropic, gemini, groq, ollama, deepseek, ...")
	model := fs.String("model", "", "model name (provider default if empty)")
	apiKey := fs.String("key", "", "API key for the provider (or set via env var)")
	_ = fs.Parse(args)

	var cfg *config.Config
	if *cfgFile != "" {
		var err error
		cfg, err = config.Load(*cfgFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	} else {
		cfg = config.Default()
		cfg.ListenAddr = *addr
	}

	// Override addr from flag if config was loaded but flag was explicitly set.
	if *addr != ":8080" || cfg.ListenAddr == "" {
		cfg.ListenAddr = *addr
	}

	p := buildProvider(*provider, *model, *apiKey)

	var srv *proxy.Server
	if *cfgFile != "" {
		srv = proxy.FromConfig(cfg, p)
	} else {
		srv = proxy.NewServer(p)
		if cfg.JWTSecret != "" {
			srv.SetJWTSecret([]byte(cfg.JWTSecret))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("llmbridge proxy listening on %s (provider: %s)\n", cfg.ListenAddr, *provider)
	if err := srv.Start(ctx, cfg.ListenAddr); err != nil {
		fmt.Fprintln(os.Stderr, "server error:", err)
		os.Exit(1)
	}
}

func buildProvider(providerName, model, apiKey string) base.LLM {
	switch providerName {
	case "openai":
		return openai.New(model, keyOrEnv(apiKey, "OPENAI_API_KEY"))
	case "anthropic":
		return anthropic.New(model, keyOrEnv(apiKey, "ANTHROPIC_API_KEY"))
	case "gemini":
		return gemini.New(model, keyOrEnv(apiKey, "GEMINI_API_KEY"))
	case "groq":
		return compatible.NewGroq(model, keyOrEnv(apiKey, "GROQ_API_KEY"))
	case "ollama":
		return compatible.NewOllama(model)
	case "deepseek":
		return compatible.NewDeepSeek(model, keyOrEnv(apiKey, "DEEPSEEK_API_KEY"))
	case "mistral":
		return compatible.NewMistral(model, keyOrEnv(apiKey, "MISTRAL_API_KEY"))
	case "perplexity":
		return compatible.NewPerplexity(model, keyOrEnv(apiKey, "PERPLEXITY_API_KEY"))
	default:
		fmt.Fprintf(os.Stderr, "unknown provider %q; supported: openai, anthropic, gemini, groq, ollama, deepseek, mistral, perplexity\n", providerName)
		os.Exit(1)
		return nil
	}
}

func keyOrEnv(flag, envVar string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(envVar)
}

// ---- key sub-commands ----

type multiFlag []string

func (f *multiFlag) String() string { return fmt.Sprint(*f) }
func (f *multiFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func cmdKey(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: llmbridge key <generate|list|delete>")
		os.Exit(1)
	}
	switch args[0] {
	case "generate":
		cmdKeyGenerate(args[1:])
	case "list":
		cmdKeyList(args[1:])
	case "delete":
		cmdKeyDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown key sub-command: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdKeyGenerate(args []string) {
	fs := flag.NewFlagSet("key generate", flag.ExitOnError)
	var scopes multiFlag
	fs.Var(&scopes, "scope", "scope to grant (repeatable: -scope completion -scope admin)")
	spendLimit := fs.Float64("spend-limit", 0, "maximum USD spend (0 = unlimited)")
	_ = fs.Parse(args)

	if len(scopes) == 0 {
		scopes = []string{"completion"}
	}
	store := auth.NewAPIKeyStore()
	key, err := store.GenerateAPIKey(scopes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error generating key:", err)
		os.Exit(1)
	}
	if *spendLimit > 0 {
		store.SetSpendLimit(key, *spendLimit)
	}
	fmt.Println(key)
}

func cmdKeyList(_ []string) {
	// Without a running server, list is a no-op from CLI.
	// In a real deployment this would call the /admin/keys HTTP endpoint.
	fmt.Fprintln(os.Stderr, "key list requires a running server; use GET /admin/keys with your admin API key")
}

func cmdKeyDelete(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: llmbridge key delete <key>")
		os.Exit(1)
	}
	// Same as list — requires a running server.
	fmt.Fprintf(os.Stderr, "key delete requires a running server; use DELETE /admin/key/delete?key=%s\n", args[0])
}
