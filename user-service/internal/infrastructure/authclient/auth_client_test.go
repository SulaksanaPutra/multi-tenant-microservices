package authclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthClient_AssignUserRole(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/auth/users/usr_1/role" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": UserRoleResponse{
				UserID:   "usr_1",
				TenantID: "ten_1",
				RoleID:   "role_1",
			},
		})
	}))
	defer ts.Close()

	client := NewAuthClient(ts.URL)
	res, err := client.AssignUserRole(context.Background(), "Bearer token", "usr_1", "role_1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.UserID != "usr_1" || res.RoleID != "role_1" {
		t.Errorf("unexpected response: %+v", res)
	}
}

func TestAuthClient_GetUserRole(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/auth/users/usr_1/role" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": UserRoleResponse{
				UserID:   "usr_1",
				RoleName: "admin",
			},
		})
	}))
	defer ts.Close()

	client := NewAuthClient(ts.URL)
	res, err := client.GetUserRole(context.Background(), "Bearer token", "usr_1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.RoleName != "admin" {
		t.Errorf("unexpected role name: %s", res.RoleName)
	}
}

func TestAuthClient_CreateRole(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/roles" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": Role{
				ID:   "role_new",
				Name: "editor",
			},
		})
	}))
	defer ts.Close()

	client := NewAuthClient(ts.URL)
	res, err := client.CreateRole(context.Background(), "Bearer token", CreateRoleInput{Name: "editor"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Name != "editor" {
		t.Errorf("unexpected role name: %s", res.Name)
	}
}

func TestAuthClient_ListRoles(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/auth/roles" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": []Role{
				{ID: "role_1", Name: "admin"},
			},
		})
	}))
	defer ts.Close()

	client := NewAuthClient(ts.URL)
	roles, err := client.ListRoles(context.Background(), "Bearer token")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(roles) != 1 || roles[0].Name != "admin" {
		t.Errorf("unexpected roles list: %+v", roles)
	}
}

func TestAuthClient_ListPermissions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/auth/permissions" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": []PermissionCatalogItem{
				{ID: "perm_1", Name: "users:read", Service: "user-service"},
			},
		})
	}))
	defer ts.Close()

	client := NewAuthClient(ts.URL)
	perms, err := client.ListPermissions(context.Background(), "Bearer token")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(perms) != 1 || perms[0].Name != "users:read" {
		t.Errorf("unexpected permissions catalog: %+v", perms)
	}
}
