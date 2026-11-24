package capabilities

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"

	h "github.com/natexcvi/go-llm"
)

type Task struct {
	ID        string   `json:"id,omitempty"`
	Content   string   `json:"content"`
	Status    string   `json:"status,omitempty" jsonschema:"enum=pending,enum=in_progress,enum=completed,enum=cancelled,enum=blocked"`
	Parent    string   `json:"parent_id,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}
type taskID struct {
	ID string `json:"task_id"`
}
type statusUpdate struct {
	ID     string `json:"task_id"`
	Status string `json:"status"`
}
type plan struct{ tasks []Task }

func (p *plan) replace(tasks []Task) ([]Task, error) {
	byID := map[string]Task{}
	for i := range tasks {
		t := &tasks[i]
		if t.ID == "" {
			t.ID = rand.Text()[:8]
		}
		if t.Status == "" {
			t.Status = "pending"
		}
		if strings.TrimSpace(t.Content) == "" || !slices.Contains([]string{"pending", "in_progress", "completed", "cancelled", "blocked"}, t.Status) {
			return nil, h.Retry(errors.New("content and a valid status are required"))
		}
		if _, ok := byID[t.ID]; ok {
			return nil, h.Retry(fmt.Errorf("duplicate task %s", t.ID))
		}
		byID[t.ID] = *t
	}
	// Check both hierarchy and prerequisite edges before committing the batch.
	visited := map[string]bool{}
	active := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if active[id] {
			return fmt.Errorf("cyclic task reference %s", id)
		}
		t, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown task %s", id)
		}
		active[id] = true
		edges := slices.Clone(t.DependsOn)
		if t.Parent != "" {
			edges = append(edges, t.Parent)
		}
		for _, dep := range edges {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(active, id)
		visited[id] = true
		return nil
	}
	for _, t := range tasks {
		if err := visit(t.ID); err != nil {
			return nil, h.Retry(err)
		}
	}
	p.tasks = tasks
	return p.tasks, nil
}
func (p *plan) index(id string) (int, error) {
	i := slices.IndexFunc(p.tasks, func(t Task) bool { return t.ID == id })
	if i < 0 {
		return i, h.Retry(fmt.Errorf("unknown task %s", id))
	}
	return i, nil
}
func (p *plan) update(updates []statusUpdate) ([]Task, error) {
	next := slices.Clone(p.tasks)
	for _, u := range updates {
		i, err := p.index(u.ID)
		if err != nil {
			return nil, err
		}
		next[i].Status = u.Status
	}
	return p.replace(next)
}
func Planning() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		p := &plan{tasks: []Task{}}
		if err := h.BindState(r, "planning", &p.tasks); err != nil {
			return err
		}
		return r.Register(
			h.NewTool("write_plan", "Replace the plan with a validated list of tasks.", func(_ context.Context, in struct {
				Items []Task `json:"items"`
			}) ([]Task, error) {
				return p.replace(in.Items)
			}),
			h.NewTool("read_plan", "Read the current plan.", func(context.Context, struct{}) ([]Task, error) { return p.tasks, nil }),
			h.NewTool("add_task", "Add a task.", func(_ context.Context, t Task) ([]Task, error) { return p.replace(append(slices.Clone(p.tasks), t)) }),
			h.NewTool("update_task_status", "Update one task.", func(_ context.Context, u statusUpdate) ([]Task, error) { return p.update([]statusUpdate{u}) }),
			h.NewTool("update_task_statuses", "Atomically update task statuses.", func(_ context.Context, in struct {
				Updates []statusUpdate `json:"updates"`
			}) ([]Task, error) {
				return p.update(in.Updates)
			}),
			h.NewTool("remove_task", "Remove a task and its references.", func(_ context.Context, in taskID) ([]Task, error) {
				i, err := p.index(in.ID)
				if err != nil {
					return nil, err
				}
				next := slices.Delete(slices.Clone(p.tasks), i, i+1)
				for j := range next {
					if next[j].Parent == in.ID {
						next[j].Parent = ""
					}
					next[j].DependsOn = slices.DeleteFunc(slices.Clone(next[j].DependsOn), func(id string) bool { return id == in.ID })
				}
				return p.replace(next)
			}),
			h.NewTool("set_dependency", "Require another task to complete first.", func(_ context.Context, in struct {
				Task    string `json:"task_id"`
				Depends string `json:"depends_on_id"`
			}) ([]Task, error) {
				i, err := p.index(in.Task)
				if err != nil {
					return nil, err
				}
				next := slices.Clone(p.tasks)
				if !slices.Contains(next[i].DependsOn, in.Depends) {
					next[i].DependsOn = append(slices.Clone(next[i].DependsOn), in.Depends)
				}
				return p.replace(next)
			}),
			h.NewTool("get_available_tasks", "List pending tasks whose prerequisites are complete.", func(context.Context, struct{}) ([]Task, error) {
				var result = []Task{}
				for _, t := range p.tasks {
					if t.Status != "pending" {
						continue
					}
					ok := true
					for _, d := range t.DependsOn {
						i, _ := p.index(d)
						if p.tasks[i].Status != "completed" {
							ok = false
							break
						}
					}
					if ok {
						result = append(result, t)
					}
				}
				return result, nil
			}),
		)
	}
}
