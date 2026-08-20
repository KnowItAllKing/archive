package archive

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func (s *Store) git(args ...string) error {
	gitArgs := append([]string{"-c", "core.fsmonitor=false"}, args...)
	command := exec.Command("git", gitArgs...)
	command.Dir = s.Root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return fmt.Errorf("git %s: %w: %s", args[0], err, message)
		}
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

func (s *Store) commit(message string, paths ...string) error {
	addArgs := append([]string{"add", "--"}, paths...)
	if err := s.git(addArgs...); err != nil {
		return err
	}
	commitArgs := s.identityArgs()
	commitArgs = append(commitArgs, "commit", "--quiet", "--only", "--allow-empty", "-m", message, "--")
	commitArgs = append(commitArgs, paths...)
	return s.git(commitArgs...)
}

func (s *Store) commitAll(message string) error {
	if err := s.git("add", "--all"); err != nil {
		return err
	}
	commitArgs := s.identityArgs()
	commitArgs = append(commitArgs, "commit", "--quiet", "--allow-empty", "-m", message)
	return s.git(commitArgs...)
}

func (s *Store) identityArgs() []string {
	args := []string{}
	if !s.hasGitConfig("user.name") {
		args = append(args, "-c", "user.name=archive")
	}
	if !s.hasGitConfig("user.email") {
		args = append(args, "-c", "user.email=archive@localhost")
	}
	return args
}

func (s *Store) gitOutput(args ...string) (string, error) {
	gitArgs := append([]string{"-c", "core.fsmonitor=false"}, args...)
	command := exec.Command("git", gitArgs...)
	command.Dir = s.Root
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := bytes.TrimSpace(stderr.Bytes())
		if len(message) > 0 {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, message)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

func (s *Store) remoteURL() (string, error) {
	names, err := s.gitOutput("remote")
	if err != nil {
		return "", err
	}
	if !contains(strings.Fields(names), "origin") {
		return "", nil
	}
	url, err := s.gitOutput("remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(url), nil
}

func (s *Store) unpushedCount() (int, error) {
	output, err := s.gitOutput("rev-list", "--count", "HEAD", "--not", "--remotes=origin")
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, fmt.Errorf("parse unpushed commit count %q: %w", output, err)
	}
	return count, nil
}

func (s *Store) hasGitConfig(key string) bool {
	command := exec.Command("git", "-c", "core.fsmonitor=false", "config", "--get", key)
	command.Dir = s.Root
	output, err := command.Output()
	return err == nil && len(bytes.TrimSpace(output)) > 0
}
