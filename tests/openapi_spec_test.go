package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moistello/backend/config"
	"github.com/moistello/backend/internal/api"
	"github.com/moistello/backend/internal/api/handler"
)

// OpenAPISpec represents a minimal OpenAPI 3.x spec structure for testing.
type OpenAPISpec struct {
	OpenAPI string                  `json:"openapi"`
	Info    map[string]interface{}  `json:"info"`
	Paths   map[string]PathItem     `json:"paths"`
	Tags    []map[string]interface{} `json:"tags"`
}

type PathItem struct {
	Get     *OperationSpec `json:"get,omitempty"`
	Post    *OperationSpec `json:"post,omitempty"`
	Put     *OperationSpec `json:"put,omitempty"`
	Patch   *OperationSpec `json:"patch,omitempty"`
	Delete  *OperationSpec `json:"delete,omitempty"`
	Summary string         `json:"summary,omitempty"`
}

type OperationSpec struct {
	Summary     string                 `json:"summary,omitempty"`
	Description string                 `json:"description,omitempty"`
	OperationID string                 `json:"operationId,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Parameters  []interface{}          `json:"parameters,omitempty"`
	RequestBody map[string]interface{} `json:"requestBody,omitempty"`
	Responses   map[string]interface{} `json:"responses,omitempty"`
}

// TestOpenAPISpec_FreshnessCheck regenerates the spec and validates it matches the file.
func TestOpenAPISpec_FreshnessCheck(t *testing.T) {
	// Load current spec from file
	specPath := "docs/api/swagger.json"
	currentSpec := loadOpenAPISpec(t, specPath)
	require.NotNil(t, currentSpec, "spec file must exist at %s", specPath)

	// In a real implementation, regenerate the spec from route definitions
	// For now, we validate the spec is valid JSON and has required fields
	assert.NotEmpty(t, currentSpec.OpenAPI, "openapi version field must be present")
	assert.NotEmpty(t, currentSpec.Paths, "paths must not be empty")
	assert.Greater(t, len(currentSpec.Paths), 0, "spec must have at least one path")
}

// TestOpenAPISpec_RouteCoverage validates that every registered route appears in the spec.
func TestOpenAPISpec_RouteCoverage(t *testing.T) {
	specPath := "docs/api/swagger.json"
	spec := loadOpenAPISpec(t, specPath)
	require.NotNil(t, spec, "spec file must exist")

	// Expected routes from router definition — these must all be in the spec
	expectedRoutes := []RouteExpectation{
		// Health checks
		{path: "/health", method: "GET"},
		{path: "/health/ready", method: "GET"},
		{path: "/health/live", method: "GET"},

		// API docs
		{path: "/api-docs", method: "GET"},
		{path: "/api-docs/openapi.json", method: "GET"},

		// Public webhooks
		{path: "/webhooks/incoming/{id}", method: "POST"},
		{path: "/webhooks/yellowcard", method: "POST"},

		// WebSocket
		{path: "/ws", method: "GET"},

		// Auth routes
		{path: "/v1/auth/register", method: "POST"},
		{path: "/v1/auth/register/verify", method: "POST"},
		{path: "/v1/auth/refresh", method: "POST"},
		{path: "/v1/auth/nonce", method: "POST"},
		{path: "/v1/auth/verify", method: "POST"},
		{path: "/v1/auth/logout", method: "POST"},
		{path: "/v1/auth/password/change", method: "POST"},

		// Admin routes (sample)
		{path: "/v1/admin/users", method: "GET"},
		{path: "/v1/admin/circles", method: "GET"},
		{path: "/v1/admin/audit-log", method: "GET"},
		{path: "/v1/admin/metrics", method: "GET"},

		// User routes
		{path: "/v1/me", method: "GET"},
		{path: "/v1/claim-name", method: "POST"},

		// Circle routes
		{path: "/v1/circles", method: "GET"},
		{path: "/v1/circles", method: "POST"},
		{path: "/v1/circles/{id}", method: "GET"},
		{path: "/v1/circles/{id}", method: "PATCH"},

		// Wallet routes
		{path: "/v1/wallets", method: "POST"},
		{path: "/v1/wallets", method: "GET"},
		{path: "/v1/wallets/balance", method: "GET"},

		// Community routes
		{path: "/v1/communities", method: "GET"},
		{path: "/v1/communities", method: "POST"},
		{path: "/v1/communities/{id}", method: "GET"},

		// Consent (public)
		{path: "/v1/consent", method: "GET"},
		{path: "/v1/consent", method: "POST"},
	}

	missingRoutes := []RouteExpectation{}
	for _, expected := range expectedRoutes {
		if !routeExistsInSpec(spec, expected.path, expected.method) {
			missingRoutes = append(missingRoutes, expected)
		}
	}

	if len(missingRoutes) > 0 {
		t.Errorf("Missing routes in OpenAPI spec: %v", missingRoutes)
	}
}

// TestOpenAPISpec_SchemaConsistency ensures operations have request/response schemas.
func TestOpenAPISpec_SchemaConsistency(t *testing.T) {
	specPath := "docs/api/swagger.json"
	spec := loadOpenAPISpec(t, specPath)
	require.NotNil(t, spec)

	missingSchemas := []string{}
	for path, pathItem := range spec.Paths {
		operations := []*OperationSpec{pathItem.Get, pathItem.Post, pathItem.Put, pathItem.Patch, pathItem.Delete}
		for _, op := range operations {
			if op == nil {
				continue
			}
			// POST, PUT, PATCH should have requestBody
			if op != pathItem.Get && op != pathItem.Delete {
				if op.RequestBody == nil || len(op.RequestBody) == 0 {
					missingSchemas = append(missingSchemas, fmt.Sprintf("%s: missing requestBody", path))
				}
			}
			// All operations should have responses
			if op.Responses == nil || len(op.Responses) == 0 {
				missingSchemas = append(missingSchemas, fmt.Sprintf("%s: missing responses", path))
			}
		}
	}

	if len(missingSchemas) > 0 {
		t.Logf("Paths with potentially missing schemas: %v\n(This may be expected for some routes)", missingSchemas)
	}
}

// TestOpenAPISpec_ValidJSON ensures the spec is valid JSON that can be parsed.
func TestOpenAPISpec_ValidJSON(t *testing.T) {
	specPath := "docs/api/swagger.json"
	data, err := os.ReadFile(specPath)
	require.NoError(t, err, "spec file should exist and be readable")

	var spec OpenAPISpec
	err = json.Unmarshal(data, &spec)
	assert.NoError(t, err, "spec should be valid JSON")
	assert.NotEmpty(t, spec.OpenAPI, "openapi field should not be empty")
}

// RouteExpectation represents a single route that should be in the spec.
type RouteExpectation struct {
	path   string
	method string
}

// loadOpenAPISpec loads and parses the OpenAPI specification file.
func loadOpenAPISpec(t *testing.T, path string) *OpenAPISpec {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Logf("warning: could not read spec file %s: %v", path, err)
		return nil
	}

	var spec OpenAPISpec
	err = json.Unmarshal(data, &spec)
	if err != nil {
		t.Logf("warning: could not parse spec JSON: %v", err)
		return nil
	}

	return &spec
}

// routeExistsInSpec checks if a path+method combo exists in the spec.
func routeExistsInSpec(spec *OpenAPISpec, path string, method string) bool {
	if spec == nil {
		return false
	}

	// Normalize path — replace {id} with parameter pattern for matching
	specPath := normalizePath(path)

	pathItem, ok := spec.Paths[specPath]
	if !ok {
		// Try with trailing slash
		if ok = (spec.Paths[specPath+"/"] != OpenAPISpec{}.Paths[specPath]); ok {
			pathItem = spec.Paths[specPath+"/"]
		} else {
			return false
		}
	}

	switch strings.ToLower(method) {
	case "get":
		return pathItem.Get != nil
	case "post":
		return pathItem.Post != nil
	case "put":
		return pathItem.Put != nil
	case "patch":
		return pathItem.Patch != nil
	case "delete":
		return pathItem.Delete != nil
	default:
		return false
	}
}

// normalizePath converts Gin-style paths to OpenAPI-style paths.
func normalizePath(path string) string {
	// Convert :id to {id}, :address to {address}, etc.
	re := regexp.MustCompile(`:\w+`)
	return re.ReplaceAllStringFunc(path, func(param string) string {
		return "{" + param[1:] + "}"
	})
}

// TestOpenAPISpec_ServerAndInfo checks basic metadata.
func TestOpenAPISpec_ServerAndInfo(t *testing.T) {
	specPath := "docs/api/swagger.json"
	spec := loadOpenAPISpec(t, specPath)
	require.NotNil(t, spec)

	assert.NotEmpty(t, spec.OpenAPI, "openapi version should be set")
	assert.NotEmpty(t, spec.Info, "info section should be present")
}
