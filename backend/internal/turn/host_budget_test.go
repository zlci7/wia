package turn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// hostMethodBudget is how many operations a turn may still ask the application for.
//
// The budget exists because Host is the one place where this extraction can fail while
// still compiling. The pipeline keeps its own order and its own helpers — notePlayerAction
// must run between the character stage and coordination, which is why the stages cannot
// be collapsed into one opaque call — so a handful of stage operations have to be asked
// for until the subsystems behind them move. What must not happen is that list growing
// to describe the application: fifteen methods named after App's internals would be
// storyapp.App with an interface in front of it, and the boundary would be gone.
//
// So the number is fixed. Raising it is a decision to be argued for, not a step to take
// to make something compile: if a change needs an eighth method, the right move is
// usually to move another stage into this package rather than to widen the door.
const hostMethodBudget = 7

// TestHostStaysSmall fails when the turn service starts asking the application for more
// than the budget allows.
func TestHostStaysSmall(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service.go", nil, 0)
	if err != nil {
		t.Fatalf("parse service.go: %v", err)
	}
	count := 0
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "Host" {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatalf("Host is no longer an interface")
			}
			for _, method := range iface.Methods.List {
				if len(method.Names) == 0 {
					continue // an embedded interface, which would be a wider door
				}
				count++
				names = append(names, method.Names[0].Name)
			}
		}
	}
	if count > hostMethodBudget {
		t.Errorf("Host has %d methods (%v), above the budget of %d: a turn should own its "+
			"stages, so move the stage here instead of asking the application for more",
			count, names, hostMethodBudget)
	}
	t.Logf("Host: %d of %d methods: %v", count, hostMethodBudget, names)
}
