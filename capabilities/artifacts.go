package capabilities

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	h "github.com/natexcvi/go-llm"
)

// ArtifactCheck inspects a persistent artifact before writing or exposing it.
type ArtifactCheck func(name, text string) error

var ErrArtifactBlocked = errors.New("artifact blocked")
var instructionOverride = regexp.MustCompile(`(?i)\b(ignore|disregard)\s+(all\s+)?(prior|previous|system)\s+(instructions|prompts)\b`)
var credentialRequest = regexp.MustCompile(`(?i)\b(send|upload|reveal|exfiltrate)\b[^\n]{0,80}\b(api[ _-]?keys?|credentials|access[ _-]?tokens?)\b`)

// ScanArtifact reports common instruction overrides, credential requests and
// hidden formatting controls. Applications may combine it with their own checks.
func ScanArtifact(name, text string) error {
	for _, s := range []string{name, text} {
		if strings.IndexFunc(s, func(c rune) bool { return unicode.Is(unicode.Cf, c) || c == 0 }) >= 0 {
			return fmt.Errorf("%w: hidden text control", ErrArtifactBlocked)
		}
		if instructionOverride.MatchString(s) {
			return fmt.Errorf("%w: instruction override", ErrArtifactBlocked)
		}
		if credentialRequest.MatchString(s) {
			return fmt.Errorf("%w: credential disclosure request", ErrArtifactBlocked)
		}
	}
	return nil
}
func checkArtifact(checks []ArtifactCheck, name, text string) error {
	for _, check := range checks {
		if err := check(name, text); err != nil {
			return err
		}
	}
	return nil
}

// InjectionPolicy screens successful tool results, optionally limiting checks to selected
// tool names. OnDetection receives the call and reason, without the retrieved content.
// Block replaces flagged output before it enters model history or tool-result events.
type InjectionPolicy struct {
	Check       ArtifactCheck
	Select      func(string) bool
	OnDetection func(context.Context, h.Call, error) error
	Block       bool
	Replacement string
}

func PromptInjectionDefender(policy InjectionPolicy) h.Capability {
	check := policy.Check
	if check == nil {
		check = ScanArtifact
	}
	return wrap(func(next h.ToolHandler) h.ToolHandler {
		return func(ctx context.Context, c h.Call) (string, error) {
			content, err := next(ctx, c)
			if err != nil || policy.Select != nil && !policy.Select(c.Name) {
				return content, err
			}
			reason := check(c.Name, content)
			if reason == nil {
				return content, nil
			}
			if policy.OnDetection != nil {
				if err := policy.OnDetection(ctx, c, reason); err != nil {
					return "", err
				}
			}
			if !policy.Block {
				return content, nil
			}
			if policy.Replacement != "" {
				return policy.Replacement, nil
			}
			return "Tool result withheld after prompt injection detection.", nil
		}
	})
}
