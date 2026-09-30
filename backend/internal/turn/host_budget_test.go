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
// still compiling. It started at six stage operations; the intent, character and
// narration, coordination, progression and frozen input now live in this package, so
// only the operation the application genuinely owns is left: observing a turn.
//
// So the number only falls. Raising it is a decision to be argued for, not a step to take
// to make something compile: if a change needs a fourth method, the right move is usually
// to move another stage into this package rather than to widen the door.
const hostMethodBudget = 1

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
