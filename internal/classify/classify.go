package classify

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kinds assigned to attention events.
const (
	KindNeedsDecision = "needs_decision"
	KindSettled       = "settled"
	KindUnknownIdle   = "unknown_idle"
	KindSkip          = "skip"
)

// Input is one agent snapshot plus optional prior status.
type Input struct {
	Status     string
	PrevStatus string
	Title      string
	Detection  string
	Excerpt    string
}

// Result is a cheap classification. No LLM.
type Result struct {
	Kind   string
	Reason string
}

var questionPhrases = []string{
	"do you want",
	"would you like",
	"shall i",
	"should i",
	"can i ",
	"may i ",
	"allow this",
	"allow the",
	"allow edit",
	"allow herdr",
	"needs approval",
	"waiting for your",
	"needs your",
	"awaiting confirmation",
	"awaiting approval",
	"[y/n]",
	"(y/n)",
	"(y/n",
	"yes / no",
	"yes/no",
	"continue?",
	"proceed?",
	"confirm?",
}

// Classify assigns a herd kind. working is skipped. blocked is always
// needs_decision. Unrecognized questions that present as idle are treated as
// needs_decision when the previous status was working.
func Classify(in Input) Result {
	status := strings.ToLower(strings.TrimSpace(in.Status))
	prev := strings.ToLower(strings.TrimSpace(in.PrevStatus))

	switch status {
	case "", "working":
		return Result{Kind: KindSkip, Reason: "working"}
	}

	text := combinedText(in)
	strong := strongQuestion(in.Title, text)
	prompt := lastNonEmptyLooksPrompt(text)

	switch status {
	case "blocked":
		return Result{Kind: KindNeedsDecision, Reason: "herdr blocked"}
	case "unknown":
		if strong || (prev == "working" && prompt) {
			return Result{Kind: KindNeedsDecision, Reason: "unknown with question"}
		}
		return Result{Kind: KindUnknownIdle, Reason: "unknown status"}
	case "done":
		if strong || (prev == "working" && prompt && strings.Contains(lastNonEmpty(text), "?")) {
			return Result{Kind: KindNeedsDecision, Reason: "question markers"}
		}
		return Result{Kind: KindSettled, Reason: "herdr done"}
	case "idle":
		if strong {
			return Result{Kind: KindNeedsDecision, Reason: "approval chrome"}
		}
		if prev == "working" && prompt {
			return Result{Kind: KindNeedsDecision, Reason: "idle after work looks like a prompt"}
		}
		if strings.TrimSpace(text) == "" && prev == "working" {
			return Result{Kind: KindNeedsDecision, Reason: "idle after work with empty screen"}
		}
		return Result{Kind: KindSettled, Reason: "idle, no question"}
	default:
		if strong {
			return Result{Kind: KindNeedsDecision, Reason: "approval chrome"}
		}
		return Result{Kind: KindUnknownIdle, Reason: "unrecognized status"}
	}
}

func combinedText(in Input) string {
	var b strings.Builder
	if in.Title != "" {
		b.WriteString(in.Title)
		b.WriteByte('\n')
	}
	if in.Detection != "" {
		b.WriteString(in.Detection)
		b.WriteByte('\n')
	}
	if in.Excerpt != "" {
		b.WriteString(in.Excerpt)
	}
	return b.String()
}

func strongQuestion(title, text string) bool {
	// Only the tail. Full transcripts about approvals must not page.
	tail := strings.ToLower(title + "\n" + strings.Join(lastLines(text, 8), "\n"))
	for _, p := range questionPhrases {
		if strings.Contains(tail, p) {
			return true
		}
	}
	return false
}

func lastNonEmptyLooksPrompt(text string) bool {
	line := lastNonEmpty(text)
	if line == "" {
		return false
	}
	line = stripSpinner(line)
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(line)
	return last == '?' || last == ':'
}

func lastNonEmpty(text string) string {
	lines := lastLines(text, 8)
	for i := len(lines) - 1; i >= 0; i-- {
		s := stripSpinner(strings.TrimSpace(lines[i]))
		if s != "" {
			return s
		}
	}
	return ""
}

func lastLines(text string, n int) []string {
	raw := strings.Split(text, "\n")
	if len(raw) <= n {
		return raw
	}
	return raw[len(raw)-n:]
}

func stripSpinner(s string) string {
	s = strings.TrimLeftFunc(s, func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		switch r {
		case '⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏',
			'⣾', '⣽', '⣻', '⢿', '⡿', '⣟', '⣯', '⣷',
			'•', '·', '●', '○', '-', '|', '/', '\\':
			return true
		}
		return false
	})
	return strings.TrimSpace(s)
}

// TruncateExcerpt keeps the tail of a screen read for the queue.
func TruncateExcerpt(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 {
		maxRunes = 2000
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[len(runes)-maxRunes:])
}
