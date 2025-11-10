package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
)

type Config struct {
	PackageName     string
	RepoURL         string
	RepoMode        string // "test", "remote", "local"
	TreeOutput      bool
	FilterSubstring string
}

type Graph map[string][]string

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: pkgdepvis <config.csv>")
		os.Exit(1)
	}

	configPath := os.Args[1]
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("package_name: %s\n", cfg.PackageName)
	fmt.Printf("repo_url: %s\n", cfg.RepoURL)
	fmt.Printf("repo_mode: %s\n", cfg.RepoMode)
	fmt.Printf("tree_output: %t\n", cfg.TreeOutput)
	fmt.Printf("filter_substring: %s\n", cfg.FilterSubstring)

	if cfg.RepoMode == "remote" || cfg.RepoMode == "local" {
		deps, err := fetchDirectDependencies(cfg.RepoURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching Cargo.toml dependencies: %v\n", err)
			os.Exit(1)
		}
		for _, dep := range deps {
			fmt.Println(dep)
		}
		return
	}

	if cfg.RepoMode == "test" {
		depsGraph, err := loadTestGraph(cfg.RepoURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading test graph: %v\n", err)
			os.Exit(1)
		}

		resultGraph, err := buildDependencyGraph(depsGraph, cfg.PackageName, cfg.FilterSubstring)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error building dependency graph: %v\n", err)
			os.Exit(1)
		}

		if cfg.TreeOutput {
			printDependencyTree(resultGraph, cfg.PackageName)
		} else {
			fmt.Println("Visualization (Mermaid) is not available in stages 1-3 version.")
		}
	} else {
		fmt.Fprintln(os.Stderr, "Unsupported repo_mode. Use 'test', 'remote', or 'local'.")
		os.Exit(1)
	}
}

func fetchDirectDependencies(repoURL string) ([]string, error) {
	var content []byte
	var err error

	if strings.HasPrefix(repoURL, "http://") || strings.HasPrefix(repoURL, "https://") {
		resp, err := http.Get(repoURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch Cargo.toml: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		content, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
	} else {
		content, err = ioutil.ReadFile(repoURL)
		if err != nil {
			return nil, fmt.Errorf("failed to read local Cargo.toml: %w", err)
		}
	}

	return parseCargoDependencies(string(content)), nil
}

func parseCargoDependencies(content string) []string {
	lines := strings.Split(content, "\n")
	var deps []string
	inDeps := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if trimmed == "[dependencies]" {
			inDeps = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inDeps = false
			continue
		}

		if inDeps && strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			name := strings.TrimSpace(parts[0])
			if isValidCargoName(name) {
				deps = append(deps, name)
			}
		}
	}

	return deps
}

func isValidCargoName(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func printDependencyTree(graph Graph, root string) {
	fmt.Println(root)
	children := graph[root]
	for i, child := range children {
		isLast := (i == len(children)-1)
		inPath := map[string]bool{root: true}
		printTreeRecursive(graph, child, "", isLast, inPath)
	}
}

func printTreeRecursive(graph Graph, node string, prefix string, isLast bool, inPath map[string]bool) {
	if inPath[node] {
		branch := "└── "
		if !isLast {
			branch = "├── "
		}
		fmt.Printf("%s%s%s (*)\n", prefix, branch, node)
		return
	}

	branch := "├── "
	if isLast {
		branch = "└── "
	}
	fmt.Printf("%s%s%s\n", prefix, branch, node)

	children := graph[node]
	if len(children) == 0 {
		return
	}

	newInPath := make(map[string]bool)
	for k, v := range inPath {
		newInPath[k] = v
	}
	newInPath[node] = true

	newPrefix := prefix
	if isLast {
		newPrefix += "    "
	} else {
		newPrefix += "│   "
	}

	for i, child := range children {
		isLastChild := (i == len(children)-1)
		printTreeRecursive(graph, child, newPrefix, isLastChild, newInPath)
	}
}

func loadTestGraph(path string) (Graph, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	graph := make(Graph)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format at line %d", lineNum)
		}

		node := strings.TrimSpace(parts[0])
		if !isValidPackageName(node) {
			return nil, fmt.Errorf("invalid package name at line %d: %q", lineNum, node)
		}

		depsStr := strings.TrimSpace(parts[1])
		var deps []string
		if depsStr != "" {
			for _, d := range strings.Fields(depsStr) {
				if !isValidPackageName(d) {
					return nil, fmt.Errorf("invalid dependency name at line %d: %q", lineNum, d)
				}
				deps = append(deps, d)
			}
		}
		graph[node] = deps
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return graph, nil
}

func isValidPackageName(s string) bool {
	matched, _ := regexp.MatchString(`^[A-Z]+$`, s)
	return matched
}

func buildDependencyGraph(fullGraph Graph, startNode, filter string) (Graph, error) {
	if _, exists := fullGraph[startNode]; !exists {
		return nil, fmt.Errorf("root package %q not found in graph", startNode)
	}

	result := make(Graph)
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var dfs func(node string) error
	dfs = func(node string) error {
		if inStack[node] {
			return fmt.Errorf("cyclic dependency detected: %q is part of a cycle", node)
		}
		if visited[node] {
			return nil
		}

		if node != startNode && filter != "" && strings.Contains(node, filter) {
			visited[node] = true
			return nil
		}

		visited[node] = true
		inStack[node] = true
		result[node] = []string{}

		for _, dep := range fullGraph[node] {
			if filter != "" && strings.Contains(dep, filter) {
				continue
			}
			result[node] = append(result[node], dep)
			if err := dfs(dep); err != nil {
				return err
			}
		}

		inStack[node] = false
		return nil
	}

	err := dfs(startNode)
	return result, err
}

func loadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	if len(records) != 5 {
		return nil, fmt.Errorf("expected 5 configuration entries, got %d", len(records))
	}

	expectedKeys := []string{"package_name", "repo_url", "repo_mode", "tree_output", "filter_substring"}
	values := make([]string, 5)

	for i, record := range records {
		if len(record) != 2 {
			return nil, fmt.Errorf("invalid format in line %d: expected 2 fields, got %d", i+1, len(record))
		}
		key := strings.TrimSpace(record[0])
		value := strings.TrimSpace(record[1])

		if key != expectedKeys[i] {
			return nil, fmt.Errorf("unexpected key in line %d: got %q, expected %q", i+1, key, expectedKeys[i])
		}

		if value == "" && key != "filter_substring" {
			return nil, fmt.Errorf("empty value for %q", key)
		}
		values[i] = value
	}

	var treeOutput bool
	switch strings.ToLower(values[3]) {
	case "true", "1", "yes", "on":
		treeOutput = true
	case "false", "0", "no", "off":
		treeOutput = false
	default:
		return nil, fmt.Errorf("invalid boolean value for tree_output: %q", values[3])
	}

	return &Config{
		PackageName:     values[0],
		RepoURL:         values[1],
		RepoMode:        values[2],
		TreeOutput:      treeOutput,
		FilterSubstring: values[4],
	}, nil
}

// go run main.go config.csv