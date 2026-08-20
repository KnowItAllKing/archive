package archive

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

var (
	validID       = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Nd}]+(?:-[\p{Ll}\p{Lo}\p{Nd}]+)*$`)
	validCategory = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Nd}]+(?:-[\p{Ll}\p{Lo}\p{Nd}]+)*$`)
	validTag      = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Nd}]+(?:-[\p{Ll}\p{Lo}\p{Nd}]+)*$`)
)

type Entry struct {
	ID       string   `yaml:"id" json:"id"`
	Title    string   `yaml:"title" json:"title"`
	Category string   `yaml:"category" json:"category"`
	Tags     []string `yaml:"tags" json:"tags"`
	Created  string   `yaml:"created" json:"created"`
	Updated  string   `yaml:"updated" json:"updated"`
	Review   string   `yaml:"review,omitempty" json:"review,omitempty"`
	Source   string   `yaml:"source,omitempty" json:"source,omitempty"`
	Raw      string   `yaml:"raw,omitempty" json:"raw,omitempty"`
	Body     string   `yaml:"-" json:"body"`
}

type EntrySummary struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	Created  string   `json:"created"`
	Updated  string   `json:"updated"`
	Review   string   `json:"review,omitempty"`
	Source   string   `json:"source,omitempty"`
	Raw      string   `json:"raw,omitempty"`
}

func (entry Entry) Summary() EntrySummary {
	return EntrySummary{
		ID: entry.ID, Title: entry.Title, Category: entry.Category, Tags: entry.Tags,
		Created: entry.Created, Updated: entry.Updated, Review: entry.Review,
		Source: entry.Source, Raw: entry.Raw,
	}
}

func ParseTags(input string) []string {
	if strings.TrimSpace(input) == "" {
		return []string{}
	}
	seen := map[string]bool{}
	var tags []string
	for _, part := range strings.Split(input, ",") {
		tag := strings.ToLower(strings.TrimSpace(part))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

func parseEntry(data []byte) (Entry, error) {
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return Entry{}, fmt.Errorf("missing opening YAML frontmatter delimiter")
	}
	rest := data[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return Entry{}, fmt.Errorf("missing closing YAML frontmatter delimiter")
	}
	var entry Entry
	if err := yaml.Unmarshal(rest[:end], &entry); err != nil {
		return Entry{}, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	body := rest[end+5:]
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	entry.Body = string(body)
	if entry.Tags == nil {
		entry.Tags = []string{}
	}
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func marshalEntry(entry Entry) ([]byte, error) {
	if err := validateEntry(entry); err != nil {
		return nil, err
	}
	frontmatter, err := yaml.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal YAML frontmatter: %w", err)
	}
	body := strings.TrimRight(entry.Body, "\n")
	return []byte("---\n" + string(frontmatter) + "---\n\n" + body + "\n"), nil
}

func validateEntry(entry Entry) error {
	if !validID.MatchString(entry.ID) {
		return fmt.Errorf("invalid entry id %q: use lowercase letters, numbers, and hyphens", entry.ID)
	}
	if strings.TrimSpace(entry.Title) == "" {
		return fmt.Errorf("entry %q has an empty title", entry.ID)
	}
	if entry.Category != "inbox" && !validCategory.MatchString(entry.Category) {
		return fmt.Errorf("entry %q has invalid category %q", entry.ID, entry.Category)
	}
	for _, tag := range entry.Tags {
		if !validTag.MatchString(tag) {
			return fmt.Errorf("entry %q has invalid tag %q: use lowercase letters, numbers, and hyphens", entry.ID, tag)
		}
	}
	dates := []struct {
		name  string
		value string
	}{
		{name: "created", value: entry.Created},
		{name: "updated", value: entry.Updated},
	}
	for _, date := range dates {
		if _, err := time.Parse("2006-01-02", date.value); err != nil {
			return fmt.Errorf("entry %q has invalid %s date %q: use YYYY-MM-DD", entry.ID, date.name, date.value)
		}
	}
	if entry.Review != "" {
		if _, err := time.Parse("2006-01-02", entry.Review); err != nil {
			return fmt.Errorf("entry %q has invalid review date %q: use YYYY-MM-DD", entry.ID, entry.Review)
		}
	}
	if entry.Raw != "" {
		expected := filepath.ToSlash(filepath.Join("raw", entry.ID+".md"))
		if entry.Raw != expected {
			return fmt.Errorf("entry %q has invalid raw path %q: expected %q", entry.ID, entry.Raw, expected)
		}
	}
	return nil
}

func normalizeBody(body string) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	return body + "\n"
}

func slugify(input string) string {
	var out strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(input) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
			lastHyphen = false
		} else if out.Len() > 0 && !lastHyphen {
			out.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func normalizeTags(tags []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(tags))
	for _, input := range tags {
		tag := strings.ToLower(strings.TrimSpace(input))
		if !validTag.MatchString(tag) {
			return nil, fmt.Errorf("invalid tag %q: use lowercase letters, numbers, and hyphens", input)
		}
		if !seen[tag] {
			seen[tag] = true
			result = append(result, tag)
		}
	}
	sort.Strings(result)
	return result, nil
}
