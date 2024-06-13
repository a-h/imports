package imports

import (
	"bytes"
	"fmt"
	"log"
	"path"
	"slices"
	"strings"

	"go/ast"
	goparser "go/parser"
	"go/printer"
	"go/token"

	"golang.org/x/tools/imports"

	"github.com/a-h/templ/generator"
	"github.com/a-h/templ/parser/v2"
)

var fset = token.NewFileSet()

func getImports(name, src string) (imports []*ast.ImportSpec, err error) {
	gofile, err := goparser.ParseFile(fset, name, src, goparser.ImportsOnly)
	if err != nil {
		return imports, fmt.Errorf("failed to parse imports: %w", err)
	}
	return gofile.Imports, nil
}

func updateImports(name, src string) (updated []*ast.ImportSpec, err error) {
	// Apply auto imports.
	updatedGoCode, err := imports.Process(name, []byte(src), nil)
	if err != nil {
		return updated, fmt.Errorf("failed to process go code %q: %w", src, err)
	}
	// Get updated imports.
	gofile, err := goparser.ParseFile(fset, name, updatedGoCode, goparser.ImportsOnly)
	if err != nil {
		return updated, fmt.Errorf("failed to get imports from updated go code: %w", err)
	}
	return gofile.Imports, nil
}

func Process(dir string, src string) (t parser.TemplateFile, err error) {
	t, err = parser.ParseString(src)
	if err != nil {
		return t, err
	}
	fileName := path.Join(dir, "templ.go")

	// The first node always contains existing imports.
	// If there isn't one, create it.
	if len(t.Nodes) == 0 {
		t.Nodes = append(t.Nodes, parser.TemplateFileGoExpression{})
	}
	// If there is one, ensure it is a Go expression.
	if _, ok := t.Nodes[0].(parser.TemplateFileGoExpression); !ok {
		t.Nodes = append([]parser.TemplateFileNode{parser.TemplateFileGoExpression{}}, t.Nodes...)
	}

	// Find all existing imports.
	importsNode := t.Nodes[0].(parser.TemplateFileGoExpression)
	existingImports, err := getImports(fileName, t.Package.Expression.Value+"\n"+importsNode.Expression.Value)
	if err != nil {
		return t, fmt.Errorf("failed to get imports from existing go code at %v: %w", importsNode.Expression.Range, err)
	}

	// Generate code.
	gw := bytes.NewBuffer(nil)
	if _, _, err = generator.Generate(t, gw); err != nil {
		return t, fmt.Errorf("failed to generate go code: %w", err)
	}

	// Find all the imports in the generated Go code.
	updatedImports, err := updateImports(fileName, gw.String())
	if err != nil {
		return t, fmt.Errorf("failed to get imports from generated go code: %w", err)
	}

	// Quit early if there are no imports to add or remove.
	if len(existingImports) == 0 && len(updatedImports) == 0 {
		return t, nil
	}

	// Update the template with the imports.
	// Ensure that there is a Go expression to add the imports to as the first node.
	gofile, err := goparser.ParseFile(fset, fileName, t.Package.Expression.Value+"\n"+importsNode.Expression.Value, goparser.AllErrors)
	if err != nil {
		log.Printf("failed to parse go code: %v", importsNode.Expression.Value)
		return t, fmt.Errorf("failed to parse imports section: %w", err)
	}
	slices.SortFunc(updatedImports, func(a, b *ast.ImportSpec) int {
		return strings.Compare(a.Path.Value, b.Path.Value)
	})
	gofile.Imports = updatedImports
	newImportDecl := &ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: convertSlice(updatedImports),
	}
	gofile.Decls = append([]ast.Decl{newImportDecl}, gofile.Decls...)
	// Write out the Go code with the imports.
	updatedGoCode := new(strings.Builder)
	err = printer.Fprint(updatedGoCode, fset, gofile)
	if err != nil {
		return t, fmt.Errorf("failed to write updated go code: %w", err)
	}
	importsNode.Expression.Value = strings.TrimSpace(strings.SplitN(updatedGoCode.String(), "\n", 2)[1])
	t.Nodes[0] = importsNode

	return t, nil
}

func convertSlice(slice []*ast.ImportSpec) []ast.Spec {
	result := make([]ast.Spec, len(slice))
	for i, v := range slice {
		result[i] = ast.Spec(v)
	}
	return result
}
