package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func main() {
	baseline := load("contracts/graphql-v1.graphql")
	current := load("schema.graphql")
	var breaks []string
	for name, oldType := range baseline.Types {
		if oldType.BuiltIn || len(name) >= 2 && name[:2] == "__" {
			continue
		}
		newType := current.Types[name]
		if newType == nil {
			breaks = append(breaks, fmt.Sprintf("removed type %s", name))
			continue
		}
		if oldType.Kind != newType.Kind {
			breaks = append(breaks, fmt.Sprintf("changed type kind %s", name))
			continue
		}
		for _, oldField := range oldType.Fields {
			newField := newType.Fields.ForName(oldField.Name)
			if newField == nil {
				breaks = append(breaks, fmt.Sprintf("removed field %s.%s", name, oldField.Name))
				continue
			}
			if oldField.Type.String() != newField.Type.String() {
				breaks = append(breaks, fmt.Sprintf("changed field type %s.%s from %s to %s", name, oldField.Name, oldField.Type.String(), newField.Type.String()))
			}
			for _, oldArgument := range oldField.Arguments {
				newArgument := newField.Arguments.ForName(oldArgument.Name)
				if newArgument == nil {
					breaks = append(breaks, fmt.Sprintf("removed argument %s.%s(%s)", name, oldField.Name, oldArgument.Name))
				} else if oldArgument.Type.String() != newArgument.Type.String() {
					breaks = append(breaks, fmt.Sprintf("changed argument type %s.%s(%s) from %s to %s", name, oldField.Name, oldArgument.Name, oldArgument.Type.String(), newArgument.Type.String()))
				}
			}
			for _, newArgument := range newField.Arguments {
				if oldField.Arguments.ForName(newArgument.Name) == nil && newArgument.Type.NonNull && newArgument.DefaultValue == nil {
					breaks = append(breaks, fmt.Sprintf("added required argument %s.%s(%s)", name, oldField.Name, newArgument.Name))
				}
			}
		}
		if oldType.Kind == ast.InputObject {
			for _, newField := range newType.Fields {
				if oldType.Fields.ForName(newField.Name) == nil && newField.Type.NonNull && newField.DefaultValue == nil {
					breaks = append(breaks, fmt.Sprintf("added required input field %s.%s", name, newField.Name))
				}
			}
		}
		if oldType.Kind == ast.Enum {
			for _, oldValue := range oldType.EnumValues {
				if newType.EnumValues.ForName(oldValue.Name) == nil {
					breaks = append(breaks, fmt.Sprintf("removed enum value %s.%s", name, oldValue.Name))
				}
			}
		}
	}
	sort.Strings(breaks)
	if len(breaks) > 0 {
		for _, item := range breaks {
			_, _ = fmt.Fprintln(os.Stderr, item)
		}
		os.Exit(1)
	}
	fmt.Println("GraphQL v1 compatibility passed")
}

func load(path string) *ast.Schema {
	content, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: path, Input: string(content)})
	if err != nil {
		fatal(err)
	}
	return schema
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
