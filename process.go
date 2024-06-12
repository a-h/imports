package imports

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"go/ast"
	goparser "go/parser"
	"go/printer"
	"go/token"

	"github.com/a-h/templ/parser/v2"
	"golang.org/x/tools/imports"
)

var fset = token.NewFileSet()

func applyPrefix(pkg parser.Package, existingImportsMap map[string]*ast.ImportSpec, src string) string {
	var sb strings.Builder
	// package xxx
	sb.WriteString(pkg.Expression.Value)
	sb.WriteString("\n")
	// import "fmt"
	for _, imp := range existingImportsMap {
		sb.WriteString("import ")
		sb.WriteString(imp.Path.Value)
		sb.WriteString("\n")
	}
	// fmt.Println("Hello, world!")
	sb.WriteString(src)
	return sb.String()
}

func getImports(pkg parser.Package, existingImportsMap map[string]*ast.ImportSpec, name, src string) (err error) {
	gofile, err := goparser.ParseFile(fset, name, applyPrefix(pkg, existingImportsMap, src), goparser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("failed to parse imports: %w", err)
	}
	addImportsToMap(gofile.Imports, existingImportsMap)
	return nil
}

func applyAutoImports(pkg parser.Package, existingImportsMap map[string]*ast.ImportSpec, name, src string) (string, error) {
	updated, err := imports.Process(name, []byte(applyPrefix(pkg, existingImportsMap, src)), nil)
	if err != nil {
		return "", fmt.Errorf("failed to process go code %q: %w", src, err)
	}
	return string(updated), nil
}

func getImportSpecName(imp *ast.ImportSpec) string {
	if imp.Name == nil {
		return imp.Path.Value
	}
	return imp.Name.Name + " " + imp.Path.Value
}

func addImportsToMap(imports []*ast.ImportSpec, existingImportsMap map[string]*ast.ImportSpec) {
	if existingImportsMap == nil {
		existingImportsMap = make(map[string]*ast.ImportSpec)
	}
	for _, imp := range imports {
		existingImportsMap[getImportSpecName(imp)] = imp
	}
}

func updateImports(pkg parser.Package, existingImportsMap map[string]*ast.ImportSpec, name, src string) error {
	// Apply auto imports.
	updatedGoCode, err := applyAutoImports(pkg, existingImportsMap, name, src)
	if err != nil {
		return fmt.Errorf("failed to apply imports to expression: %w", err)
	}
	// Get updated imports.
	gofile, err := goparser.ParseFile(fset, name, updatedGoCode, goparser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("failed to get imports from updated go code: %w", err)
	}
	addImportsToMap(gofile.Imports, existingImportsMap)
	return nil
}

func getSourceCodeForNode(n parser.Node) (src string, ok bool) {
	switch n := n.(type) {
	case parser.Element:
		var sb strings.Builder
		for i, attr := range n.Attributes {
			switch attr := attr.(type) {
			case parser.ExpressionAttribute:
				sb.WriteString(fmt.Sprintf("var var%d = ", i))
				sb.WriteString(attr.Expression.Value)
				sb.WriteString("\n")
			case parser.BoolExpressionAttribute:
				sb.WriteString(fmt.Sprintf("var var%d = ", i))
				sb.WriteString(attr.Expression.Value)
				sb.WriteString("\n")
			}
		}
		if sb.Len() == 0 {
			return "", false
		}
		return sb.String(), true
	case parser.GoCode:
		return n.Expression.Value, true
	case parser.StringExpression:
		return "var x = " + n.Expression.Value, true
	default:
		// Not supported.
		return "", false
	}
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
	allImports := make(map[string]*ast.ImportSpec)
	err = getImports(t.Package, nil, fileName, importsNode.Expression.Value)

	// Find all the imports in the Go code.
	// There may be Go code at the TemplateFileLevel.
	for _, n := range t.Nodes {
		header, ok := n.(parser.TemplateFileGoExpression)
		if !ok {
			continue
		}
		err = updateImports(t.Package, allImports, fileName, header.Expression.Value)
		if err != nil {
			return t, fmt.Errorf("failed to get imports from go code at %v: %w", header.Expression.Range, err)
		}
	}

	// Do the same for the interior nodes.
	var perr error
	walkTemplate(t, func(n parser.Node) bool {
		src, ok := getSourceCodeForNode(n)
		if !ok {
			// Skip this node.
			return true
		}

		err = updateImports(t.Package, allImports, fileName, src)
		if err != nil {
			perr = fmt.Errorf("failed to get imports from go code: %w", err)
			return false
		}

		// Continue.
		return true
	})
	if perr != nil {
		return t, perr
	}

	// If there are no imports to process, quit early.
	if len(allImports) == 0 {
		return t, nil
	}

	// Update the template with the imports.
	// Ensure that there is a Go expression to add the imports to as the first node.
	gofile, err := goparser.ParseFile(fset, fileName, applyPrefix(t.Package, nil, importsNode.Expression.Value), goparser.AllErrors)
	if err != nil {
		return t, fmt.Errorf("failed to parse imports section: %w", err)
	}
	gofile.Imports = nil
	for _, key := range getSortedMapKeys(allImports) {
		pkg := allImports[key]
		gofile.Imports = append(gofile.Imports, pkg)
		newImportDecl := &ast.GenDecl{
			Tok:   token.IMPORT,
			Specs: []ast.Spec{pkg},
		}
		gofile.Decls = append([]ast.Decl{newImportDecl}, gofile.Decls...)
	}
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

func getSortedMapKeys(m map[string]*ast.ImportSpec) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, len(m))
	var i int
	for k := range m {
		keys[i] = k
		i++
	}
	slices.Sort(keys)
	slices.Reverse(keys)
	return keys
}

func walkTemplate(t parser.TemplateFile, f func(parser.Node) bool) {
	for _, n := range t.Nodes {
		hn, ok := n.(parser.HTMLTemplate)
		if !ok {
			continue
		}
		walkNodes(hn.Children, f)
	}
}

func walkNodes(t []parser.Node, f func(parser.Node) bool) {
	for _, n := range t {
		if !f(n) {
			continue
		}
		if h, ok := n.(parser.CompositeNode); ok {
			walkNodes(h.ChildNodes(), f)
		}
	}
}
