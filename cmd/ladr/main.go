// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Command ladr runs and checks LADR ladders standalone — no Redis, no
// Postgres: step outputs live in memory, reliks live in memory, code
// execution runs on this machine (LADR_SESSIONS_DIR), files: tools are
// scoped to the run's workspace directory.
//
//	ladr check story.ladr
//	ladr run story.ladr --input topic=static --knob depth=3
//
// Provider credentials come from the environment: OPENROUTER_API_KEY,
// OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY, XAI_API_KEY, ... or a
// custom OpenAI-compatible endpoint via LADR_PROVIDER=custom,
// LADR_BASE_URL=..., LADR_API_KEY=...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ladr/ladr/internal/device"
	"github.com/ladr/ladr/internal/engine"
	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = runCheck(os.Args[2:])
	case "run":
		err = runRun(os.Args[2:])
	case "version":
		fmt.Println("ladr 0.1.0")
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ladr — run and check LADR ladders standalone

Usage:
  ladr check <file.ladr>                 parse + validate a ladder
  ladr run <file.ladr> [flags]           execute a ladder locally
  ladr version

Run flags:
  --provider name        provider id (default: from available keys; see below)
  --model id             model id for the run (step-level model fields win)
  --input k=v            run input (repeatable)
  --knob k=v             knob value (repeatable)
  --max-loops n          loop ceiling (default 3)
  --workspace dir        files: tool root (default: temp dir per run)
  --sessions-dir dir     persistent interpreter sessions dir (default: temp)
  --timeout duration     overall wall clock (default 10m)
  -v                     print engine events to stderr

