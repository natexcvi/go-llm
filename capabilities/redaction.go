package capabilities

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	h "github.com/natexcvi/go-llm"
)

// TextFilter replaces text or returns an error to stop processing. Filters run
// in order so the next one inspects the sanitized value.
type TextFilter func(string) (string, error)

func filterText(filters []TextFilter, text string) (string, error) {
	var err error
	for _, f := range filters {
		if f == nil {
			return "", errors.New("nil text filter")
		}
		text, err = f(text)
		if err != nil {
			return "", err
		}
	}
	return text, nil
}
func FilterInput(filters ...TextFilter) h.Capability {
	filters = slices.Clone(filters)
	return func(_ context.Context, r *h.Run) error {
		return r.FilterPrompt(func(s string) (string, error) { return filterText(filters, s) })
	}
}
func FilterOutput(filters ...TextFilter) h.Capability {
	filters = slices.Clone(filters)
	return func(_ context.Context, r *h.Run) error {
		r.TransformText = append(r.TransformText, func(_ context.Context, text string) (string, error) { return filterText(filters, text) })
		return nil
	}
}
func FilterToolResults(filters ...TextFilter) h.Capability {
	filters = slices.Clone(filters)
	return wrap(func(next h.ToolHandler) h.ToolHandler {
		return func(ctx context.Context, c h.Call) (string, error) {
			s, err := next(ctx, c)
			if err != nil {
				return "", err
			}
			return filterText(filters, s)
		}
	})
}
func BlockedKeywords(keywords ...string) TextFilter {
	words := slices.Clone(keywords)
	for i := range words {
		words[i] = strings.ToLower(words[i])
	}
	return func(s string) (string, error) {
		lower := strings.ToLower(s)
		for _, word := range words {
			if word != "" && strings.Contains(lower, word) {
				return "", errors.New("text contains a blocked keyword")
			}
		}
		return s, nil
	}
}

type detector struct {
	label string
	re    *regexp.Regexp
	valid func(string) bool
}

func redact(text string, patterns []detector) (string, error) {
	for _, p := range patterns {
		text = p.re.ReplaceAllStringFunc(text, func(candidate string) string {
			if p.valid != nil && !p.valid(candidate) {
				return candidate
			}
			return "[redacted:" + p.label + "]"
		})
	}
	return text, nil
}

var secretPatterns = []detector{
	{"anthropic_key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), nil},
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), nil},
	{"aws_access_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), nil},
	{"github_token", regexp.MustCompile(`\b(?:gh[pousr]|github_pat)_[A-Za-z0-9_]{20,}`), nil},
	{"slack_token", regexp.MustCompile(`\bxox[bporas]-[A-Za-z0-9-]{10,}`), nil},
	{"slack_app_token", regexp.MustCompile(`\bxapp-\d-[A-Za-z0-9-]{10,}`), nil},
	{"stripe_key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{20,}`), nil},
	{"stripe_webhook_secret", regexp.MustCompile(`\bwhsec_[A-Za-z0-9]{20,}`), nil},
	{"google_api_key", regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}`), nil},
	{"google_oauth_secret", regexp.MustCompile(`\bGOCSPX-[A-Za-z0-9_-]{20,}`), nil},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), nil},
	{"private_key", regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(?:\r?\n|\\n).*?(?:-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|\z)`), nil},
}

func RedactSecrets(s string) (string, error) { return redact(s, secretPatterns) }

var piiPatterns = []detector{
	{"iban", regexp.MustCompile(`(?i)\b(?:AD|AE|AL|AT|AZ|BA|BE|BG|BH|BI|BR|BY|CH|CR|CY|CZ|DE|DJ|DK|DO|EE|EG|ES|FI|FK|FO|FR|GB|GE|GI|GL|GR|GT|HN|HR|HU|IE|IL|IQ|IS|IT|JO|KW|KZ|LB|LC|LI|LT|LU|LV|LY|MA|MC|MD|ME|MK|MN|MR|MT|MU|NI|NL|NO|OM|PK|PL|PS|PT|QA|RO|RS|RU|SA|SC|SD|SE|SI|SK|SM|SO|ST|SV|TL|TN|TR|UA|VA|VG|XK)\d{2}(?:[A-Z0-9]{10,30}|(?: [A-Z0-9]{4}){2,7}(?: [A-Z0-9]{1,3})?)\b`), validIBAN},
	{"credit_card", regexp.MustCompile(`\b[2-6](?:[ -]?\d){12,18}\b`), validCard},
	{"us_ssn", regexp.MustCompile(`\b\d{3}[- ]\d{2}[- ]\d{4}\b`), nil},
	{"email", regexp.MustCompile(`[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9-]{1,63}(?:\.[A-Za-z0-9-]{1,63})*\.[A-Za-z]{2,24}\b`), nil},
}

func RedactPersonalData(s string) (string, error) { return redact(s, piiPatterns) }
func validIBAN(s string) bool {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	n := 0
	for _, c := range s[4:] + s[:4] {
		if c >= 'A' {
			n = (n*100 + int(c-'A') + 10) % 97
		} else {
			n = (n*10 + int(c-'0')) % 97
		}
	}
	return n == 1
}
func validCard(s string) bool {
	sum := 0
	double := false
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] < '0' || s[i] > '9' {
			continue
		}
		n := int(s[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}
