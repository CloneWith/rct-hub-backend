// Command verify runs the repository's required local and CI checks.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	gqlast "github.com/vektah/gqlparser/v2/ast"
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
	if err := checkErrorContract(); err != nil {
		fail("error-contract", err)
	}

	checks := []check{
		{name: "graphql-compat", cmd: "go", args: []string{"run", "./tools/graphqlcompat"}},
		{name: "seed-consistency", cmd: "go", args: []string{"test", "./cmd/initdb/", "-run", "TestSeedConsistency", "-count=1"}},
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

type errorContract struct {
	SchemaVersion int      `json:"schemaVersion"`
	Codes         []string `json:"codes"`
}

func checkErrorContract() error {
	content, err := os.ReadFile("contracts/errors-v1.json")
	if err != nil {
		return fmt.Errorf("read error contract: %w", err)
	}
	var contract errorContract
	if err := json.Unmarshal(content, &contract); err != nil {
		return fmt.Errorf("decode error contract: %w", err)
	}
	if contract.SchemaVersion != 1 {
		return fmt.Errorf("error contract schema version = %d, want 1", contract.SchemaVersion)
	}
	if err := checkPublicErrorCodes(contract.Codes); err != nil {
		return err
	}
	fmt.Println("error contract passed")
	return nil
}

func checkPublicErrorCodes(codes []string) error {
	content, err := os.ReadFile("schema.graphql")
	if err != nil {
		return fmt.Errorf("read GraphQL schema: %w", err)
	}
	schema, err := gqlparser.LoadSchema(&gqlast.Source{Name: "schema.graphql", Input: string(content)})
	if err != nil {
		return fmt.Errorf("parse GraphQL schema: %w", err)
	}
	definition := schema.Types["MatchErrorCode"]
	if definition == nil || definition.Kind != gqlast.Enum {
		return fmt.Errorf("GraphQL schema does not define MatchErrorCode")
	}
	public := make(map[string]bool, len(definition.EnumValues))
	for _, value := range definition.EnumValues {
		public[value.Name] = true
	}
	published := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code == "" || published[code] {
			return fmt.Errorf("error contract contains an empty or repeated code")
		}
		published[code] = true
	}
	if !equalStringSet(published, public) {
		return fmt.Errorf("error contract does not match public GraphQL MatchErrorCode")
	}
	return nil
}

func equalStringSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
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
		fixtureGenerator := strings.HasPrefix(normalized, "internal/matchfixture/")
		if strings.Contains(text, "matchengine.Execute(") && normalized != "internal/matchcommand/orchestrator.go" && normalized != "tools/matchlab/lab.go" && !fixtureGenerator {
			return fmt.Errorf("%s calls MatchEngine directly; formal writes must enter through the orchestrator", normalized)
		}
		if !strings.Contains(text, `"rctHubBackend/internal/matchcommand"`) {
			continue
		}
		allowed := strings.HasPrefix(normalized, "internal/graphql/") ||
			strings.HasPrefix(normalized, "internal/persistence/") || fixtureGenerator || normalized == "internal/server/server.go"
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
	for dependency := range strings.FieldsSeq(string(output)) {
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
