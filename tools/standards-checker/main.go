package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Violation struct {
	Service string `json:"service"`
	Rule    string `json:"rule"`
	ID      string `json:"id"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

var services = []string{
	"auth-service",
	"order-service",
	"tenant-service",
	"notification-service",
	"user-service",
	"infra-provisioner",
	"payment-service",
}

func main() {
	jsonFlag := flag.Bool("json", false, "machine-readable output")
	quietFlag := flag.Bool("quiet", false, "summary only")
	strictFlag := flag.Bool("strict", false, "include *_test.go in style checks")
	serviceFlag := flag.String("service", "", "scan one service")
	flag.Parse()

	targetServices := services
	if *serviceFlag != "" {
		targetServices = []string{*serviceFlag}
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error locating repository root: %v\n", err)
		os.Exit(2)
	}

	var allViolations []Violation

	for _, service := range targetServices {
		serviceDir := filepath.Join(repoRoot, service)
		if _, err := os.Stat(serviceDir); os.IsNotExist(err) {
			continue
		}

		violations := scanService(repoRoot, service, *strictFlag)
		allViolations = append(allViolations, violations...)
	}

	if *jsonFlag {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(allViolations)
	} else if *quietFlag {
		fmt.Printf("Total: %d violations across %d services\n", len(allViolations), len(targetServices))
	} else {
		printHumanReport(allViolations)
	}

	if len(allViolations) > 0 {
		os.Exit(1)
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("README.md not found in parent directories")
}

func scanService(repoRoot, service string, strict bool) []Violation {
	var violations []Violation
	serviceDir := filepath.Join(repoRoot, service)

	_ = filepath.Walk(serviceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		relPath, _ := filepath.Rel(repoRoot, path)
		relPath = filepath.ToSlash(relPath)

		isTest := strings.HasSuffix(relPath, "_test.go")

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		fileViolations := checkFile(fset, node, service, relPath, isTest, strict)
		violations = append(violations, fileViolations...)

		return nil
	})

	return violations
}

func checkFile(fset *token.FileSet, file *ast.File, service, relPath string, isTest, strict bool) []Violation {
	var violations []Violation

	add := func(pos token.Pos, rule, id, msg string) {
		line := fset.Position(pos).Line
		violations = append(violations, Violation{
			Service: service,
			Rule:    rule,
			ID:      id,
			Path:    relPath,
			Line:    line,
			Message: msg,
		})
	}

	// 1. Check Imports
	for _, imp := range file.Imports {
		pathVal := strings.Trim(imp.Path.Value, `"`)

		// Rule 4.4 — database/sql leaked into service layer
		if strings.Contains(relPath, "/service/") && !strings.Contains(relPath, "order-service/internal/service/migration_service") {
			if pathVal == "database/sql" {
				add(imp.Pos(), "4.4", "sql-in-service", "`database/sql` imported in service layer — repositories must translate driver errors to domain sentinels")
			}
		}

		// Rule 2.2 — Layer 1 (consumer/handler) importing Layer 3 (repository/publisher)
		if (strings.Contains(relPath, "/consumer/") || strings.Contains(relPath, "/handler/")) && !isTest {
			if strings.HasSuffix(pathVal, "internal/repository") || strings.HasSuffix(pathVal, "internal/publisher") {
				add(imp.Pos(), "2.2", "layer-imports-repository", "imports Layer 3 (`internal/repository`/`internal/publisher`) from Layer 1 — depend on Layer 2 (service) via interface instead")
			}
		}
	}

	// 2. AST Inspection
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch fn := n.(type) {
		case *ast.FuncDecl:
			// Rule 2.1 — New* constructors return concrete struct pointers
			if strings.HasPrefix(fn.Name.Name, "New") && fn.Type.Results != nil {
				for _, res := range fn.Type.Results.List {
					if ident, ok := res.Type.(*ast.Ident); ok {
						if ident.Name != "error" && !isBasicType(ident.Name) {
							add(fn.Pos(), "2.1", "constructor-returns-concrete", fmt.Sprintf("New* constructor returns non-pointer type `%s` — return a concrete *%s pointer", ident.Name, ident.Name))
						}
					}
				}
			}

			// Rule 3.1 — Collection queries named Get* returning slice
			if (!isTest || strict) && strings.HasPrefix(fn.Name.Name, "Get") && fn.Type.Results != nil {
				for _, res := range fn.Type.Results.List {
					if _, ok := res.Type.(*ast.ArrayType); ok {
						add(fn.Pos(), "3.1", "get-returns-collection", "method returning a slice is named `Get*` — rule 3.1 requires `List*` for collection queries")
					}
				}
			}

			// Rule 6.1 — Repository write methods must accept DTOs
			if isWriteMethod(fn.Name.Name) {
				if fn.Type.Params != nil {
					for _, param := range fn.Type.Params.List {
						if isDomainStruct(param.Type) {
							add(fn.Pos(), "6.1", "repo-write-accepts-domain", "repo write method accepts a raw `domain.*` struct — define an `{Action}{Entity}Input` DTO instead")
						}
					}
				}
			}

			// Rule 6.1 — Service methods returning raw domain entities
			if strings.Contains(relPath, "/service/") && !isTest &&
				!strings.Contains(relPath, "order-service/internal/service/migration_service") &&
				!strings.Contains(relPath, "notification-service/internal/service/inbox_service") {
				if fn.Type.Results != nil {
					for _, res := range fn.Type.Results.List {
						if isDomainStruct(res.Type) {
							add(fn.Pos(), "6.1", "service-returns-domain", "service method returns a raw `domain.*` entity — expose a `{UseCase}Output` DTO instead")
						}
					}
				}
			}

		case *ast.TypeSpec:
			// Rule 6.2 — Domain entities suffixed with Record
			if strings.Contains(relPath, "/domain/") {
				if strings.HasSuffix(fn.Name.Name, "Record") {
					add(fn.Pos(), "6.2", "record-suffix-domain", fmt.Sprintf("domain entity `type %s` uses a prohibited `Record` suffix", fn.Name.Name))
				}
			}

			// Rule 2.2 — Layer 1 interface named Repository
			if (strings.Contains(relPath, "/consumer/") || strings.Contains(relPath, "/handler/")) && !isTest {
				if _, isIface := fn.Type.(*ast.InterfaceType); isIface && strings.HasSuffix(fn.Name.Name, "Repository") {
					add(fn.Pos(), "2.2", "layer-interface-repository-name", fmt.Sprintf("Layer 1 declares an interface named `%s` — consumer-side interfaces should be service-shaped", fn.Name.Name))
				}
			}

		case *ast.StructType:
			// Rule 6.1 — Service DTOs carrying json tags
			if strings.Contains(relPath, "/service/") && !isTest {
				for _, field := range fn.Fields.List {
					if field.Tag != nil && strings.Contains(field.Tag.Value, `json:"`) {
						add(field.Pos(), "6.1", "json-tag-in-service", "`json:\"...\"` tag found in the service layer — JSON belongs in handler DTOs only")
					}
				}
			}

		case *ast.CallExpr:
			// Rule 4.3 — fmt.Errorf with no format verbs
			if sel, ok := fn.Fun.(*ast.SelectorExpr); ok {
				if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "fmt" && sel.Sel.Name == "Errorf" {
					if len(fn.Args) == 1 {
						if lit, ok := fn.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							val := strings.Trim(lit.Value, `"`)
							if !strings.Contains(val, "%") || strings.Contains(val, "%%") {
								add(fn.Pos(), "4.3", "fmt-errorf-static", fmt.Sprintf("fmt.Errorf(\"%s\") has no format verb and no %%w — prefer errors.New or add %%w", val))
							}
						}
					}
				}
			}

		case *ast.ValueSpec:
			// Rule 4.1 — Sentinel errors outside domain
			if !strings.Contains(relPath, "/domain/") {
				for _, name := range fn.Names {
					if strings.HasPrefix(name.Name, "Err") && len(fn.Values) > 0 {
						if isErrorInit(fn.Values[0]) {
							add(name.Pos(), "4.1", "sentinel-outside-domain", fmt.Sprintf("sentinel `%s` declared outside internal/domain — export it from domain package", name.Name))
						}
					}
				}
			}

			// Rule 5.1 — AMQP constants outside domain/events.go
			if (strings.Contains(relPath, "/consumer/") || strings.Contains(relPath, "/publisher/")) && !strings.Contains(relPath, "domain/events.go") {
				for _, name := range fn.Names {
					if isAMQPConst(name.Name) {
						add(name.Pos(), "5.1", "amqp-const-outside-domain", fmt.Sprintf("AMQP constant `%s` declared in consumer/publisher — centralize in domain/events.go", name.Name))
					}
				}
			}
		}

		// Rule 3.4 — Abbreviated identifiers (only when --strict is set)
		if strict {
			checkAbbrev(n, relPath, add)
		}

		return true
	})

	// Rule 6.3 — internal_* files must declare Internal* struct
	if isInternalFile(relPath) && !hasInternalType(file) {
		add(file.Pos(), "6.3", "internal-file-without-internal-type", "internal_* file must declare an `Internal*` handler/service struct")
	}

	return violations
}

