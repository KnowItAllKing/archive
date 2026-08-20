package archive

import (
	_ "embed"
	"fmt"
)

//go:embed doctor.md
var doctorPrompt string

type DoctorConfig struct {
	Harness string
	Model   string
	Effort  string
}

func DoctorArgs(config DoctorConfig, statusReport string) ([]string, error) {
	prompt := doctorPrompt + "\n# Current store status\n\n" + statusReport + "\n"
	switch config.Harness {
	case "claude":
		model := config.Model
		if model == "" {
			model = "fable"
		}
		return []string{
			"claude",
			"--model", model,
			"--effort", config.Effort,
			"--allowedTools", "Bash(archive:*)",
			prompt,
		}, nil
	case "codex":
		args := []string{"codex"}
		if config.Model != "" {
			args = append(args, "--model", config.Model)
		}
		args = append(args, "-c", "model_reasoning_effort="+config.Effort, prompt)
		return args, nil
	default:
		return nil, fmt.Errorf("unknown doctor harness %q: use claude or codex", config.Harness)
	}
}
