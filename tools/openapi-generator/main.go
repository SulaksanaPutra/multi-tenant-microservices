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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceMeta struct {
	Name  string
	Title string
	Port  int
}

var allServices = []ServiceMeta{
	{Name: "auth-service", Title: "Auth Service", Port: 8085},
	{Name: "user-service", Title: "User Service", Port: 8081},
	{Name: "tenant-service", Title: "Tenant Service", Port: 8082},
	{Name: "order-service", Title: "Order Service", Port: 8084},
	{Name: "notification-service", Title: "Notification Service", Port: 8083},
	{Name: "payment-service", Title: "Payment Service", Port: 8086},
}

type OpenAPISpec struct {
	OpenAPI    string                 `yaml:"openapi" json:"openapi"`
	Info       OpenAPIInfo            `yaml:"info" json:"info"`
	Servers    []OpenAPIServer        `yaml:"servers" json:"servers"`
	Tags       []OpenAPITag           `yaml:"tags" json:"tags"`
	Paths      map[string]PathItem    `yaml:"paths" json:"paths"`
	Components map[string]interface{} `yaml:"components,omitempty" json:"components,omitempty"`
}

type OpenAPIInfo struct {
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description" json:"description"`
	Version     string `yaml:"version" json:"version"`
}

type OpenAPIServer struct {
	URL string `yaml:"url" json:"url"`
}

type OpenAPITag struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

type PathItem map[string]Operation

type Operation struct {
	Tags        []string               `yaml:"tags" json:"tags"`
	Summary     string                 `yaml:"summary,omitempty" json:"summary,omitempty"`
	OperationID string                 `yaml:"operationId,omitempty" json:"operationId,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Category    string                 `yaml:"x-category,omitempty" json:"x-category,omitempty"`
	Service     string                 `yaml:"x-service,omitempty" json:"x-service,omitempty"`
	Permission  string                 `yaml:"x-permission,omitempty" json:"x-permission,omitempty"`
	Sources     []string               `yaml:"x-source,omitempty" json:"x-source,omitempty"`
	Security    []map[string][]string  `yaml:"security,omitempty" json:"security,omitempty"`
	Parameters  []Parameter            `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	RequestBody *RequestBody           `yaml:"requestBody,omitempty" json:"requestBody,omitempty"`
	Responses   map[string]Response    `yaml:"responses" json:"responses"`
}

