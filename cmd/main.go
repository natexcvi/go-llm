package main

import (
	"fmt"
	"os"

	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/prebuilt"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	tokenLimit int
	gptModel   string
)

var rootCmd = &cobra.Command{
	Use:   "go-llm",
	Short: "A CLI for using the prebuilt agents.",
	// Long: ``,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("root command")
	},
}

var subCmd = &cobra.Command{
	Use:   "code-refactor [code_base_dir] [goal]",
	Short: "A code refactoring assistant.",
	Run: func(cmd *cobra.Command, args []string) {
		codeBaseDir := args[0]
		goal := args[1]
		if err := validateDirectory(codeBaseDir); err != nil {
			log.Error(err)
			return
		}
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Errorf("OPENAI_API_KEY environment variable not set")
			return
		}
		engine := engines.NewGPTEngine(apiKey, gptModel)
		agent := prebuilt.NewCodeRefactorAgent(engine)
		res, err := agent.Run(prebuilt.CodeBaseRefactorRequest{
			Dir:  codeBaseDir,
			Goal: goal,
		})
		if err != nil {
			log.Error(err)
			return
		}
		log.Info(res)
	},
	Args:      cobra.ExactArgs(2),
	ValidArgs: []string{"code-base-dir", "goal"},
}

func validateDirectory(dir string) error {
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

func init() {
	rootCmd.PersistentFlags().IntVar(&tokenLimit, "token-limit", 0, "stop after using this many tokens")
	rootCmd.PersistentFlags().StringVar(&gptModel, "gpt-model", "gpt-3.5-turbo", "the GPT model to use")
}

func main() {
	log.SetLevel(log.DebugLevel)
	rootCmd.AddCommand(subCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
