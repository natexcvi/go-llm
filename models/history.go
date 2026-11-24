package models

import h "github.com/natexcvi/go-llm"

// Preserve standing instructions when replaying a server-minted boundary.
// Local compaction strategies already selected which history to retain.
func effectiveHistory(messages []h.Message, transport, endpoint string) []h.Message {
	for i := len(messages) - 1; i > 0; i-- {
		n := messages[i].Native
		if n != nil && n.Boundary && n.Transport == transport && n.Endpoint == endpoint {
			var out []h.Message
			for _, m := range messages[:i] {
				if m.Role == h.System {
					out = append(out, m)
				}
			}
			return append(out, messages[i:]...)
		}
	}
	return messages
}
