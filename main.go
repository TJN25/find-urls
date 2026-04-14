package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var githubRe = regexp.MustCompile(`https?://github\.com/[^\s"'<>)]*`)

type Config struct {
	Output string
	Roots  []string
}

func main() {
	cfg, err := parseArgs(os.Args[1:])

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: find-github-links -o output.tsv ROOT [ROOT ...]")
		os.Exit(2)
	}

	seen, err := loadSeenSlugs(cfg.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load output file: %v\n", err)
		os.Exit(1)
	}

	out, err := openOutput(cfg.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open output file: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	for _, root := range cfg.Roots {
		if err := processRoot(root, out, seen); err != nil {
			fmt.Fprintf(os.Stderr, "process root %s: %v\n", root, err)

		}
	}
}

func parseArgs(args []string) (Config, error) {
	var cfg Config

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o", "--output":
			i++
			if i >= len(args) {
				return cfg, errors.New("missing value after -o/--output")
			}
			cfg.Output = args[i]
		default:
			cfg.Roots = append(cfg.Roots, args[i])
		}
	}
	if cfg.Output == "" {
		cfg.Output = "github_links_from_scripts.tsv"
	}
	if len(cfg.Roots) == 0 {
		return cfg, errors.New("at least one root directory is required")
	}
	return cfg, nil
}

func loadSeenSlugs(path string) (map[string]struct{}, error) {
	seen := make(map[string]struct{})

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return seen, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if line == "slug\tlink" {
				continue
			}
		}
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) > 0 && parts[0] != "" {
			seen[parts[0]] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return seen, nil
}

func openOutput(path string) (*os.File, error) {
	_, err := os.Stat(path)
	needsHeader := os.IsNotExist(err)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	if needsHeader {
		if _, err := fmt.Fprintln(f, "slug\tlink"); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func processRoot(root string, out *os.File, seen map[string]struct{}) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		slug := entry.Name()
		if _, ok := seen[slug]; ok {
			continue
		}

		fmt.Printf("checking %s\n", slug)
		slugDir := filepath.Join(root, slug)
		link, err := findLinks(slugDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "walk %s: %v\n", slugDir, err)
			continue
		}
		if link == "" {
			continue
		}

		if _, err := fmt.Fprintf(out, "%s\t%s\n", slug, link); err != nil {
			return err
		}
		fmt.Printf("found link %s for %s\n", link, slug)
		seen[slug] = struct{}{}

	}
	return nil
}

func findLinks(slugDir string) (string, error) {
	var found string
	err := filepath.WalkDir(slugDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".sh" {
			return nil
		}

		lowerPath := strings.ToLower(path)
		if !strings.Contains(lowerPath, "install") && !strings.Contains(lowerPath, "run") {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			if os.IsPermission(statErr) {
				return nil
			}
			return nil
		}
		if info.Mode()&0o111 == 0 {
			return nil
		}

		link, scanErr := extractLink(path)
		if scanErr != nil {
			if os.IsPermission(scanErr) {
				return nil
			}
			return nil
		}
		if link == "" {
			return nil
		}

		found = link
		return errors.New("found")
	})

	if err != nil && err.Error() == "found" {
		return found, nil
	}
	if err != nil {
		return "", err
	}
	return found, nil
}

func extractLink(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		match := githubRe.FindString(line)
		if match != "" {
			return match, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", nil
}
