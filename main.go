package main

import (
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

func readFile(filename string) string {
	content, err := os.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
	}
	return string(content)
}

func main() {
	configLogger("log.txt", log.DebugLevel)
	log.Debug("session started")
	engine := engines.NewGPTEngine(os.Getenv("OPENAI_TOKEN"), "gpt-3.5-turbo")
	agent, err := prebuilt.NewUnitTestWriter(engine)
	if err != nil {
		log.Error(err)
		return
	}
	res, err := agent.Run(prebuilt.UnitTestWriterRequest{
		SourceFile:  readFile("tools/key_value_store.go"),
		ExampleFile: readFile("tools/bash_test.go"),
	})
	if err != nil {
		log.Error(err)
		return
	}
	log.Info(res)
	err = os.WriteFile("tools/key_value_store_test.go", []byte(res.UnitTestFile), 0644)
	if err != nil {
		log.Error(err)
		return
	}
	log.Debug("session ended")
}
