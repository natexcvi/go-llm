package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
)

type UnitConversionRequest struct {
	From  string
	To    string
	Value float32
}

func (req UnitConversionRequest) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"from": "%s", "to": "%s", "value": %f}`, req.From, req.To, req.Value)), nil
}

type UnitConversionResponse struct {
	Value float32
}

func main() {
	task := &agents.Task[UnitConversionRequest, UnitConversionResponse]{
		Description: "Converting units",
		Examples: []agents.Example[UnitConversionRequest, UnitConversionResponse]{
			{
				Input: UnitConversionRequest{
					From:  "miles",
					To:    "kilometers",
					Value: 1,
				},
				Answer: UnitConversionResponse{
					Value: 1.609344,
				},
				IntermediarySteps: []*engines.ChatMessage{
					{
						Role: engines.ConvRoleAssistant,
						Text: "THT: I should write Python code to convert miles to kilometers",
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: `ACT: python({"code": "def convert_miles_to_kilometers(miles):\n    return miles * 1.609344\nprint(convert_miles_to_kilometers(1))"})`,
					},
					{
						Role: engines.ConvRoleSystem,
						Text: "OBS: 1.609344",
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: "THT: I now know the final answer.",
					},
				},
			},
		},
		AnswerParser: func(msg string) (UnitConversionResponse, error) {
			var res UnitConversionResponse
			if err := json.Unmarshal([]byte(msg), &res); err != nil {
				return UnitConversionResponse{}, err
			}
			return res, nil
		},
	}
	agent := agents.NewChainAgent(engines.NewGPTEngine(os.Getenv("OPENAI_TOKEN"), "gpt-3.5-turbo"), task, memory.NewBufferedMemory(0)).WithMaxSolutionAttempts(12).WithTools(tools.NewPythonREPL(), tools.NewBashTerminal())
	res, err := agent.Run(UnitConversionRequest{
		From:  "USD",
		To:    "GBP",
		Value: 1,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", res)
}
