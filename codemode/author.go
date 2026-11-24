package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/codemode/internal/wire"
	"github.com/natexcvi/go-llm/storage"
)

// AuthoredCapability is a persisted, named Starlark tool. Its program
// receives one params dictionary and returns its final expression.
type AuthoredCapability struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Code        string `json:"code"`
	Disabled    bool   `json:"disabled,omitempty"`
}

func validName(name string) bool { return len(name) <= 56 && identifier.MatchString(name) }
func (e *Engine) validate(ctx context.Context, c AuthoredCapability) error {
	if !validName(c.Name) || c.Code == "" || len(c.Code) > 64<<10 || len(c.Description) > 4096 {
		return h.Retry(errors.New("invalid authored name, code or description"))
	}
	_, err := e.evaluate(ctx, wire.Request{Code: c.Code, Params: json.RawMessage("{}"), ValidateOnly: true})
	return err
}
func readAuthored(ctx context.Context, store storage.Store, key string) (AuthoredCapability, error) {
	var c AuthoredCapability
	raw, err := store.Get(ctx, key)
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	if err == nil && (!validName(c.Name) || key != c.Name+".json") {
		err = fmt.Errorf("invalid capability record %q", key)
	}
	return c, err
}
func writeAuthored(ctx context.Context, store storage.Store, c AuthoredCapability) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return store.Put(ctx, c.Name+".json", raw)
}

// CapabilityCreation offers author, list and disable tools over a dedicated
// store. Each name occupies one atomic record. Changes take effect on the next
// run configured with LoadActive. New scripts can define data transformations.
func (e *Engine) CapabilityCreation(store storage.Store) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if store == nil || e == nil || e.module == nil {
			return errors.New("code engine and authored capability store required")
		}
		author := h.NewTool("author_capability", "Validate and save a Starlark tool for subsequent runs. Read arguments from params and return a final expression.", func(ctx context.Context, in struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			Code        string `json:"code"`
		}) (string, error) {
			c := AuthoredCapability{Name: in.Name, Description: in.Description, Code: in.Code}
			if err := e.validate(ctx, c); err != nil {
				return "", err
			}
			e.authorMu.Lock()
			defer e.authorMu.Unlock()
			return "Saved authored_" + c.Name + " for the next run.", writeAuthored(ctx, store, c)
		})
		list := h.NewTool("list_authored_capabilities", "List saved capability names and activation status.", func(ctx context.Context, _ struct{}) (map[string]string, error) {
			keys, err := store.List(ctx)
			if err != nil {
				return nil, err
			}
			out := map[string]string{}
			for _, key := range keys {
				c, err := readAuthored(ctx, store, key)
				if err != nil {
					return nil, err
				}
				out[c.Name] = "active"
				if c.Disabled {
					out[c.Name] = "disabled"
				}
			}
			return out, nil
		})
		disable := h.NewTool("disable_authored_capability", "Disable an existing capability for subsequent runs.", func(ctx context.Context, in struct {
			Name string `json:"name"`
		}) (string, error) {
			if !validName(in.Name) {
				return "", h.Retry(errors.New("invalid name"))
			}
			e.authorMu.Lock()
			defer e.authorMu.Unlock()
			c, err := readAuthored(ctx, store, in.Name+".json")
			if errors.Is(err, fs.ErrNotExist) {
				return "", h.Retry(err)
			}
			if err != nil {
				return "", err
			}
			c.Disabled = true
			return in.Name, writeAuthored(ctx, store, c)
		})
		return r.Register(author, list, disable)
	}
}

// LoadActive installs the enabled scripts as authored_NAME tools. Each script
// is revalidated at activation and runs with the same WebAssembly resource limits.
func (e *Engine) LoadActive(store storage.Store) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if store == nil || e == nil || e.module == nil {
			return errors.New("code engine and authored capability store required")
		}
		keys, err := store.List(ctx)
		if err != nil {
			return err
		}
		for _, key := range keys {
			c, err := readAuthored(ctx, store, key)
			if err != nil {
				return err
			}
			if c.Disabled {
				continue
			}
			if err = e.validate(ctx, c); err != nil {
				return fmt.Errorf("stored capability %s: %w", c.Name, err)
			}
			t := h.NewTool("authored_"+c.Name, c.Description, func(ctx context.Context, params map[string]json.RawMessage) (string, error) {
				b, err := json.Marshal(params)
				if err != nil {
					return "", err
				}
				result, err := e.evaluate(ctx, wire.Request{Code: c.Code, Params: b})
				return strings.TrimSuffix(result.Printed+string(result.Result), "\n"), err
			})
			if err = r.Register(t); err != nil {
				return err
			}
		}
		return nil
	}
}