type Parameter struct {
	Name        string                 `yaml:"name" json:"name"`
	In          string                 `yaml:"in" json:"in"`
	Required    bool                   `yaml:"required" json:"required"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Schema      map[string]interface{} `yaml:"schema" json:"schema"`
}

type RequestBody struct {
	Required bool                    `yaml:"required" json:"required"`
	Content  map[string]MediaContent `yaml:"content" json:"content"`
}

type MediaContent struct {
	Schema map[string]interface{} `yaml:"schema" json:"schema"`
}

type Response struct {
	Description string                  `yaml:"description" json:"description"`
	Content     map[string]MediaContent `yaml:"content,omitempty" json:"content,omitempty"`
}

func main() {
	outFlag := flag.String("out", filepath.Join("docs", "openapi"), "output directory")
	jsonFlag := flag.Bool("json", false, "output JSON format")
	yamlFlag := flag.Bool("yaml", true, "output YAML format")
	serviceFlag := flag.String("service", "", "specify single service")
	serverFlag := flag.String("server", "", "override server URL")
	noCombinedFlag := flag.Bool("no-combined", false, "skip combined openapi.yaml")
	noPerServiceFlag := flag.Bool("no-per-service", false, "skip per-service files")
	flag.Parse()

	format := "yaml"
	if *jsonFlag && !*yamlFlag {
		format = "json"
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error locating repository root: %v\n", err)
		os.Exit(2)
	}

	outDir := *outFlag
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(repoRoot, outDir)
	}
	_ = os.MkdirAll(outDir, 0755)

	targetServices := allServices
	if *serviceFlag != "" {
		targetServices = nil
		for _, s := range allServices {
			if s.Name == *serviceFlag {
				targetServices = append(targetServices, s)
				break
			}
		}
	}

	var combinedPaths = make(map[string]PathItem)
	var combinedTags []OpenAPITag

	for _, svc := range targetServices {
		svcDir := filepath.Join(repoRoot, svc.Name)
		if _, err := os.Stat(svcDir); os.IsNotExist(err) {
			continue
		}

		spec := generateServiceSpec(repoRoot, svc, *serverFlag)
		combinedTags = append(combinedTags, OpenAPITag{Name: svc.Name, Description: svc.Title})

		for p, item := range spec.Paths {
			if _, exists := combinedPaths[p]; !exists {
				combinedPaths[p] = make(PathItem)
			}
			for method, op := range item {
				combinedPaths[p][method] = op
			}
		}

		if !*noPerServiceFlag {
			ext := ".yaml"
			if format == "json" {
				ext = ".json"
			}
			outFile := filepath.Join(outDir, svc.Name+ext)
			writeSpec(outFile, spec, format)
			fmt.Printf("Generated %s (%d paths)\n", filepath.Base(outFile), len(spec.Paths))
		}
	}

	if !*noCombinedFlag && len(targetServices) > 1 {
		serverURL := "http://localhost:8000"
		if *serverFlag != "" {
			serverURL = *serverFlag
		}
		combinedSpec := OpenAPISpec{
			OpenAPI: "3.0.1",
			Info: OpenAPIInfo{
				Title:       "Microservice API Fleet",
				Description: "Combined OpenAPI 3.0.1 specification for all microservices.",
				Version:     "1.0.0",
			},
			Servers: []OpenAPIServer{{URL: serverURL}},
			Tags:    combinedTags,
			Paths:   combinedPaths,
			Components: map[string]interface{}{
				"securitySchemes": map[string]interface{}{
					"BearerAuth": map[string]interface{}{
						"type":         "http",
						"scheme":       "bearer",
						"bearerFormat": "JWT",
					},
					"InternalToken": map[string]interface{}{
						"type": "apiKey",
						"in":   "header",
						"name": "X-Internal-Service-Token",
					},
				},
			},
		}
		ext := ".yaml"
		if format == "json" {
			ext = ".json"
		}
		outFile := filepath.Join(outDir, "openapi"+ext)
		writeSpec(outFile, combinedSpec, format)
		fmt.Printf("Generated combined spec %s (%d paths)\n", filepath.Base(outFile), len(combinedPaths))
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

func generateServiceSpec(repoRoot string, svc ServiceMeta, serverOverride string) OpenAPISpec {
	serverURL := fmt.Sprintf("http://localhost:%d", svc.Port)
	if serverOverride != "" {
		serverURL = serverOverride
	}

	spec := OpenAPISpec{
		OpenAPI: "3.0.1",
		Info: OpenAPIInfo{
			Title:       svc.Title + " API",
			Description: "Auto-generated by `tools/openapi-generator` from Gin routers and handler DTOs.",
			Version:     "1.0.0",
		},
		Servers: []OpenAPIServer{{URL: serverURL}},
		Tags:    []OpenAPITag{{Name: svc.Name, Description: svc.Title}},
		Paths:   make(map[string]PathItem),
		Components: map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"BearerAuth": map[string]interface{}{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
				"InternalToken": map[string]interface{}{
					"type": "apiKey",
					"in":   "header",
					"name": "X-Internal-Service-Token",
				},
			},
		},
	}

	routerFile := filepath.Join(repoRoot, svc.Name, "cmd", "router.go")
	if _, err := os.Stat(routerFile); os.IsNotExist(err) {
		routerFile = filepath.Join(repoRoot, svc.Name, "cmd", "main.go")
	}

	routes := parseRouterRoutes(repoRoot, svc.Name, routerFile)

	for _, r := range routes {
		path := r.Path
		method := strings.ToLower(r.Method)

		if _, exists := spec.Paths[path]; !exists {
			spec.Paths[path] = make(PathItem)
		}

		op := Operation{
			Tags:        []string{svc.Name},
			Summary:     r.Summary,
			OperationID: r.OperationID,
			Description: r.Description,
			Category:    r.Category,
			Service:     svc.Name,
			Permission:  r.Permission,
			Sources:     r.Sources,
			Parameters:  r.Parameters,
			RequestBody: r.RequestBody,
			Responses:   r.Responses,
		}

		if r.Category == "jwt" {
			op.Security = []map[string][]string{{"BearerAuth": {}}}
		} else if r.Category == "internal" {
			op.Security = []map[string][]string{{"InternalToken": {}}}
		}

		spec.Paths[path][method] = op
	}

	return spec
}

type ExtractedRoute struct {
	Method      string
	Path        string
	OperationID string
	Summary     string
	Description string
	Category    string
	Permission  string
	Sources     []string
	Parameters  []Parameter
	RequestBody *RequestBody
	Responses   map[string]Response
}

func parseRouterRoutes(repoRoot, serviceName, routerFile string) []ExtractedRoute {
	var routes []ExtractedRoute
	if _, err := os.Stat(routerFile); os.IsNotExist(err) {
		return routes
	}

	content, err := os.ReadFile(routerFile)
	if err != nil {
		return routes
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, routerFile, content, parser.ParseComments)
	if err != nil {
		return routes
	}

	// Always add health endpoint if present in router
	relRouter, _ := filepath.Rel(repoRoot, routerFile)
	relRouter = filepath.ToSlash(relRouter)

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		method := strings.ToUpper(sel.Sel.Name)
		if method == "GET" || method == "POST" || method == "PUT" || method == "DELETE" {
			if len(call.Args) >= 2 {
				pathLit, ok := call.Args[0].(*ast.BasicLit)
				if ok && pathLit.Kind == token.STRING {
					pathVal := strings.Trim(pathLit.Value, `"`)

					// Standardize path params: :param -> {param}
					openAPIPath := reGinParam.ReplaceAllString(pathVal, "{$1}")

					handlerName := ""
					if ident, ok := call.Args[1].(*ast.Ident); ok {
						handlerName = ident.Name
					} else if hSel, ok := call.Args[1].(*ast.SelectorExpr); ok {
						handlerName = hSel.Sel.Name
					}

					opID := handlerName
					if opID == "" {
						opID = cleanOpID(pathVal)
					}

					category := "public"
					if strings.Contains(pathVal, "/internal/") {
						category = "internal"
					} else if strings.Contains(pathVal, "/api/") && !strings.Contains(pathVal, "/api/auth/login") && !strings.Contains(pathVal, "/api/auth/credentials/setup") && !strings.Contains(pathVal, "/api/tenants/register") {
						category = "jwt"
					}

					pos := fset.Position(call.Pos())
					sources := []string{fmt.Sprintf("%s:%d", relRouter, pos.Line)}

					route := ExtractedRoute{
						Method:      method,
						Path:        openAPIPath,
						OperationID: opID,
						Summary:     opID,
						Category:    category,
						Sources:     sources,
						Responses: map[string]Response{
							"200": {Description: "Successful response"},
						},
					}

					// Extract path parameters from route
					for _, m := range reOpenAPIParam.FindAllStringSubmatch(openAPIPath, -1) {
						route.Parameters = append(route.Parameters, Parameter{
							Name:        m[1],
							In:          "path",
							Required:    true,
							Description: fmt.Sprintf("Path parameter %s", m[1]),
							Schema:      map[string]interface{}{"type": "string"},
						})
					}

					if handlerName != "" {
						enrichRouteFromHandler(repoRoot, serviceName, handlerName, &route)
					}

					routes = append(routes, route)
				}
			}
		}
		return true
	})

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Path < routes[j].Path
	})

	return routes
}

var reGinParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)
var reOpenAPIParam = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func cleanOpID(path string) string {
	parts := strings.Split(path, "/")
	clean := ""
	for _, p := range parts {
		if p != "" && !strings.HasPrefix(p, ":") && !strings.HasPrefix(p, "{") {
			clean += strings.Title(p)
		}
	}
	if clean == "" {
		return "health"
	}
	return clean
}

func enrichRouteFromHandler(repoRoot, serviceName, handlerName string, route *ExtractedRoute) {
	handlerDir := filepath.Join(repoRoot, serviceName, "internal", "handler")
	if _, err := os.Stat(handlerDir); os.IsNotExist(err) {
		return
	}

	_ = filepath.Walk(handlerDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		relPath, _ := filepath.Rel(repoRoot, path)
		relPath = filepath.ToSlash(relPath)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == handlerName {
				route.Sources = append(route.Sources, fmt.Sprintf("%s:%d", relPath, fset.Position(fn.Pos()).Line))

				if fn.Doc != nil {
					route.Description = strings.TrimSpace(fn.Doc.Text())
				}

				// Inspect handler function body for query params and request body structs
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}

					sel, ok := call.Fun.(*ast.SelectorExpr)
					if ok {
						switch sel.Sel.Name {
						case "Query", "DefaultQuery":
							if len(call.Args) >= 1 {
								if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
									qName := strings.Trim(lit.Value, `"`)
									route.Parameters = append(route.Parameters, Parameter{
										Name:        qName,
										In:          "query",
										Required:    false,
										Description: fmt.Sprintf("Query parameter %s", qName),
										Schema:      map[string]interface{}{"type": "string"},
									})
								}
							}
						case "ShouldBindJSON", "BindJSON":
							route.RequestBody = &RequestBody{
								Required: true,
								Content: map[string]MediaContent{
									"application/json": {
										Schema: map[string]interface{}{"type": "object"},
									},
								},
							}
						}
					}
					return true
				})
			}
		}
		return nil
	})
}

func writeSpec(filename string, spec OpenAPISpec, format string) {
	var data []byte
	var err error
	if format == "json" {
		data, err = json.MarshalIndent(spec, "", "  ")
	} else {
		data, err = yaml.Marshal(spec)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling spec: %v\n", err)
		return
	}

	_ = os.WriteFile(filename, data, 0644)
}
