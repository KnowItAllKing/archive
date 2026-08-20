package archive

import (
	"strings"
	"testing"
)

func TestDoctorArgsClaudeDefaults(t *testing.T) {
	args, err := DoctorArgs(DoctorConfig{Harness: "claude", Effort: "high"}, "STATUS_REPORT")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args[:len(args)-1], " ")
	if joined != "claude --model fable --effort high --allowedTools Bash(archive:*)" {
		t.Fatalf("claude args = %q", joined)
	}
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "archive doctor") || !strings.Contains(prompt, "STATUS_REPORT") {
		t.Fatalf("prompt missing content: %q", prompt[:80])
	}
}

func TestDoctorArgsCodex(t *testing.T) {
	args, err := DoctorArgs(DoctorConfig{Harness: "codex", Model: "gpt-5.2", Effort: "medium"}, "STATUS")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args[:len(args)-1], " ")
	if joined != "codex --model gpt-5.2 -c model_reasoning_effort=medium" {
		t.Fatalf("codex args = %q", joined)
	}

	args, err = DoctorArgs(DoctorConfig{Harness: "codex", Effort: "high"}, "STATUS")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "--model") {
		t.Fatalf("codex without model should use its config default: %v", args[:len(args)-1])
	}
}

func TestDoctorArgsRejectsUnknownHarness(t *testing.T) {
	if _, err := DoctorArgs(DoctorConfig{Harness: "gemini"}, ""); err == nil || !strings.Contains(err.Error(), "unknown doctor harness") {
		t.Fatalf("error = %v", err)
	}
}
