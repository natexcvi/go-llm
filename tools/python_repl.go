package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

type PythonREPL struct {
	pythonBinary string
}

func (p *PythonREPL) createVenv() error {
	if _, err := os.Stat(".venv"); err == nil {
		return nil
	}
	_, err := exec.Command(p.pythonBinary, "-m", "venv", ".venv").Output()
	if err != nil {
		return err
	}
	return nil
}

func (p *PythonREPL) installModules(modules []string) error {
	args := []string{"-m", "pip", "install"}
	args = append(args, modules...)
	_, err := exec.Command("./.venv/bin/python", args...).Output()
	if err != nil {
		return err
	}
	return nil
}

func (p *PythonREPL) Execute(args json.RawMessage) (json.RawMessage, error) {
	var command struct {
		Code    string   `json:"code"`
		Modules []string `json:"modules"`
	}
	err := json.Unmarshal(args, &command)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %w", err)
	}
	err = p.createVenv()
	if err != nil {
		return nil, fmt.Errorf("failed to create venv: %w", err)
	}
	if len(command.Modules) > 0 {
		err = p.installModules(command.Modules)
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("failed to install modules: %s", string(exitError.Stderr))
			}
			return nil, fmt.Errorf("failed to install modules: %w", err)
		}
	}
	out, err := exec.Command("./.venv/bin/python", "-c", command.Code).Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("python exited with code %d: %s", exitError.ExitCode(), string(exitError.Stderr))
		}
		return nil, err
	}
	return json.RawMessage(out), nil
}

func (p *PythonREPL) Name() string {
	return "python"
}

func (p *PythonREPL) Description() string {
	return "A Python REPL. Use this to execute scripts that help you complete your task. " +
		"If you need to install any modules, you can do so by passing a list of modules to the modules argument."
}

func (p *PythonREPL) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"code": "the Python code to execute, properly escaped", "modules": ["a list", "of modules", "to install"]}`)
}

func (p *PythonREPL) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}

func NewPythonREPL() *PythonREPL {
	return &PythonREPL{
		pythonBinary: "python3",
	}
}

func NewPythonREPLWithCustomBinary(pythonBinary string) *PythonREPL {
	return &PythonREPL{
		pythonBinary: pythonBinary,
	}
}
