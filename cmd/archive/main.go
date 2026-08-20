package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	archive "github.com/kai/archive/internal/archive"
)

const version = "1.0.4"

const usage = `archive stores distilled knowledge in local Markdown files.

Usage:
  archive init [--remote URL]
  archive add --title TITLE --category CATEGORY --tags TAGS [--source SOURCE] [--file FILE] [--raw FILE]
  archive update [flags] ID
  archive search [--category CATEGORY] [-n LIMIT] [--lexical | --semantic] [--json] QUERY
  archive show [--json] ID
  archive list [--category CATEGORY] [--tag TAG] [--json]
  archive categories [--json]
  archive status [--json]
  archive push
  archive migrate
  archive reindex
  archive prompt [--json]
  archive doctor [claude|codex] [--model NAME] [--effort LEVEL]
  archive version

ARCHIVE_STORE selects the store. It defaults to ~/archive-store.
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "archive: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}

	store, err := archive.DefaultStore()
	if err != nil {
		return err
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "init":
		return runInit(store, args[1:], stdout)
	case "status":
		return runStatus(store, args[1:], stdout)
	case "push":
		if len(args) != 1 {
			return errors.New("usage: archive push")
		}
		return store.Push(stdout)
	case "doctor":
		return runDoctor(store, args[1:])
	case "version":
		if len(args) != 1 {
			return errors.New("usage: archive version")
		}
		_, err := fmt.Fprintf(stdout, "archive %s\n", version)
		return err
	case "add":
		return runAdd(store, args[1:], stdin, stdout)
	case "update":
		return runUpdate(store, args[1:], stdin, stdout)
	case "search":
		return runSearch(store, args[1:], stdout)
	case "show":
		return runShow(store, args[1:], stdout)
	case "list":
		return runList(store, args[1:], stdout)
	case "categories":
		return runCategories(store, args[1:], stdout)
	case "migrate":
		if len(args) != 1 {
			return errors.New("usage: archive migrate")
		}
		return store.Migrate(stdout)
	case "reindex":
		if len(args) != 1 {
			return errors.New("usage: archive reindex")
		}
		count, err := store.Reindex()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "indexed %d entries\n", count)
		return nil
	case "prompt":
		return runPrompt(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q; run archive help", args[0])
	}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func runInit(store *archive.Store, args []string, stdout io.Writer) error {
	fs := newFlagSet("init")
	remote := fs.String("remote", "", "add this URL as the origin remote")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("init flags: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: archive init [--remote URL]")
	}
	return store.Init(stdout, *remote)
}

func runStatus(store *archive.Store, args []string, stdout io.Writer) error {
	fs := newFlagSet("status")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("status flags: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: archive status [--json]")
	}
	status, err := store.Status()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, status)
	}
	fmt.Fprintf(stdout, "store: %s\nformat: %d\nentries: %d\n", status.Store, status.Format, status.Entries)
	names := make([]string, 0, len(status.Categories))
	for name := range status.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(stdout, "  %s: %d\n", name, status.Categories[name])
	}
	if status.Embeddings == "off" {
		fmt.Fprintln(stdout, "embeddings: off")
	} else {
		fmt.Fprintf(stdout, "embeddings: %s (%d/%d embedded)\n", status.Embeddings, status.Embedded, status.Entries)
	}
	if status.Remote == "" {
		fmt.Fprintln(stdout, "remote: none")
	} else {
		fmt.Fprintf(stdout, "remote: %s (%d unpushed commits)\n", status.Remote, status.Unpushed)
	}
	if len(status.Dirty) == 0 {
		fmt.Fprintln(stdout, "dirty: none")
	} else {
		fmt.Fprintf(stdout, "dirty: %d uncommitted changes\n", len(status.Dirty))
		for _, line := range status.Dirty {
			fmt.Fprintf(stdout, "  %s\n", line)
		}
	}
	return nil
}

func runAdd(store *archive.Store, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := newFlagSet("add")
	title := fs.String("title", "", "entry title")
	category := fs.String("category", "", "entry category")
	tags := fs.String("tags", "", "comma-separated tags")
	source := fs.String("source", "", "source URL, path, or session reference")
	file := fs.String("file", "", "read distilled body from file")
	raw := fs.String("raw", "", "stash an ephemeral raw source")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("add flags: %w", err)
	}
	if fs.NArg() != 0 || *title == "" || *category == "" {
		return errors.New("usage: archive add --title TITLE --category CATEGORY --tags TAGS [--source SOURCE] [--file FILE] [--raw FILE]")
	}
	if *file == "" && stdinIsTerminal(stdin) {
		return errors.New("no body provided: pipe the distilled body on stdin or pass --file")
	}
	body, err := readBody(*file, stdin)
	if err != nil {
		return err
	}
	entry, err := store.Add(archive.AddInput{
		Title: *title, Category: *category, Tags: archive.ParseTags(*tags),
		Source: *source, Body: body, RawFile: *raw,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, entry)
	}
	_, err = fmt.Fprintln(stdout, entry.ID)
	return err
}

func runUpdate(store *archive.Store, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := newFlagSet("update")
	title := fs.String("title", "", "replace title")
	category := fs.String("category", "", "replace category")
	tags := fs.String("tags", "", "replace comma-separated tags")
	source := fs.String("source", "", "replace source")
	file := fs.String("file", "", "read replacement body from file")
	keepBody := fs.Bool("keep-body", false, "keep the existing body and only change fields")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("update flags: %w", err)
	}
	if fs.NArg() != 1 {
		return errors.New("usage: archive update [--title TITLE] [--category CATEGORY] [--tags TAGS] [--source SOURCE] [--file FILE | --keep-body] ID")
	}
	body := ""
	if !*keepBody {
		if *file == "" && stdinIsTerminal(stdin) {
			return errors.New("no body provided: pipe the new distillate, use --file, or pass --keep-body")
		}
		var err error
		body, err = readBody(*file, stdin)
		if err != nil {
			return err
		}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	entry, err := store.Update(fs.Arg(0), archive.UpdateInput{
		Body: body, KeepBody: *keepBody, Title: *title, SetTitle: set["title"],
		Category: *category, SetCategory: set["category"],
		Tags: archive.ParseTags(*tags), SetTags: set["tags"],
		Source: *source, SetSource: set["source"],
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, entry)
	}
	_, err = fmt.Fprintln(stdout, entry.ID)
	return err
}

func runSearch(store *archive.Store, args []string, stdout io.Writer) error {
	fs := newFlagSet("search")
	limit := fs.Int("n", 10, "maximum results")
	category := fs.String("category", "", "category filter")
	lexical := fs.Bool("lexical", false, "lexical FTS search only, skip embeddings")
	semantic := fs.Bool("semantic", false, "semantic vector search only")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("search flags: %w", err)
	}
	if fs.NArg() == 0 || (*lexical && *semantic) {
		return errors.New("usage: archive search [--category CATEGORY] [-n LIMIT] [--lexical | --semantic] [--json] QUERY")
	}
	mode := archive.ModeAuto
	if *lexical {
		mode = archive.ModeLexical
	}
	if *semantic {
		mode = archive.ModeSemantic
	}
	results, err := store.Search(strings.Join(fs.Args(), " "), *category, *limit, mode)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, results)
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "%d. %s [%s] score=%.6f\n   %s\n   tags: %s\n", result.Rank, result.Title, result.Category, result.Score, result.Snippet, strings.Join(result.Tags, ", "))
	}
	return nil
}

func runShow(store *archive.Store, args []string, stdout io.Writer) error {
	fs := newFlagSet("show")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("show flags: %w", err)
	}
	if fs.NArg() != 1 {
		return errors.New("usage: archive show [--json] ID")
	}
	entry, raw, err := store.Show(fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, entry)
	}
	_, err = stdout.Write(raw)
	return err
}

func runList(store *archive.Store, args []string, stdout io.Writer) error {
	fs := newFlagSet("list")
	category := fs.String("category", "", "category filter")
	tag := fs.String("tag", "", "tag filter")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("list flags: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: archive list [--category CATEGORY] [--tag TAG] [--json]")
	}
	entries, err := store.List(*category, *tag)
	if err != nil {
		return err
	}
	if *jsonOutput {
		summaries := make([]archive.EntrySummary, len(entries))
		for i, entry := range entries {
			summaries[i] = entry.Summary()
		}
		return writeJSON(stdout, summaries)
	}
	for _, entry := range entries {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", entry.ID, entry.Category, entry.Title, strings.Join(entry.Tags, ","))
	}
	return nil
}

func runCategories(store *archive.Store, args []string, stdout io.Writer) error {
	fs := newFlagSet("categories")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("categories flags: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: archive categories [--json]")
	}
	categories, err := store.Categories()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, categories)
	}
	for _, category := range categories {
		fmt.Fprintf(stdout, "%s\t%s\n", category.Name, category.Description)
	}
	return nil
}

func runPrompt(args []string, stdout io.Writer) error {
	fs := newFlagSet("prompt")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("prompt flags: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: archive prompt [--json]")
	}
	if *jsonOutput {
		return writeJSON(stdout, map[string]string{"prompt": archive.Prompt()})
	}
	_, err := io.WriteString(stdout, archive.Prompt())
	return err
}

func runDoctor(store *archive.Store, args []string) error {
	harness := "claude"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		harness = args[0]
		args = args[1:]
	}
	fs := newFlagSet("doctor")
	model := fs.String("model", "", "model for the doctor session")
	effort := fs.String("effort", "high", "reasoning effort for the doctor session")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("doctor flags: %w", err)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: archive doctor [claude|codex] [--model NAME] [--effort LEVEL]")
	}
	statusReport := ""
	status, err := store.Status()
	if err != nil {
		statusReport = "`archive status` failed: " + err.Error() + "\nHelp the user fix this first."
	} else {
		var rendered strings.Builder
		if err := writeJSON(&rendered, status); err != nil {
			return err
		}
		statusReport = "```json\n" + rendered.String() + "```"
	}
	doctorArgs, err := archive.DoctorArgs(archive.DoctorConfig{
		Harness: harness, Model: *model, Effort: *effort,
	}, statusReport)
	if err != nil {
		return err
	}
	binary, err := exec.LookPath(doctorArgs[0])
	if err != nil {
		return fmt.Errorf("%s CLI not found on PATH", doctorArgs[0])
	}
	return syscall.Exec(binary, doctorArgs, os.Environ())
}

func stdinIsTerminal(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func readBody(filename string, stdin io.Reader) (string, error) {
	var data []byte
	var err error
	if filename != "" {
		data, err = os.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("read body file %q: %w", filename, err)
		}
	} else {
		data, err = io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
	}
	body := strings.TrimRight(string(data), "\n")
	if body == "" {
		return "", nil
	}
	return body + "\n", nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