func isBasicType(name string) bool {
	switch name {
	case "bool", "string", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"byte", "rune", "float32", "float64", "complex64", "complex128":
		return true
	}
	return false
}

func isWriteMethod(name string) bool {
	prefixes := []string{"Create", "Update", "Upsert", "Save", "Insert", "Delete", "BulkCreate", "BulkUpdate"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func isDomainStruct(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return isDomainStruct(t.X)
	case *ast.ArrayType:
		return isDomainStruct(t.Elt)
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok && x.Name == "domain" {
			return true
		}
	}
	return false
}

func isErrorInit(expr ast.Expr) bool {
	if call, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				return (x.Name == "errors" && sel.Sel.Name == "New") || (x.Name == "fmt" && sel.Sel.Name == "Errorf")
			}
		}
	}
	return false
}

var amqpPattern = regexp.MustCompile(`^(?:[A-Z]\w*)?(?:Exchange|RoutingKey|Queue)\w*$`)

func isAMQPConst(name string) bool {
	return amqpPattern.MatchString(name)
}

var (
	reAbbrevRepo = regexp.MustCompile(`\b(?:\w*Repo\b|\brepo\b)`)
	reAbbrevSvc  = regexp.MustCompile(`\b\w*[sS]vc\b`)
	reAbbrevPub  = regexp.MustCompile(`\bpub\b`)
	reAbbrevCons = regexp.MustCompile(`\b\w*[cC]ons\b`)
	reAbbrevHnd  = regexp.MustCompile(`\b\w*[hH]nd\b`)
	reAbbrevMig  = regexp.MustCompile(`\bmig\b`)
)

