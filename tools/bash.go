package tools

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type BashTerminal struct {
}

func (b *BashTerminal) Execute(args json.RawMessage) (json.RawMessage, error) {
	var command struct {
		Command string `json:"command"`
	}
	err := json.Unmarshal(args, &command)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("bash", "-c", command.Command).Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("bash exited with code %d: %s", exitError.ExitCode(), string(exitError.Stderr))
		}
		return nil, err
	}
	return json.RawMessage(out), nil
}

func (b *BashTerminal) Name() string {
	return "bash"
}

func (b *BashTerminal) Description() string {
	return "A tool for executing bash commands. Important! This tool is not sandboxed, so it can do anything on the host machine."
}

func (b *BashTerminal) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"command": "the bash command to execute"}`)
}

func NewBashTerminal() *BashTerminal {
	return &BashTerminal{}
}
