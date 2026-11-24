package capabilities

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	h "github.com/natexcvi/go-llm"
)

// PeerNetwork connects concurrently running agents in one application scope.
// Supply a distinct network per tenant. Messages are delivered before requests.
type PeerNetwork struct {
	mu      sync.Mutex
	inboxes map[string][]peerMessage
}
type peerMessage struct {
	From string `json:"from"`
	Text string `json:"text"`
}

func Peers(network *PeerNetwork, name string) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if network == nil || name == "" {
			return errors.New("peer network and name are required")
		}
		network.mu.Lock()
		if network.inboxes == nil {
			network.inboxes = map[string][]peerMessage{}
		}
		if _, exists := network.inboxes[name]; exists {
			network.mu.Unlock()
			return fmt.Errorf("peer %q is already running", name)
		}
		network.inboxes[name] = nil
		network.mu.Unlock()
		r.OnClose = append(r.OnClose, func() error {
			network.mu.Lock()
			defer network.mu.Unlock()
			delete(network.inboxes, name)
			return nil
		})
		var delivered []peerMessage
		if err := h.BindState(r, "peers."+name, &delivered); err != nil {
			return err
		}
		r.BeforeRequest = append(r.BeforeRequest, func(ctx context.Context, q *h.Request, _ h.Usage) error {
			network.mu.Lock()
			delivered = append(delivered, network.inboxes[name]...)
			network.inboxes[name] = nil
			network.mu.Unlock()
			for _, m := range delivered {
				q.Messages = append(q.Messages, h.Message{Role: h.User, Text: fmt.Sprintf("Message from peer %q:\n%s", m.From, m.Text)})
			}
			return nil
		})
		return r.Register(
			h.NewTool("list_peers", "List other active peers, optionally filtered by name.", func(_ context.Context, in struct {
				Query string `json:"query,omitempty"`
			}) ([]string, error) {
				network.mu.Lock()
				defer network.mu.Unlock()
				names := []string{}
				for peer := range network.inboxes {
					if peer != name && strings.Contains(peer, in.Query) {
						names = append(names, peer)
					}
				}
				sort.Strings(names)
				return names, nil
			}),
			h.NewTool("send_message", "Send text to an active peer.", func(_ context.Context, in struct {
				To   string `json:"to"`
				Text string `json:"text"`
			}) (string, error) {
				network.mu.Lock()
				defer network.mu.Unlock()
				inbox, ok := network.inboxes[in.To]
				if !ok {
					return "", h.Retry(errors.New("peer is unavailable"))
				}
				if len(inbox) >= 128 || len(in.Text) > 16384 {
					return "", h.Retry(errors.New("peer inbox or message size limit reached"))
				}
				network.inboxes[in.To] = append(inbox, peerMessage{From: name, Text: in.Text})
				return "queued", nil
			}),
		)
	}
}
