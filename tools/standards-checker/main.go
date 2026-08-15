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

	// Rule 1.1 — Archetype A HTTP services must have cmd/router.go
	handlerDir := filepath.Join(serviceDir, "internal", "handler")
	if dirHasGoFiles(handlerDir) {
		routerCmd := filepath.Join(serviceDir, "cmd", "router.go")
		routerHandler := filepath.Join(serviceDir, "internal", "handler", "router.go")
		if !fileExists(routerCmd) && !fileExists(routerHandler) {
			violations = append(violations, Violation{
				Service: service,
				Rule:    "1.1",
				ID:      "missing-cmd-router",
				Path:    filepath.ToSlash(filepath.Join(service, "cmd", "main.go")),
				Line:    1,
				Message: "Archetype A service with `internal/handler` must isolate route setup into `cmd/router.go`",
			})
		}
	}

	// Rule 1.2 — Microservice entrypoints must call loadEnv(".env")
	mainCmd := filepath.Join(serviceDir, "cmd", "main.go")
	if fileExists(mainCmd) && service != "infra-provisioner" {
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, mainCmd, nil, 0)
		if err == nil && !hasLoadEnvCall(node) {
			violations = append(violations, Violation{
				Service: service,
				Rule:    "1.2",
				ID:      "missing-load-env",
				Path:    filepath.ToSlash(filepath.Join(service, "cmd", "main.go")),
				Line:    1,
				Message: "Microservice entrypoint `cmd/main.go` does not invoke `loadEnv(\".env\")` at startup",
			})
		}
	}

	// Rule 5.10 — Services with internal/consumer must declare shared transport interface in internal/consumer/amqp.go (or interfaces.go)
	consumerDir := filepath.Join(serviceDir, "internal", "consumer")
	if dirHasGoFiles(consumerDir) {
		amqpFile := filepath.Join(consumerDir, "amqp.go")
		interfacesFile := filepath.Join(consumerDir, "interfaces.go")
		if !fileExists(amqpFile) && !fileExists(interfacesFile) {
			violations = append(violations, Violation{
				Service: service,
				Rule:    "5.10",
				ID:      "missing-consumer-amqp-interface-file",
				Path:    filepath.ToSlash(filepath.Join(service, "internal", "consumer", "amqp.go")),
				Line:    1,
				Message: "Consumer package lacks a centralized transport interface file (`internal/consumer/amqp.go` or `interfaces.go`) defining package-level AMQPClient contract",
			})
		}
	}

	// Rule 5.9 — Infrastructure rabbitmq.Client must provide Consume method
	rmqClientFile := filepath.Join(serviceDir, "internal", "infrastructure", "rabbitmq", "client.go")
	if fileExists(rmqClientFile) {
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, rmqClientFile, nil, 0)
		if err == nil && !hasConsumeMethod(node) {
			violations = append(violations, Violation{
				Service: service,
				Rule:    "5.9",
				ID:      "missing-rabbitmq-consume-method",
				Path:    filepath.ToSlash(filepath.Join(service, "internal", "infrastructure", "rabbitmq", "client.go")),
				Line:    1,
				Message: "Infrastructure `rabbitmq.Client` must provide a `Consume(queueName, consumerTag string) (<-chan Delivery, error)` method to encapsulate transport driver interactions",
			})
		}
	}

	// Rule 8.1 — Every internal Go source file must ship a corresponding *_test.go file
	internalDir := filepath.Join(serviceDir, "internal")
	if _, err := os.Stat(internalDir); err == nil {
		_ = filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// Skip embed.go
			if filepath.Base(path) == "embed.go" {
				return nil
			}

			relPath, _ := filepath.Rel(repoRoot, path)
			relPath = filepath.ToSlash(relPath)

			dir := filepath.Dir(path)
			base := filepath.Base(path)
			nameNoExt := strings.TrimSuffix(base, ".go")

			normalizedName := strings.ReplaceAll(strings.ToLower(nameNoExt), "_", "") + "test"
			entries, err := os.ReadDir(dir)
			hasMatchingTest := false
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
						testNameNoExt := strings.TrimSuffix(entry.Name(), ".go")
						normalizedTest := strings.ReplaceAll(strings.ToLower(testNameNoExt), "_", "")
						if normalizedTest == normalizedName {
							hasMatchingTest = true
							break
						}
					}
				}
			}

			if !hasMatchingTest {
				violations = append(violations, Violation{
					Service: service,
					Rule:    "8.1",
					ID:      "missing-unit-tests",
					Path:    relPath,
					Line:    1,
					Message: fmt.Sprintf("file `%s` lacks a matching `*_test.go` unit test file — Rule 8.1 requires 1:1 unit test files for every source file", base),
				})
			}
			return nil
		})
	}

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

	// Rule 9 — Docker & Deployment standards (context-aware per archetype)
	dockerViolations := checkDockerStandards(repoRoot, service, serviceDir)
	violations = append(violations, dockerViolations...)

	return violations
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirHasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			return true
		}
	}
	return false
}

