package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
)

type IsolatedPythonREPL struct {
}

func NewIsolatedPythonREPL() *IsolatedPythonREPL {
	return &IsolatedPythonREPL{}
}

func (repl *IsolatedPythonREPL) Execute(arg json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Code    string   `json:"code"`
		Modules []string `json:"modules"`
	}
	err := json.Unmarshal(arg, &args)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %w", err)
	}
	tmpDir, err := os.MkdirTemp("", "python_repl")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	err = os.WriteFile(path.Join(tmpDir, "script.py"), []byte(args.Code), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write script to file: %w", err)
	}
	cmdArgs := []string{"run", "--rm", "-v", fmt.Sprintf("%s:/app", tmpDir), "python:3.11-alpine"}
	shArgs := []string{}
	if len(args.Modules) > 0 {
		shArgs = append(shArgs, "python", "-m", "pip", "install", "--quiet")
		shArgs = append(shArgs, args.Modules...)
		shArgs = append(shArgs, "&&")
	}
	shArgs = append(shArgs, "python", path.Join("app", "script.py"))
	cmdArgs = append(cmdArgs, "sh", "-c", strings.Join(shArgs, " "))
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("python exited with code %d: %s", exitError.ExitCode(), string(exitError.Stderr))
		}
		return nil, err
	}
	return json.Marshal(string(out))
}

func (repl *IsolatedPythonREPL) Name() string {
	return "isolated_python"
}

func (repl *IsolatedPythonREPL) Description() string {
	return "A Python REPL that runs in a Docker container. " +
		"Use this to run any Python code that can help you complete your task."
}

func (repl *IsolatedPythonREPL) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"code": "the Python code to execute", "modules": ["a list", "of modules", "to install"]}`)
}
