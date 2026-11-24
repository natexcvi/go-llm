package browser

import (
	"context"
	"errors"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
)

// BrowserUse registers browse_web(task) on the parent. Each delegation opens
// a fresh Chromium session. The child has its own history and request budget.
// Review can inspect the child's proposed answer and return corrective feedback.
type BrowserUse struct {
	Browser  Chromium
	Model    h.Model
	MaxSteps int
	Review   func(context.Context, string) (string, error)
}

func (b BrowserUse) Capability() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		steps := b.MaxSteps
		if steps < 0 {
			return errors.New("negative browser step limit")
		}
		if steps == 0 {
			steps = 50
		}
		model := b.Model
		if model == nil {
			model = r.Model
		}
		child := h.Agent[string]{Model: model, Output: h.Text(), Limits: h.Limits{Requests: steps}, Instructions: "Complete the assigned browsing task within the available origins. Read the page and report what you observed. If a step fails, state what remains incomplete.", Capabilities: []h.Capability{b.Browser.Capability(), capabilities.DetectLoops(4)}}
		if b.Review != nil {
			child.Capabilities = append(child.Capabilities, capabilities.Goal("Verify the observed browser result", b.Review))
		}
		return capabilities.SubAgent("browse_web", "Delegate a self-contained web browsing task to a Chromium agent.", child)(ctx, r)
	}
}
