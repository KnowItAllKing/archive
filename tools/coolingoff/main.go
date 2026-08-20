// Command coolingoff enforces the dependency cooling-off policy: every module
// version in the build list must have been published at least minAge ago,
// according to the Go module proxy. Pipe `go list -m -json all` into it.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const minAge = 7 * 24 * time.Hour

type listedModule struct {
	Path    string
	Version string
	Main    bool
	Replace *listedModule
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "coolingoff: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	modules, err := readModules(os.Stdin)
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return errors.New("no modules on stdin; pipe `go list -m -json all` into this command")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	now := time.Now()
	var mutex sync.Mutex
	var violations []string
	var failures []string
	semaphore := make(chan struct{}, 8)
	var group sync.WaitGroup
	for _, module := range modules {
		group.Add(1)
		go func(module listedModule) {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			published, err := publishedAt(client, module.Path, module.Version)
			mutex.Lock()
			defer mutex.Unlock()
			if err != nil {
				failures = append(failures, err.Error())
				return
			}
			if age := now.Sub(published); age < minAge {
				violations = append(violations, fmt.Sprintf(
					"%s@%s published %.1f days ago (minimum %.0f)",
					module.Path, module.Version, age.Hours()/24, minAge.Hours()/24,
				))
			}
		}(module)
	}
	group.Wait()

	sort.Strings(violations)
	sort.Strings(failures)
	for _, line := range violations {
		fmt.Println("TOO NEW  " + line)
	}
	for _, line := range failures {
		fmt.Println("ERROR    " + line)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d modules could not be checked", len(failures))
	}
	if len(violations) > 0 {
		return fmt.Errorf("%d of %d modules violate the %.0f-day cooling-off period", len(violations), len(modules), minAge.Hours()/24)
	}
	fmt.Printf("cooling-off ok: %d modules at least %.0f days old\n", len(modules), minAge.Hours()/24)
	return nil
}

func readModules(input io.Reader) ([]listedModule, error) {
	decoder := json.NewDecoder(input)
	var modules []listedModule
	for {
		var module listedModule
		if err := decoder.Decode(&module); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse go list output: %w", err)
		}
		if module.Main {
			continue
		}
		if module.Replace != nil {
			module = *module.Replace
		}
		// A replacement without a version is a local directory, not a download.
		if module.Version == "" {
			continue
		}
		modules = append(modules, module)
	}
	return modules, nil
}

func publishedAt(client *http.Client, path, version string) (time.Time, error) {
	url := "https://proxy.golang.org/" + escapeModulePath(path) + "/@v/" + escapeModulePath(version) + ".info"
	response, err := client.Get(url)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s@%s: %v", path, version, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s@%s: read response: %v", path, version, err)
	}
	if response.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("%s@%s: proxy returned %s", path, version, response.Status)
	}
	var info struct {
		Time time.Time
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return time.Time{}, fmt.Errorf("%s@%s: parse version info: %v", path, version, err)
	}
	if info.Time.IsZero() {
		return time.Time{}, fmt.Errorf("%s@%s: proxy reports no publication time", path, version)
	}
	return info.Time, nil
}

// The module proxy protocol escapes uppercase letters as '!' + lowercase.
func escapeModulePath(path string) string {
	var out strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			out.WriteByte('!')
			out.WriteRune(r + ('a' - 'A'))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
