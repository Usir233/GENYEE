package internal_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	repoImportPrefix       = "github.com/Usir233/GENYEE/internal/application/repository"
	interfacesImportPath   = "github.com/Usir233/GENYEE/internal/types/interfaces"
	gormImportPrefix       = "gorm.io/gorm"
	databaseSQLImportPath  = "database/sql"
	asynqDLAllowedRelSubir = "middleware/asynqdl"
)

// TestHandlerAndMiddlewareLayeringGuard enforces Clean Architecture boundaries
// across internal/handler/** and internal/middleware/** (excluding middleware/asynqdl):
//  1. Non-test files MUST NOT import internal/application/repository, gorm.io/gorm, or database/sql.
//  2. Non-test files MUST NOT hold or accept interfaces.*Repository, gorm.*, or sql.* types
//     in struct fields, constructor parameters, or function signatures.
func TestHandlerAndMiddlewareLayeringGuard(t *testing.T) {
	_, callerFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve internal/architecture_test.go path via runtime.Caller")
	}
	internalDir := filepath.Dir(callerFile)

	fset := token.NewFileSet()
	var scannedFiles int
	var violations []string

	for _, subdir := range []string{"handler", "middleware"} {
		root := filepath.Join(internalDir, subdir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, relErr := filepath.Rel(internalDir, path)
			if relErr != nil {
				return relErr
			}
			relSlash := filepath.ToSlash(rel)

			if d.IsDir() {
				if relSlash == asynqDLAllowedRelSubir || strings.HasPrefix(relSlash, asynqDLAllowedRelSubir+"/") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}

			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return fmt.Errorf("parse %s: %w", relSlash, parseErr)
			}
			scannedFiles++
			violations = append(violations, inspectFileLayering(fset, relSlash, file)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", subdir, err)
		}
	}

	if scannedFiles == 0 {
		t.Fatal("AST guard scanned 0 files; expected non-test files in internal/handler and internal/middleware")
	}
	if len(violations) > 0 {
		t.Fatalf(
			"Clean Architecture layering violations detected across %d scanned files (%d violations):\n  - %s",
			scannedFiles,
			len(violations),
			strings.Join(violations, "\n  - "),
		)
	}
}

func TestInspectFileLayeringDetectsProhibitedPatterns(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantSubstr string
	}{
		{
			name: "concrete repository import",
			src: `package handler
import _ "github.com/Usir233/GENYEE/internal/application/repository"
`,
			wantSubstr: "prohibited import \"github.com/Usir233/GENYEE/internal/application/repository\"",
		},
		{
			name: "gorm import",
			src: `package handler
import "gorm.io/gorm"
type H struct { db *gorm.DB }
`,
			wantSubstr: "prohibited import \"gorm.io/gorm\"",
		},
		{
			name: "database sql import",
			src: `package middleware
import "database/sql"
var _ *sql.DB
`,
			wantSubstr: "prohibited import \"database/sql\"",
		},
		{
			name: "interfaces repository struct field",
			src: `package handler
import "github.com/Usir233/GENYEE/internal/types/interfaces"
type Handler struct {
	repo interfaces.KnowledgeBaseRepository
}
`,
			wantSubstr: "struct field \"repo\" references prohibited type interfaces.KnowledgeBaseRepository",
		},
		{
			name: "aliased interfaces repository constructor param",
			src: `package handler
import iface "github.com/Usir233/GENYEE/internal/types/interfaces"
func NewHandler(r iface.UserRepository) {}
`,
			wantSubstr: "parameter \"r\" in NewHandler references prohibited type iface.UserRepository",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "synthetic.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}
			got := inspectFileLayering(fset, "handler/synthetic.go", file)
			if len(got) == 0 {
				t.Fatalf("expected violation containing %q, got none", tc.wantSubstr)
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.wantSubstr) {
				t.Fatalf("violations = %q, want substring %q", joined, tc.wantSubstr)
			}
		})
	}
}

