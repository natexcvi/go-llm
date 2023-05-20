package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type AskUser struct {
	source io.Reader
}

func (b *AskUser) Execute(args json.RawMessage) (json.RawMessage, error) {
	var command struct {
		Question string `json:"question"`
	}
	err := json.Unmarshal(args, &command)
	if err != nil {
		return nil, err
	}
	fmt.Println(command.Question)
	answer, err := b.readUserInput()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the user did not provide an answer")
		}
		return nil, fmt.Errorf("error while reading user input: %s", err.Error())
	}
	var response struct {
		Answer string `json:"answer"`
	}
	response.Answer = answer
	return json.Marshal(response)
}

func (b *AskUser) readUserInput() (string, error) {
	var answer string
	n, err := fmt.Fscanln(b.source, &answer)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("no input")
	}
	return answer, nil
}

func (b *AskUser) Name() string {
	return "ask_user"
}

func (b *AskUser) Description() string {
	return "A tool for asking the user a question."
}

func (b *AskUser) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"question": "the question to ask the user"}`)
}

func (b *AskUser) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}

func NewAskUser() *AskUser {
	return &AskUser{
		source: os.Stdin,
	}
}

func NewAskUserWithSource(source io.Reader) *AskUser {
	return &AskUser{
		source: source,
	}
}
