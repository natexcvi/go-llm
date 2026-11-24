package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/natexcvi/go-llm/workspace"
)

func TestConfinedWritesAndEdits(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	w, err := workspace.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	for _, name := range []string{"../escape", "link/escape"} {
		if err := w.WriteFile(name, []byte("bad")); err == nil {
			t.Fatal(name)
		}
	}
	if err = w.WriteFile("nested/file", []byte("same same")); err != nil {
		t.Fatal(err)
	}
	if err = w.Edit("nested/file", "same", "oops"); err == nil {
		t.Fatal("ambiguous edit accepted")
	}
	b, err := workspace.ReadFile(w, "nested/file", 64)
	if err != nil || string(b) != "same same" {
		t.Fatal(string(b), err)
	}
	if err = w.Edit("nested/file", "same same", "done"); err != nil {
		t.Fatal(err)
	}
	b, err = workspace.ReadFile(w, "nested/file", 64)
	if err != nil || string(b) != "done" {
		t.Fatal(string(b), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = w.Execute(ctx, workspace.Command{Program: "go", Args: []string{"version"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