func checkAbbrev(node ast.Node, relPath string, add func(token.Pos, string, string, string)) {
	if ident, ok := node.(*ast.Ident); ok {
		name := ident.Name
		if reAbbrevRepo.MatchString(name) {
			add(ident.Pos(), "3.4", "abbrev-repo", "abbreviated repository identifier (`repo` / `*Repo`) — use a full word like `roleRepository``")
		} else if reAbbrevSvc.MatchString(name) {
			add(ident.Pos(), "3.4", "abbrev-svc", "abbreviated service identifier (`svc` / `*Svc`) — use a full word like `workspaceService``")
		} else if reAbbrevPub.MatchString(name) {
			if strings.Contains(relPath, "/consumer/") || strings.Contains(relPath, "/publisher/") || strings.Contains(relPath, "/worker/") {
				add(ident.Pos(), "3.4", "abbrev-pub", "abbreviated publisher identifier (`pub`) — use a full word like `tenantEventPublisher`")
			}
		} else if reAbbrevCons.MatchString(name) {
			add(ident.Pos(), "3.4", "abbrev-cons", "abbreviated consumer identifier (`cons` / `*Cons`) — use a full word like `userCreatedConsumer`")
		} else if reAbbrevHnd.MatchString(name) {
			add(ident.Pos(), "3.4", "abbrev-hnd", "abbreviated handler identifier (`hnd` / `*Hnd`) — use a full word like `orderHandler`")
		} else if reAbbrevMig.MatchString(name) {
			add(ident.Pos(), "3.4", "abbrev-mig", "abbreviated migration identifier (`mig`) — use `migration`")
		}
	}
}

func isInternalFile(rel string) bool {
	return strings.Contains(rel, "internal_") && (strings.HasSuffix(rel, "_handler.go") || strings.HasSuffix(rel, "_service.go")) && !strings.HasSuffix(rel, "_test.go")
}

func hasInternalType(file *ast.File) bool {
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					if strings.HasPrefix(ts.Name.Name, "Internal") {
						return true
					}
				}
			}
		}
	}
	return false
}

func printHumanReport(violations []Violation) {
	if len(violations) == 0 {
		fmt.Println("No violations found. Clean!")
		return
	}

	byService := make(map[string][]Violation)
	for _, v := range violations {
		byService[v.Service] = append(byService[v.Service], v)
	}

	for svc, list := range byService {
		fmt.Printf("== %s ==\n", svc)
		for _, v := range list {
			fmt.Printf("  [Rule %s] %s\n    %s:%d\n    %s\n", v.Rule, v.ID, v.Path, v.Line, v.Message)
		}
	}
	fmt.Printf("\nTotal: %d violations across %d services\n", len(violations), len(byService))
}