func inspectFileLayering(fset *token.FileSet, displayPath string, file *ast.File) []string {
	var violations []string

	interfacesAliases := map[string]bool{
		"interfaces": true,
	}
	dotImportedInterfaces := false

	for _, imp := range file.Imports {
		impPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if isProhibitedImport(impPath) {
			pos := fset.Position(imp.Pos())
			violations = append(violations, fmt.Sprintf(
				"%s:%d: prohibited import %q",
				displayPath, pos.Line, impPath,
			))
		}
		if impPath == interfacesImportPath {
			if imp.Name != nil {
				switch imp.Name.Name {
				case ".":
					dotImportedInterfaces = true
				case "_":
				default:
					interfacesAliases[imp.Name.Name] = true
				}
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.StructType:
			if node.Fields == nil {
				return true
			}
			for _, field := range node.Fields.List {
				badType, hit := findProhibitedLayerType(field.Type, interfacesAliases, dotImportedInterfaces)
				if !hit {
					continue
				}
				pos := fset.Position(field.Pos())
				fieldName := "<embedded>"
				if len(field.Names) > 0 {
					names := make([]string, 0, len(field.Names))
					for _, id := range field.Names {
						names = append(names, id.Name)
					}
					fieldName = strings.Join(names, ",")
				}
				violations = append(violations, fmt.Sprintf(
					"%s:%d: struct field %q references prohibited type %s",
					displayPath, pos.Line, fieldName, badType,
				))
			}
		case *ast.FuncDecl:
			funcName := node.Name.Name
			violations = append(violations, inspectFieldList(
				fset, displayPath, "parameter", funcName,
				node.Type.Params, interfacesAliases, dotImportedInterfaces,
			)...)
			violations = append(violations, inspectFieldList(
				fset, displayPath, "result", funcName,
				node.Type.Results, interfacesAliases, dotImportedInterfaces,
			)...)
		case *ast.FuncLit:
			violations = append(violations, inspectFieldList(
				fset, displayPath, "parameter", "<func literal>",
				node.Type.Params, interfacesAliases, dotImportedInterfaces,
			)...)
			violations = append(violations, inspectFieldList(
				fset, displayPath, "result", "<func literal>",
				node.Type.Results, interfacesAliases, dotImportedInterfaces,
			)...)
		}
		return true
	})

	return violations
}

func inspectFieldList(
	fset *token.FileSet,
	displayPath string,
	kind string,
	funcName string,
	fields *ast.FieldList,
	interfacesAliases map[string]bool,
	dotImportedInterfaces bool,
) []string {
	if fields == nil {
		return nil
	}
	var violations []string
	for _, field := range fields.List {
		badType, hit := findProhibitedLayerType(field.Type, interfacesAliases, dotImportedInterfaces)
		if !hit {
			continue
		}
		pos := fset.Position(field.Pos())
		paramName := "<unnamed>"
		if len(field.Names) > 0 {
			names := make([]string, 0, len(field.Names))
			for _, id := range field.Names {
				names = append(names, id.Name)
			}
			paramName = strings.Join(names, ",")
		}
		violations = append(violations, fmt.Sprintf(
			"%s:%d: %s %q in %s references prohibited type %s",
			displayPath, pos.Line, kind, paramName, funcName, badType,
		))
	}
	return violations
}

func isProhibitedImport(impPath string) bool {
	if impPath == repoImportPrefix || strings.HasPrefix(impPath, repoImportPrefix+"/") {
		return true
	}
	if impPath == gormImportPrefix || strings.HasPrefix(impPath, gormImportPrefix+"/") {
		return true
	}
	if impPath == databaseSQLImportPath || strings.HasPrefix(impPath, databaseSQLImportPath+"/") {
		return true
	}
	return false
}

func findProhibitedLayerType(
	expr ast.Expr,
	interfacesAliases map[string]bool,
	dotImportedInterfaces bool,
) (string, bool) {
	if expr == nil {
		return "", false
	}
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		pkgIdent, ok := t.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		if interfacesAliases[pkgIdent.Name] && strings.HasSuffix(t.Sel.Name, "Repository") {
			return pkgIdent.Name + "." + t.Sel.Name, true
		}
		if pkgIdent.Name == "gorm" || pkgIdent.Name == "sql" {
			return pkgIdent.Name + "." + t.Sel.Name, true
		}
		return "", false
	case *ast.Ident:
		if dotImportedInterfaces && strings.HasSuffix(t.Name, "Repository") {
			return t.Name, true
		}
		return "", false
	case *ast.StarExpr:
		return findProhibitedLayerType(t.X, interfacesAliases, dotImportedInterfaces)
	case *ast.ArrayType:
		return findProhibitedLayerType(t.Elt, interfacesAliases, dotImportedInterfaces)
	case *ast.Ellipsis:
		return findProhibitedLayerType(t.Elt, interfacesAliases, dotImportedInterfaces)
	case *ast.ParenExpr:
		return findProhibitedLayerType(t.X, interfacesAliases, dotImportedInterfaces)
	case *ast.ChanType:
		return findProhibitedLayerType(t.Value, interfacesAliases, dotImportedInterfaces)
	case *ast.MapType:
		if bad, ok := findProhibitedLayerType(t.Key, interfacesAliases, dotImportedInterfaces); ok {
			return bad, true
		}
		return findProhibitedLayerType(t.Value, interfacesAliases, dotImportedInterfaces)
	case *ast.IndexExpr:
		if bad, ok := findProhibitedLayerType(t.X, interfacesAliases, dotImportedInterfaces); ok {
			return bad, true
		}
		return findProhibitedLayerType(t.Index, interfacesAliases, dotImportedInterfaces)
	case *ast.IndexListExpr:
		if bad, ok := findProhibitedLayerType(t.X, interfacesAliases, dotImportedInterfaces); ok {
			return bad, true
		}
		for _, idx := range t.Indices {
			if bad, ok := findProhibitedLayerType(idx, interfacesAliases, dotImportedInterfaces); ok {
				return bad, true
			}
		}
		return "", false
	default:
		return "", false
	}
}