Provider resolution: OPENROUTER_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY,
GOOGLE_API_KEY, XAI_API_KEY, DEEPSEEK_API_KEY, MISTRAL_API_KEY, GROQ_API_KEY
are picked up automatically; the first match (in that order) selects the
provider unless --provider overrides. Custom OpenAI-compatible endpoint:
LADR_PROVIDER=custom LADR_BASE_URL=https://host/v1 LADR_API_KEY=...
`)
}

// ---------------------------------------------------------------------------
// check
// ---------------------------------------------------------------------------

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ladr check <file.ladr>")
	}
	cfg, _, err := loadConfig(fs.Arg(0))
	if err != nil {
		return err
	}
	result := ladr.ValidateConfig(cfg)
	if len(result.Errors) > 0 || !result.Valid {
		fmt.Printf("✗ %s: %d error(s)\n", fs.Arg(0), len(result.Errors))
		for _, e := range result.Errors {
			fmt.Println("  -", e)
		}
		return fmt.Errorf("validation failed")
	}
	fmt.Printf("✓ %s: valid — %d steps, exit %q\n", fs.Arg(0), len(cfg.Steps), cfg.Exit)
	return nil
}

// ---------------------------------------------------------------------------
// run
// ---------------------------------------------------------------------------

func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	providerFlag := fs.String("provider", "", "provider id")
	modelFlag := fs.String("model", "", "model id")
	maxTokens := fs.Int("max-tokens", 8192, "per-call max output tokens (keeps prepaid credit math honest)")
	maxLoops := fs.Int("max-loops", 3, "loop ceiling")
	workspaceFlag := fs.String("workspace", "", "files: tool workspace root")
	sessionsFlag := fs.String("sessions-dir", "", "persistent interpreter sessions dir")
	timeout := fs.Duration("timeout", 10*time.Minute, "overall timeout")
	verbose := fs.Bool("v", false, "print engine events to stderr")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: ladr run <file.ladr> [--input k=v ...]")
	}
	path := fs.Arg(0)

	inputs := map[string]any{}
	for _, kv := range collectPairs(args, "--input") {
		if i := strings.Index(kv, "="); i > 0 {
			inputs[kv[:i]] = kv[i+1:]
		}
	}
	knobs := map[string]float64{}
	for _, kv := range collectPairs(args, "--knob") {
		if i := strings.Index(kv, "="); i > 0 {
			v, err := strconv.ParseFloat(kv[i+1:], 64)
			if err != nil {
				return fmt.Errorf("knob %s: not a number (%s)", kv[:i], kv[i+1:])
			}
			knobs[kv[:i]] = v
		}
	}

	cfg, _, err := loadConfig(path)
	if err != nil {
		return err
	}

	if result := ladr.ValidateConfig(cfg); len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintln(os.Stderr, "  -", e)
		}
		return fmt.Errorf("ladder failed validation — run `ladr check %s` for details", path)
	}

	pc, err := resolveProvider(*providerFlag, *modelFlag, *maxTokens)
	if err != nil {
		return err
	}

	workspace := *workspaceFlag
	if workspace == "" {
		workspace, _ = os.MkdirTemp("", "ladr-workspace-")
	} else {
		_ = os.MkdirAll(workspace, 0o755)
	}
	sessionsDir := *sessionsFlag
	if sessionsDir == "" {
		sessionsDir = filepath.Join(os.TempDir(), "ladr-sessions")
	}

	instanceID := fmt.Sprintf("cli-%d", time.Now().UnixMilli())
	store := relik.NewMemoryStore()

	io := &localIO{
		outputs:   map[string]string{},
		workspace: workspace,
		sessions:  device.NewSessionRunner(sessionsDir),
		verbose:   *verbose,
		instance:  instanceID,
		cfg:       cfg,
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := engine.ExecutePipeline(ctx, engine.ExecutePipelineOptions{
		Config:        cfg,
		Context: engine.ExecutionRuntimeContext{
			InstanceID: instanceID,
			Knobs:      knobs,
			Inputs:     inputs,
			Config:     cfg,
		},
		IO:             io,
		Provider:       pc,
		MaxLoops:       *maxLoops,
		RelikStore:     store,
		WriteExecutor:  engine.NewWriteExecutor(store),
		CeilingTracker: engine.NewCeilingTracker(engine.DefaultCeilings()),
	})
	if err != nil {
		fmt.Fprint(os.Stderr, io.finalTrail())
		return err
	}
	fmt.Print(result)
	if !strings.HasSuffix(result, "\n") {
		fmt.Println()
	}
	return nil
}

// collectPairs pulls `--flag value` style pairs out of the raw args (the
// std flag package consumes them, we re-read for inputs/knobs).
func collectPairs(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			out = append(out, args[i+1])
			i++
		} else if strings.HasPrefix(args[i], name+"=") {
			out = append(out, strings.TrimPrefix(args[i], name+"="))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// config loading (YAML or JSON)
// ---------------------------------------------------------------------------

func loadConfig(path string) (*ladr.PipelineConfig, json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	raw, err := toJSON(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg, err := ladr.ParseConfig(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, raw, nil
}

// ---------------------------------------------------------------------------
// provider resolution from environment
// ---------------------------------------------------------------------------

type envProvider struct {
	envVar   string
	provider string
}

var envProviders = []envProvider{
	{"OPENROUTER_API_KEY", "openrouter"},
	{"OPENAI_API_KEY", "openai"},
	{"ANTHROPIC_API_KEY", "anthropic"},
	{"GOOGLE_API_KEY", "google_ai_studio"},
	{"XAI_API_KEY", "xai"},
	{"DEEPSEEK_API_KEY", "deepseek"},
	{"MISTRAL_API_KEY", "mistral"},
	{"GROQ_API_KEY", "groq"},
}

func resolveProvider(providerFlag, modelFlag string, maxTokens int) (engine.ProviderConfig, error) {
	if os.Getenv("LADR_PROVIDER") == "custom" {
		base := os.Getenv("LADR_BASE_URL")
		key := os.Getenv("LADR_API_KEY")
		if base == "" {
			return engine.ProviderConfig{}, fmt.Errorf("LADR_PROVIDER=custom requires LADR_BASE_URL")
		}
		return engine.ProviderConfig{
			Provider: "custom",
			Model:    firstNonEmpty(modelFlag, os.Getenv("LADR_MODEL")),
			APIKey:   key,
			BaseURL:  base,
		}, nil
	}

	name, key := "", ""
	if providerFlag != "" {
		name = providerFlag
		for _, ep := range envProviders {
			if ep.provider == name {
				key = os.Getenv(ep.envVar)
				break
			}
		}
		if key == "" {
			key = os.Getenv("LADR_API_KEY")
		}
	} else {
		for _, ep := range envProviders {
			if v := os.Getenv(ep.envVar); v != "" {
				name, key = ep.provider, v
				break
			}
		}
	}
	if name == "" {
		return engine.ProviderConfig{}, fmt.Errorf("no provider key found — set OPENROUTER_API_KEY (or OPENAI/ANTHROPIC/GOOGLE/XAI/DEEPSEEK/MISTRAL/GROQ), or LADR_PROVIDER=custom LADR_BASE_URL=... LADR_API_KEY=...")
	}
	if key == "" {
		return engine.ProviderConfig{}, fmt.Errorf("provider %s selected but its API key env var is empty", name)
	}

	model := firstNonEmpty(modelFlag, os.Getenv("LADR_MODEL"))
	// OpenRouter addresses models as vendor/id; if the user gave a bare id,
	// pass it through — OpenRouter resolves both.
	return engine.ProviderConfig{Provider: name, Model: model, APIKey: key, MaxTokens: maxTokens}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
