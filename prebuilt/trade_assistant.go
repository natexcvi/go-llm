package prebuilt

import (
	"encoding/json"
	"fmt"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
)

type TradeAssistantRequest struct {
	Stocks []string `json:"stocks"`
}

func (r TradeAssistantRequest) Encode() string {
	return fmt.Sprintf(`{"stocks": %s}`, r.Stocks)
}

func (r TradeAssistantRequest) Schema() string {
	return `{"stocks": "list of stock tickers"}`
}

type Recommendation string

const (
	RecommendationBuy  Recommendation = "buy"
	RecommendationSell Recommendation = "sell"
	RecommendationHold Recommendation = "hold"
)

type TradeAssistantResponse struct {
	Recommendations map[string]Recommendation
}

func (r TradeAssistantResponse) Encode() string {
	marshalled, err := json.Marshal(r.Recommendations)
	if err != nil {
		panic(err)
	}
	return string(marshalled)
}

func (r TradeAssistantResponse) Schema() string {
	return `{"recommendations": {"ticker": "buy/sell/hold"}}`
}

func NewTradeAssistantAgent(engine engines.LLM, wolframAlphaAppID string) agents.Agent[TradeAssistantRequest, TradeAssistantResponse] {
	task := &agents.Task[TradeAssistantRequest, TradeAssistantResponse]{
		Description: "You will be given a list of stocks. " +
			"Your task is to recommend whether to buy, sell, or hold each stock.",
		Examples: []agents.Example[TradeAssistantRequest, TradeAssistantResponse]{
			{
				Input: TradeAssistantRequest{
					Stocks: []string{"AAPL", "MSFT", "GOOG"},
				},
				Answer: TradeAssistantResponse{
					Recommendations: map[string]Recommendation{
						"AAPL": RecommendationBuy,
						"MSFT": RecommendationSell,
						"GOOG": RecommendationHold,
					},
				},
				IntermediarySteps: []*engines.ChatMessage{
					(&agents.ChainAgentThought{
						Content: "I should look up the stock price for each stock.",
					}).Encode(engine),
					(&agents.ChainAgentAction{
						Tool: tools.NewWolframAlpha(wolframAlphaAppID),
						Args: json.RawMessage(`{"query": "stock price of AAPL"}`),
					}).Encode(engine),
					(&agents.ChainAgentObservation{
						Content: "AAPL is currently trading at $100.00",
					}).Encode(engine),
				},
			},
		},
		AnswerParser: func(text string) (TradeAssistantResponse, error) {
			var recommendations map[string]Recommendation
			if err := json.Unmarshal([]byte(text), &recommendations); err != nil {
				return TradeAssistantResponse{}, err
			}
			return TradeAssistantResponse{
				Recommendations: recommendations,
			}, nil
		},
	}
	return agents.NewChainAgent(engine, task, memory.NewSummarisedMemory(3, engine)).WithMaxSolutionAttempts(12).WithTools(
		tools.NewGoogleSearch(),
		tools.NewIsolatedPythonREPL(),
		tools.NewWolframAlpha(wolframAlphaAppID),
		tools.NewWebpageSummary(engine),
	)
}
