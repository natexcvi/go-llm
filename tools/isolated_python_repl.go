package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type IsolatedPythonREPL struct {
	cli *client.Client
}

func NewIsolatedPythonREPL() *IsolatedPythonREPL {
	return &IsolatedPythonREPL{}
}

func (repl *IsolatedPythonREPL) init() error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	repl.cli = cli
	out, err := cli.ImagePull(ctx, "python:3.11.2-slim-buster", types.ImagePullOptions{})
	defer out.Close()
	if err != nil {
		return err
	}
	io.Copy(os.Stdout, out)
	return nil
}

func (repl *IsolatedPythonREPL) Execute(arg json.RawMessage) (json.RawMessage, error) {
	if repl.cli == nil {
		err := repl.init()
		if err != nil {
			return nil, err
		}
	}
	ctx := context.Background()
	var code struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(arg, &code); err != nil {
		return nil, err
	}
	resp, err := repl.cli.ContainerCreate(ctx, &container.Config{
		Image: "python:3.11.2-slim-buster",
		Cmd:   []string{"python", "-c", code.Code},
	}, nil, nil, nil, "")
	if err != nil {
		return json.RawMessage{}, err
	}
	if err := repl.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return json.RawMessage{}, err
	}
	statusCh, errCh := repl.cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return json.RawMessage{}, err
		}
	case <-statusCh:
	}
	res, err := repl.cli.ContainerLogs(ctx, resp.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: false, Details: false})
	if err != nil {
		return json.RawMessage{}, err
	}
	output, err := io.ReadAll(res)
	if err != nil {
		return json.RawMessage{}, err
	}
	var marshalledOutput json.RawMessage
	if strings.Contains(string(output), "\f") {
		trimmedOutput := strings.Split(string(output), "\f")[1]
		marshalledOutput, err = json.Marshal(trimmedOutput)
	} else {
		marshalledOutput, err = json.Marshal(string(output))
	}
	if err != nil {
		return json.RawMessage{}, err
	}
	return marshalledOutput, nil
}

func (repl *IsolatedPythonREPL) Name() string {
	return "python_repl"
}

func (repl *IsolatedPythonREPL) Description() string {
	return "A Python REPL that runs in a Docker container. " +
		"Use this to run any Python code that can help you complete your task."
}

func (repl *IsolatedPythonREPL) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"code": "the Python code to execute"}`)
}
