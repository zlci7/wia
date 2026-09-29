package context_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestContextPackageExposesOnlyEngineBuildEntryPoint(t *testing.T) {
	t.Helper()

	root := filepath.Join("..", "..", "..", "backend", "internal", "context")
	packages, err := parser.ParseDir(token.NewFileSet(), root, nil, 0)
	if err != nil {
		t.Fatalf("ParseDir returned error: %v", err)
	}

	for _, file := range packages["context"].Files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.TypeSpec:
				if typed.Name.Name == "Builder" || typed.Name.Name == "AgentContext" {
					t.Fatalf("context package exposes legacy %s type", typed.Name.Name)
				}
				if typed.Name.Name == "BuildInput" {
					fields, ok := typed.Type.(*ast.StructType)
					if !ok {
						return true
					}
					for _, field := range fields.Fields.List {
						for _, name := range field.Names {
							if name.Name == "Tools" {
								t.Fatal("BuildInput exposes legacy direct Tools field")
							}
						}
					}
				}
			case *ast.FuncDecl:
				if typed.Name.Name == "NewBuilder" {
					t.Fatal("context package exposes legacy NewBuilder constructor")
				}
				if typed.Recv != nil && typed.Name.Name == "Build" {
					for _, field := range typed.Recv.List {
						if exprName(field.Type) == "Builder" {
							t.Fatal("context package exposes legacy Builder.Build entry point")
						}
					}
				}
			}
			return true
		})
	}
}

func exprName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return exprName(typed.X)
	default:
		return ""
	}
}
