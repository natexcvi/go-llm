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

var codeRefactorAgent = &cobra.Command{
	Use:   "code-refactor CODE_BASE_DIR GOAL",
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

var tradeAssistantAgent = &cobra.Command{
	Use:   "trade-assistant STOCK...",
	Short: "A stock trading assistant.",
	Run: func(cmd *cobra.Command, args []string) {
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Errorf("OPENAI_API_KEY environment variable not set")
			return
		}
		wolframAppID := os.Getenv("WOLFRAM_APP_ID")
		if wolframAppID == "" {
			log.Errorf("WOLFRAM_APP_ID environment variable not set")
			return
		}
		engine := engines.NewGPTEngine(apiKey, gptModel)
		agent := prebuilt.NewTradeAssistantAgent(engine, wolframAppID)
		res, err := agent.Run(prebuilt.TradeAssistantRequest{
			Stocks: args,
		})
		if err != nil {
			log.Error(err)
			return
		}
		log.Info(res)
	},
	Args: cobra.MinimumNArgs(1),
}

func readFile(filePath string) (content string) {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatal(err)
	}
	return string(contentBytes)
}

var unitTestWriter = &cobra.Command{
	Use:   "unit-test-writer SOURCE_FILE EXAMPLE_FILE",
	Short: "A tool for writing unit tests.",
	Long: `A tool for writing unit tests.
Example usage:
	go-llm unit-test-writer source.py example.py
Where source.py is where the source code
to be tested is located, and example.py
is an example unit test file.
`,
	Run: func(cmd *cobra.Command, args []string) {
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Errorf("OPENAI_API_KEY environment variable not set")
			return
		}
		sourceFilePath := args[0]
		exampleFilePath := args[1]
		engine := engines.NewGPTEngine(apiKey, gptModel)
		agent, err := prebuilt.NewUnitTestWriter(engine, func(code string) error {
			return nil
		})
		if err != nil {
			log.Error(err)
			return
		}
		res, err := agent.Run(prebuilt.UnitTestWriterRequest{
			SourceFile:  readFile(sourceFilePath),
			ExampleFile: readFile(exampleFilePath),
		})
		if err != nil {
			log.Error(err)
			return
		}
		log.Info(res)
	},
	Args: cobra.ExactArgs(2),
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
	rootCmd.AddCommand(codeRefactorAgent)
	rootCmd.AddCommand(tradeAssistantAgent)
	rootCmd.AddCommand(unitTestWriter)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
