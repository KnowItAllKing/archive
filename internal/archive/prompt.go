package archive

import (
	_ "embed"
)

//go:embed prompt.md
var prompt string

func Prompt() string {
	return prompt
}
