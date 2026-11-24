package harness

import (
	"encoding/json"
	"fmt"
)

// BindState restores a capability's state when resuming and includes it in
// subsequent checkpoints. Bind a pointer before installing hooks and tools.
func BindState[T any](r *Run, name string, state *T) error {
	if name == "" || state == nil {
		return fmt.Errorf("state name and pointer required")
	}
	if _, ok := r.bindings[name]; ok {
		return fmt.Errorf("duplicate state binding %q", name)
	}
	if raw, ok := r.saved[name]; ok {
		if err := json.Unmarshal(raw, state); err != nil {
			return fmt.Errorf("restore %s: %w", name, err)
		}
	}
	r.bindings[name] = state
	return nil
}
func (r *Run) saveState() (map[string]json.RawMessage, error) {
	if len(r.bindings) == 0 {
		return nil, nil
	}
	saved := make(map[string]json.RawMessage, len(r.bindings))
	for k, v := range r.bindings {
		b, e := json.Marshal(v)
		if e != nil {
			return nil, fmt.Errorf("save %s: %w", k, e)
		}
		saved[k] = b
	}
	return saved, nil
}
