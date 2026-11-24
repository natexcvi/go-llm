package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
	"github.com/natexcvi/go-llm/integrations"
	"github.com/natexcvi/go-llm/models"
	"github.com/natexcvi/go-llm/workspace"
	"github.com/spf13/cobra"
)

func newRunCommand() *cobra.Command {
	var preset, dir, model, base string
	var programs []string
	cmd := &cobra.Command{Use: "run PROMPT", Short: "Run a native Go harness agent", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()
		if model == "" {
			return errors.New("set --model or OPENAI_MODEL")
		}
		a := h.Agent[string]{Model: models.OpenAI{Model: model, BaseURL: base, APIKey: os.Getenv("OPENAI_API_KEY")}, Output: h.Text(), Limits: h.Limits{TotalTokens: tokenLimit}}
		switch preset {
		case "chat":
		case "coder":
			var env []string
			for _, name := range []string{"PATH", "HOME", "TMPDIR", "GOCACHE", "GOPATH", "SYSTEMROOT"} {
				if value, ok := os.LookupEnv(name); ok {
					env = append(env, name+"="+value)
				}
			}
			w, err := workspace.Open(dir, env)
			if err != nil {
				return err
			}
			defer w.Close()
			a.Capabilities = []h.Capability{capabilities.Coder(w, w, programs...)}
		case "researcher":
			a.Capabilities = []h.Capability{capabilities.Researcher(integrations.Keenable{APIKey: os.Getenv("KEENABLE_API_KEY")})}
		default:
			return fmt.Errorf("unknown preset %q", preset)
		}
		res, err := a.Run(ctx, strings.Join(args, " "), h.Options{})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), res.Output)
		return err
	}}
	cmd.Flags().StringVar(&model, "model", os.Getenv("OPENAI_MODEL"), "Chat Completions model name")
	cmd.Flags().StringVar(&base, "base-url", os.Getenv("OPENAI_BASE_URL"), "Chat Completions API base URL")
	cmd.Flags().StringVar(&preset, "preset", "chat", "chat, coder, or researcher")
	cmd.Flags().StringVar(&dir, "workspace", ".", "working directory for the coder")
	cmd.Flags().StringSliceVar(&programs, "program", nil, "permitted coder program (repeatable); default allows all")
	return cmd
}
