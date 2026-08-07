// Command verify runs the repository's required local and CI checks.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type check struct {
	name string
	cmd  string
	args []string
}

func main() {
	if err := checkFormatting(); err != nil {
		fail("format", err)
	}
	if err := checkMatchEngineDependencies(); err != nil {
		fail("matchengine-purity", err)
	}
	if err := checkMatchCommandBoundaries(); err != nil {
		fail("match-command-boundaries", err)
	}

	checks := []check{
		{name: "vet", cmd: "go", args: []string{"vet", "./..."}},
		{name: "test", cmd: "go", args: []string{"test", "./..."}},
		{name: "build", cmd: "go", args: []string{"build", "./..."}},
	}

	for _, item := range checks {
		fmt.Printf("==> %s\n", item.name)
		command := exec.Command(item.cmd, item.args...)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.Stdin = os.Stdin
		if err := command.Run(); err != nil {
			fail(item.name, err)
		}
	}

	fmt.Println("verification passed")
}

func checkMatchCommandBoundaries() error {
	fmt.Println("==> match-command-boundaries")
	files, err := goFiles(".")
	if err != nil {
		return err
	}
	for _, path := range files {
		normalized := filepath.ToSlash(path)
		if strings.HasSuffix(normalized, "_test.go") || normalized == "tools/verify/main.go" {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(source)
		if strings.Contains(text, "matchengine.Execute(") && normalized != "internal/matchcommand/orchestrator.go" && normalized != "tools/matchlab/lab.go" {
			return fmt.Errorf("%s calls MatchEngine directly; formal writes must enter through the orchestrator", normalized)
		}
		if !strings.Contains(text, `"rctHubBackend/internal/matchcommand"`) {
			continue
		}
		allowed := strings.HasPrefix(normalized, "internal/graphql/") ||
			strings.HasPrefix(normalized, "internal/persistence/") || normalized == "internal/server/server.go"
		if !allowed {
			return fmt.Errorf("%s imports matchcommand outside the GraphQL adapter, persistence store, or composition root", normalized)
		}
	}
	return nil
}

func checkMatchEngineDependencies() error {
	fmt.Println("==> matchengine-purity")
	command := exec.Command("go", "list", "-deps", "./internal/matchengine")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}
	forbidden := []string{
		"github.com/gin-gonic/gin",
		"github.com/redis/go-redis",
		"go.mongodb.org/mongo-driver",
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, prefix := range forbidden {
			if dependency == prefix || strings.HasPrefix(dependency, prefix+"/") {
				return fmt.Errorf("forbidden dependency %q", dependency)
			}
		}
	}
	return nil
}

func checkFormatting() error {
	files, err := goFiles(".")
	if err != nil {
		return err
	}

	fmt.Println("==> format")
	var unformatted []string
	for _, path := range files {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		formatted, formatErr := format.Source(source)
		if formatErr != nil {
			return fmt.Errorf("format %s: %w", path, formatErr)
		}
		normalized := bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
		if !bytes.Equal(normalized, formatted) {
			unformatted = append(unformatted, path)
		}
	}

	if len(unformatted) > 0 {
		return fmt.Errorf("these files need gofmt:\n%s", strings.Join(unformatted, "\n"))
	}
	return nil
}

func goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root {
			switch entry.Name() {
			case ".git", ".agents", "bin", "build", "dist", "tmp", "vendor":
				return filepath.SkipDir
			}
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func fail(name string, err error) {
	fmt.Fprintf(os.Stderr, "verification failed during %s: %v\n", name, err)
	os.Exit(1)
}