func checkPackageTests(dir string) (hasGo bool, hasTest bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".go") {
			if strings.HasSuffix(name, "_test.go") {
				hasTest = true
			} else {
				hasGo = true
			}
		}
	}
	return hasGo, hasTest
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

		// Rule 5.9 — Consumer directly importing third-party AMQP driver
		if strings.Contains(relPath, "/consumer/") && !isTest {
			if pathVal == "github.com/rabbitmq/amqp091-go" {
				add(imp.Pos(), "5.9", "consumer-driver-import", "consumer directly imports `github.com/rabbitmq/amqp091-go` — encapsulate transport channel consumption in `internal/infrastructure/rabbitmq`")
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

			// Rule 5.8 — AMQP Consumer constructors must not perform eager network I/O or topology setup
			if strings.Contains(relPath, "/consumer/") && !isTest && strings.HasPrefix(fn.Name.Name, "New") && fn.Body != nil {
				ast.Inspect(fn.Body, func(bodyNode ast.Node) bool {
					if call, ok := bodyNode.(*ast.CallExpr); ok {
						callName := ""
						if ident, ok := call.Fun.(*ast.Ident); ok {
							callName = ident.Name
						} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
							callName = sel.Sel.Name
						}
						if callName == "setupTopology" || callName == "DeclareExchange" || callName == "QueueDeclare" || callName == "QueueBind" || callName == "DeclareAndBindQueue" {
							add(call.Pos(), "5.8", "consumer-constructor-io", fmt.Sprintf("New* constructor invokes eager network/topology method `%s` — defer topology setup to Start(ctx) or runConsumerLoop", callName))
						}
					}
					return true
				})
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
			if strings.Contains(relPath, "/repository/") && isWriteMethod(fn.Name.Name) {
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
			if !isTest {
				for _, field := range fn.Fields.List {
					// Rule 2.2 — Struct fields holding concrete pointers to lower/peer layer structs
					if star, ok := field.Type.(*ast.StarExpr); ok {
						if sel, ok := star.X.(*ast.SelectorExpr); ok {
							if pkgIdent, ok := sel.X.(*ast.Ident); ok {
								pkgName := pkgIdent.Name
								typeName := sel.Sel.Name
								if (strings.Contains(relPath, "/handler/") || strings.Contains(relPath, "/consumer/")) && pkgName == "service" {
									add(field.Pos(), "2.2", "struct-concrete-dependency", fmt.Sprintf("struct field uses concrete pointer `*%s.%s` — depend on an interface defined in the consuming package instead", pkgName, typeName))
								} else if strings.Contains(relPath, "/consumer/") && pkgName == "rabbitmq" {
									add(field.Pos(), "2.2", "struct-concrete-dependency", fmt.Sprintf("consumer struct field uses concrete pointer `*%s.%s` — define an AMQP interface in consumer package instead", pkgName, typeName))
								} else if strings.Contains(relPath, "/service/") && (pkgName == "repository" || pkgName == "publisher" || pkgName == "provider") {
									add(field.Pos(), "2.2", "struct-concrete-dependency", fmt.Sprintf("struct field uses concrete pointer `*%s.%s` — depend on an interface defined in the consuming package instead", pkgName, typeName))
								}
							}
						}
					}

					// Rule 6.1 — Service DTOs carrying json tags
					if strings.Contains(relPath, "/service/") {
						if field.Tag != nil && strings.Contains(field.Tag.Value, `json:"`) {
							add(field.Pos(), "6.1", "json-tag-in-service", "`json:\"...\"` tag found in the service layer — JSON belongs in handler DTOs only")
						}
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

				// Rule 5.7 — Consumer Nack(false, true) without delivery count check
				if strings.Contains(relPath, "/consumer/") && !isTest && sel.Sel.Name == "Nack" {
					if len(fn.Args) >= 2 {
						if ident, ok := fn.Args[1].(*ast.Ident); ok && ident.Name == "true" {
							if !hasDeliveryCountLogic(file) {
								add(fn.Pos(), "5.7", "consumer-unbounded-requeue", "consumer calls `Nack(false, true)` with unbounded requeue without checking delivery count / headers — route to DLQ after max retries")
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

		case *ast.BasicLit:
			if !isTest && fn.Kind == token.STRING {
				val := strings.Trim(fn.Value, "`\"")
				if strings.Contains(val, "-----BEGIN PUBLIC KEY-----") ||
					strings.Contains(val, "-----BEGIN PRIVATE KEY-----") ||
					strings.Contains(val, "-----BEGIN RSA PRIVATE KEY-----") {
					add(fn.Pos(), "7.1", "hardcoded-crypto-fallback", "hardcoded RSA/ECDSA key PEM block found in binary — load keys dynamically from environment or secret manager")
				}

				if strings.HasSuffix(relPath, "cmd/main.go") {
					switch val {
					case "POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "PAYMENT_SERVICE_PORT", "AUTH_SERVICE_PORT", "ORDER_SERVICE_PORT", "USER_SERVICE_PORT", "TENANT_SERVICE_PORT", "NOTIFICATION_SERVICE_PORT":
						add(fn.Pos(), "1.3", "env-naming-convention", fmt.Sprintf("non-standard environment variable key `%s` used in `cmd/main.go` — use standard `PORT` and `DB_*` keys", val))
					}
				}
			}
		}

		// Rule 3.4 — Abbreviated identifiers (in production code by default; in tests only when --strict is set)
		if !isTest || strict {
			checkAbbrev(n, relPath, add)
		}

		return true
	})

	// Rule 5.6 — Domain event consumers must implement Inbox deduplication guard
	if !isTest {
		checkConsumerInboxGuard(file, relPath, service, add)
	}

	// Rule 6.3 — internal_* files must declare Internal* struct
	if isInternalFile(relPath) && !hasInternalType(file) {
		add(file.Pos(), "6.3", "internal-file-without-internal-type", "internal_* file must declare an `Internal*` handler/service struct")
	}

	return violations
}

func hasDeliveryCountLogic(file *ast.File) bool {
	hasCheck := false
	ast.Inspect(file, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			lower := strings.ToLower(ident.Name)
			if strings.Contains(lower, "deliverycount") || strings.Contains(lower, "maxdeliver") {
				hasCheck = true
				return false
			}
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			val := strings.Trim(lit.Value, `"'`)
			if val == "x-delivery-count" || val == "x-death" {
				hasCheck = true
				return false
			}
		}
		return true
	})
	return hasCheck
}

func checkConsumerInboxGuard(file *ast.File, relPath, service string, add func(token.Pos, string, string, string)) {
	if !strings.Contains(relPath, "/consumer/") || strings.HasSuffix(relPath, "_test.go") {
		return
	}
	// Infra provisioner is worker-based OS command executor; order-service infra consumers are DDL sync
	if service == "infra-provisioner" || service == "order-service" {
		return
	}

	hasDomainEvent := false
	var eventPos token.Pos
	hasInbox := false

	ast.Inspect(file, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			name := ident.Name
			if strings.HasSuffix(name, "Event") && name != "Event" && !strings.Contains(name, "Publisher") {
				hasDomainEvent = true
				if eventPos == token.NoPos {
					eventPos = ident.Pos()
				}
			}
			if strings.Contains(name, "Inbox") || strings.Contains(name, "ClaimEvent") {
				hasInbox = true
			}
		}
		return true
	})

	if hasDomainEvent && !hasInbox {
		if eventPos == token.NoPos {
			eventPos = file.Pos()
		}
		add(eventPos, "5.6", "consumer-missing-inbox-guard", "consumer processes domain event without an InboxService/ClaimEvent idempotency guard")
	}
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
	reAbbrevMgr  = regexp.MustCompile(`\b\w*[mM]gr\b`)
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
		} else if reAbbrevMgr.MatchString(name) {
			add(ident.Pos(), "3.4", "abbrev-mgr", "abbreviated manager identifier (`mgr` / `*Mgr`) — use a full word like `txManager`")
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

func hasLoadEnvCall(file *ast.File) bool {
	hasCall := false
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "loadEnv" {
				hasCall = true
				return false
			}
		}
		return true
	})
	return hasCall
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

func checkDockerStandards(repoRoot, service, serviceDir string) []Violation {
	var violations []Violation
	add := func(relPath, rule, id, msg string, line int) {
		violations = append(violations, Violation{
			Service: service,
			Rule:    rule,
			ID:      id,
			Path:    relPath,
			Line:    line,
			Message: msg,
		})
	}

	isArchetypeC := (service == "infra-provisioner")
	isArchetypeB := (service == "notification-service")
	isArchetypeA := (!isArchetypeC && !isArchetypeB)

	// Rule 9.1 — Every service must have a .dockerignore containing .env
	dockerIgnorePath := filepath.Join(serviceDir, ".dockerignore")
	relDockerIgnore, _ := filepath.Rel(repoRoot, dockerIgnorePath)
	relDockerIgnore = filepath.ToSlash(relDockerIgnore)

	if !fileExists(dockerIgnorePath) {
		add(relDockerIgnore, "9.1", "missing-dockerignore", "service directory lacks a `.dockerignore` file", 1)
	} else {
		content, err := os.ReadFile(dockerIgnorePath)
		if err == nil {
			lines := strings.Split(string(content), "\n")
			hasEnv := false
			for _, l := range lines {
				if strings.TrimSpace(l) == ".env" {
					hasEnv = true
					break
				}
			}
			if !hasEnv {
				add(relDockerIgnore, "9.1", "dockerignore-missing-env", "`.dockerignore` does not include `.env` to prevent host secrets from leaking into container images", 1)
			}
		}
	}

	// Rule 9.2 — Dockerfile must exist and use BuildKit cache mounts
	dockerfilePath := filepath.Join(serviceDir, "Dockerfile")
	relDockerfile, _ := filepath.Rel(repoRoot, dockerfilePath)
	relDockerfile = filepath.ToSlash(relDockerfile)

	if !fileExists(dockerfilePath) {
		add(relDockerfile, "9.2", "missing-dockerfile", "service directory lacks a `Dockerfile`", 1)
	} else {
		content, err := os.ReadFile(dockerfilePath)
		if err == nil {
			dfStr := string(content)
			if !strings.Contains(dfStr, "--mount=type=cache,target=/go/pkg/mod") {
				add(relDockerfile, "9.2", "dockerfile-missing-mod-cache-mount", "Dockerfile must use BuildKit cache mount `--mount=type=cache,target=/go/pkg/mod` for `go mod download`", 1)
			}
			if !strings.Contains(dfStr, "--mount=type=cache,target=/root/.cache/go-build") {
				add(relDockerfile, "9.2", "dockerfile-missing-build-cache-mount", "Dockerfile must use BuildKit cache mount `--mount=type=cache,target=/root/.cache/go-build` for `go build`", 1)
			}
		}
	}

	// Rule 9.3 & 9.4 — Docker Compose standards (context-aware per archetype)
	composePath := filepath.Join(serviceDir, "docker-compose.yml")
	relCompose, _ := filepath.Rel(repoRoot, composePath)
	relCompose = filepath.ToSlash(relCompose)

	if isArchetypeC {
		// Archetype C (infra-provisioner) must NOT have a standalone root docker-compose.yml
		if fileExists(composePath) {
			add(relCompose, "9.3", "archetype-c-standalone-compose", "Archetype C (`infra-provisioner`) is platform infrastructure and must be declared in `infrastructure/docker-compose.yml`, not as a standalone root compose file", 1)
		}
	} else {
		// Archetype A & B must have a root docker-compose.yml
		if !fileExists(composePath) {
			add(relCompose, "9.3", "missing-docker-compose", "Archetype A/B service must have a root `docker-compose.yml` for independent deployment", 1)
		} else {
			content, err := os.ReadFile(composePath)
			if err == nil {
				lines := strings.Split(string(content), "\n")
				for idx, line := range lines {
					trimmed := strings.TrimSpace(line)
					lineNum := idx + 1

					// Rule 9.4 — No hardcoded environment variables in docker-compose.yml
					if strings.HasPrefix(trimmed, "PORT:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "PORT:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-port", fmt.Sprintf("`PORT: %s` in docker-compose.yml is hardcoded — use `${PORT:-...}` dynamic syntax", val), lineNum)
						}
					}

					if strings.HasPrefix(trimmed, "DB_HOST:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "DB_HOST:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-db-host", fmt.Sprintf("`DB_HOST: %s` in docker-compose.yml is hardcoded — use `${<SERVICE>_DB_HOST:-postgres}` dynamic syntax", val), lineNum)
						}
					}

					if strings.HasPrefix(trimmed, "SHARED_DB_HOST:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "SHARED_DB_HOST:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-shared-db-host", fmt.Sprintf("`SHARED_DB_HOST: %s` in docker-compose.yml is hardcoded — use `${SHARED_DB_HOST:-postgres}` dynamic syntax", val), lineNum)
						}
					}

					if strings.HasPrefix(trimmed, "RABBITMQ_URL:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "RABBITMQ_URL:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-rabbitmq-url", fmt.Sprintf("`RABBITMQ_URL: %s` in docker-compose.yml is hardcoded — use `${RABBITMQ_URL:-...}` dynamic syntax", val), lineNum)
						}
					}

					if strings.HasPrefix(trimmed, "AUTH_SERVICE_URL:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "AUTH_SERVICE_URL:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-auth-url", fmt.Sprintf("`AUTH_SERVICE_URL: %s` in docker-compose.yml is hardcoded — use `${AUTH_SERVICE_URL:-...}` dynamic syntax", val), lineNum)
						}
					}

					if strings.HasPrefix(trimmed, "TENANT_SERVICE_URL:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "TENANT_SERVICE_URL:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-tenant-url", fmt.Sprintf("`TENANT_SERVICE_URL: %s` in docker-compose.yml is hardcoded — use `${TENANT_SERVICE_URL:-...}` dynamic syntax", val), lineNum)
						}
					}

					if strings.HasPrefix(trimmed, "SMTP_HOST:") {
						val := strings.TrimSpace(strings.TrimPrefix(trimmed, "SMTP_HOST:"))
						if !strings.Contains(val, "${") {
							add(relCompose, "9.4", "hardcoded-compose-smtp-host", fmt.Sprintf("`SMTP_HOST: %s` in docker-compose.yml is hardcoded — use `${SMTP_HOST:-mailpit}` dynamic syntax", val), lineNum)
						}
					}
				}

				// Archetype A specific: Traefik routing rules
				if isArchetypeA {
					composeStr := string(content)
					if !strings.Contains(composeStr, "traefik.enable=true") {
						add(relCompose, "9.3", "missing-traefik-enable", "Archetype A HTTP service must declare `traefik.enable=true` label in docker-compose.yml", 1)
					}
					if !strings.Contains(composeStr, "traefik.http.routers") {
						add(relCompose, "9.3", "missing-traefik-router", "Archetype A HTTP service must declare Traefik HTTP router labels in docker-compose.yml", 1)
					}
				}
			}
		}
	}

	return violations
}

func hasConsumeMethod(file *ast.File) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if fn.Name.Name == "Consume" && fn.Recv != nil {
				return true
			}
		}
	}
	return false
}

