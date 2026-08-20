package archive

import (
	"strings"
	"testing"
)

func TestEnterArgsClaudeDefaults(t *testing.T) {
	args, err := EnterArgs(EnterConfig{Harness: "claude", Effort: "high"}, "STATUS_REPORT")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args[:len(args)-1], " ")
	if joined != "claude --model fable --effort high --allowedTools Bash(archive:*)" {
		t.Fatalf("claude args = %q", joined)
	}
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "archivist") || !strings.Contains(prompt, "STATUS_REPORT") {
		t.Fatalf("prompt missing content: %q", prompt[:80])
	}
}

func TestEnterArgsCodex(t *testing.T) {
	args, err := EnterArgs(EnterConfig{Harness: "codex", Model: "gpt-5.2", Effort: "medium"}, "STATUS")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args[:len(args)-1], " ")
	if joined != "codex --model gpt-5.2 -c model_reasoning_effort=medium" {
		t.Fatalf("codex args = %q", joined)
	}

	args, err = EnterArgs(EnterConfig{Harness: "codex", Effort: "high"}, "STATUS")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "--model") {
		t.Fatalf("codex without model should use its config default: %v", args[:len(args)-1])
	}
}

func TestEnterArgsRejectsUnknownHarness(t *testing.T) {
	if _, err := EnterArgs(EnterConfig{Harness: "gemini"}, ""); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Fatalf("error = %v", err)
	}
}
