// Package guardrails provides configurable safety rules for LLM requests and responses.
//
// Usage:
//
//	engine := guardrails.New(
//	    guardrails.MaxInputLength(4000),
//	    guardrails.MaxOutputTokens(512),
//	    guardrails.BlockKeywords([]string{"CONFIDENTIAL", "password"}),
//	    guardrails.BlockPIIPatterns(),
//	)
//	if err := engine.CheckRequest(&req); err != nil { ... }
//	if err := engine.CheckResponse(&resp); err != nil { ... }
package guardrails

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Vedanshu7/llmbridge/types"
)

// Rule inspects an LLM request or response and returns an error if it violates a policy.
type Rule interface {
	CheckRequest(req *types.Request) error
	CheckResponse(resp *types.Response) error
}

// Engine runs an ordered list of Rules against requests and responses.
type Engine struct {
	rules []Rule
}

// New returns an Engine that applies the given rules in order.
func New(rules ...Rule) *Engine {
	return &Engine{rules: rules}
}

// CheckRequest runs all rules against a request. Returns the first violation found.
func (e *Engine) CheckRequest(req *types.Request) error {
	for _, r := range e.rules {
		if err := r.CheckRequest(req); err != nil {
			return err
		}
	}
	return nil
}

// CheckResponse runs all rules against a response. Returns the first violation found.
func (e *Engine) CheckResponse(resp *types.Response) error {
	for _, r := range e.rules {
		if err := r.CheckResponse(resp); err != nil {
			return err
		}
	}
	return nil
}

// ---- Built-in rules ----

// MaxInputLengthRule blocks requests where the total message content exceeds
// the given character limit.
type MaxInputLengthRule struct{ limit int }

// MaxInputLength returns a rule that rejects requests exceeding charLimit characters.
func MaxInputLength(charLimit int) Rule { return &MaxInputLengthRule{limit: charLimit} }

func (r *MaxInputLengthRule) CheckRequest(req *types.Request) error {
	total := len(req.System)
	for _, m := range req.Messages {
		total += len(m.Content)
	}
	if total > r.limit {
		return fmt.Errorf("guardrails: input length %d exceeds limit %d", total, r.limit)
	}
	return nil
}
func (r *MaxInputLengthRule) CheckResponse(_ *types.Response) error { return nil }

// MaxOutputTokensRule blocks responses that exceed a token count.
type MaxOutputTokensRule struct{ limit int }

// MaxOutputTokens returns a rule that rejects responses with more than limit completion tokens.
func MaxOutputTokens(limit int) Rule { return &MaxOutputTokensRule{limit: limit} }

func (r *MaxOutputTokensRule) CheckRequest(_ *types.Request) error { return nil }
func (r *MaxOutputTokensRule) CheckResponse(resp *types.Response) error {
	if resp == nil || resp.Usage == nil {
		return nil
	}
	if resp.Usage.CompletionTokens > r.limit {
		return fmt.Errorf("guardrails: completion tokens %d exceeds limit %d",
			resp.Usage.CompletionTokens, r.limit)
	}
	return nil
}

// MaxOutputLengthRule blocks responses whose content exceeds a character limit.
type MaxOutputLengthRule struct{ limit int }

// MaxOutputLength returns a rule that rejects responses exceeding charLimit characters.
func MaxOutputLength(charLimit int) Rule { return &MaxOutputLengthRule{limit: charLimit} }

func (r *MaxOutputLengthRule) CheckRequest(_ *types.Request) error { return nil }
func (r *MaxOutputLengthRule) CheckResponse(resp *types.Response) error {
	if resp == nil {
		return nil
	}
	if len(resp.Content) > r.limit {
		return fmt.Errorf("guardrails: response length %d exceeds limit %d",
			len(resp.Content), r.limit)
	}
	return nil
}

// BlockKeywordsRule rejects any message or response that contains one of the given words.
// Matching is case-insensitive.
type BlockKeywordsRule struct{ words []string }

// BlockKeywords returns a rule that rejects requests and responses containing any of the words.
func BlockKeywords(words []string) Rule {
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	return &BlockKeywordsRule{words: lower}
}

func (r *BlockKeywordsRule) CheckRequest(req *types.Request) error {
	texts := []string{req.System}
	for _, m := range req.Messages {
		texts = append(texts, m.Content)
	}
	for _, t := range texts {
		lt := strings.ToLower(t)
		for _, w := range r.words {
			if strings.Contains(lt, w) {
				return fmt.Errorf("guardrails: blocked keyword %q found in request", w)
			}
		}
	}
	return nil
}

func (r *BlockKeywordsRule) CheckResponse(resp *types.Response) error {
	if resp == nil {
		return nil
	}
	lt := strings.ToLower(resp.Content)
	for _, w := range r.words {
		if strings.Contains(lt, w) {
			return fmt.Errorf("guardrails: blocked keyword %q found in response", w)
		}
	}
	return nil
}

// PIIRule blocks messages that match common PII patterns (email, SSN, credit card).
type PIIRule struct {
	patterns []*regexp.Regexp
}

// BlockPIIPatterns returns a rule that rejects requests and responses containing common
// PII patterns: email addresses, US SSNs, and credit card numbers.
func BlockPIIPatterns() Rule {
	return &PIIRule{
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
			regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),                    // SSN
			regexp.MustCompile(`\b(?:4\d{12}(?:\d{3})?|5[1-5]\d{14})\b`),   // Visa/Mastercard
		},
	}
}

// BlockPIIPatternsCustom returns a PIIRule using caller-provided regex patterns.
func BlockPIIPatternsCustom(patterns []string) (Rule, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("guardrails: invalid PII pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	return &PIIRule{patterns: compiled}, nil
}

func (r *PIIRule) check(text string) error {
	for _, re := range r.patterns {
		if m := re.FindString(text); m != "" {
			return fmt.Errorf("guardrails: PII pattern matched in content")
		}
	}
	return nil
}

func (r *PIIRule) CheckRequest(req *types.Request) error {
	if err := r.check(req.System); err != nil {
		return err
	}
	for _, m := range req.Messages {
		if err := r.check(m.Content); err != nil {
			return err
		}
	}
	return nil
}

func (r *PIIRule) CheckResponse(resp *types.Response) error {
	if resp == nil {
		return nil
	}
	return r.check(resp.Content)
}

// RegexRule blocks content matching a custom regular expression.
type RegexRule struct {
	re  *regexp.Regexp
	msg string
}

// BlockRegex returns a rule that rejects requests and responses matching the pattern.
// msg is the human-readable error message returned on match.
func BlockRegex(pattern, msg string) (Rule, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("guardrails: invalid pattern %q: %w", pattern, err)
	}
	return &RegexRule{re: re, msg: msg}, nil
}

func (r *RegexRule) CheckRequest(req *types.Request) error {
	texts := append([]string{req.System}, func() []string {
		s := make([]string, len(req.Messages))
		for i, m := range req.Messages {
			s[i] = m.Content
		}
		return s
	}()...)
	for _, t := range texts {
		if r.re.MatchString(t) {
			return fmt.Errorf("guardrails: %s", r.msg)
		}
	}
	return nil
}

func (r *RegexRule) CheckResponse(resp *types.Response) error {
	if resp == nil {
		return nil
	}
	if r.re.MatchString(resp.Content) {
		return fmt.Errorf("guardrails: %s", r.msg)
	}
	return nil
}
