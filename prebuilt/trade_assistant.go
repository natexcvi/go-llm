package prebuilt

import (
	"encoding/json"

	"github.com/natexcvi/go-llm/agents"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
)

type TradeAssistantRequest struct {
	Stocks []string
}

func (req TradeAssistantRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(req)
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
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentThought{
							Content: "I should look up the stock prices for each stock.",
						}).Encode(),
					},
					{
						Role: engines.ConvRoleAssistant,
						Text: (&agents.ChainAgentAction{
							Tool: tools.NewBashTerminal(),
							Args: json.RawMessage(`{"command": "curl https://api.iextrading.com/1.0/stock/market/batch?symbols=AAPL,MSFT,GOOG&types=quote"}`),
						}).Encode(),
					},
				},
			},
		},
	}
	return agents.NewChainAgent(engine, task, memory.NewSummarisedMemory(3, engine)).WithMaxSolutionAttempts(12).WithTools(
		tools.NewGoogleSearch(),
		tools.NewIsolatedPythonREPL(),
		tools.NewWolframAlpha(wolframAlphaAppID),
		tools.NewWebpageSummary(engine),
	)
}
