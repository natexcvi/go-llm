package main

import (
	"encoding/json"
	"io"
	"os"

	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/prebuilt"
	log "github.com/sirupsen/logrus"
)

func configLogger(toFile string, level log.Level) {
	log.SetLevel(level)
	if toFile != "" {
		f, err := os.OpenFile(toFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Fatal(err)
		}
		logWriter := io.MultiWriter(os.Stderr, f)
		log.SetOutput(logWriter)
	}
}

func main() {
	configLogger("log.txt", log.DebugLevel)
	log.Debug("session started")
	engine := engines.NewGPTEngine(os.Getenv("OPENAI_TOKEN"), "gpt-3.5-turbo")
	agent := prebuilt.NewCodeRefactorAgent(engine)
	res, err := agent.Run(prebuilt.CodeBaseRefactorRequest{
		Dir: "/Users/nate/Git/go-llm/memory",
		Goal: "Write unit tests for summarised_memory.go, following the example of buffer_memory.go. " +
			"Use your best judgement, without asking me anything.",
	})
	if err != nil {
		log.Error(err)
		return
	}
	log.Info(res)
	f, err := os.Create("output.json")
	if err != nil {
		log.Error(err)
		return
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(res); err != nil {
		log.Error(err)
		return
	}
}
