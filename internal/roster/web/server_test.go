package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/roster"
)

// The workspace fixture: the same shape the roster tests use, small
// enough to read by eye.
const fixture = `version = 4

[access.zai]
agents = ["pi"]

[access.zai.roles]
heavy = "glm-5.3@high"

[access.codex]
agents  = ["codex"]

[access.codex.roles]
acceptance = "gpt-6.1-sol@medium"
replan     = "gpt-6.1-sol@medium"
`

// testServer starts the handler against a temp roster and returns it with
// the roster path.
func testServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(path).Handler())
	t.Cleanup(server.Close)
	return server, path
}

// getWorkspace reads the workspace JSON of GET /api/workspace.
func getWorkspace(t *testing.T, server *httptest.Server) Response {
	t.Helper()
	resp, err := http.Get(server.URL + "/api/workspace")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/workspace = %d, want 200", resp.StatusCode)
	}
	var body Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Workspace == nil {
		t.Fatalf("GET /api/workspace = %+v, want a workspace", body)
	}
	return body
}

// postOp sends one operation and returns the status and the body.
func postOp(t *testing.T, server *httptest.Server, op string) (int, Response) {
	t.Helper()
	resp, err := http.Post(server.URL+"/api/op", "application/json", strings.NewReader(op))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestWorkspaceEndpoint(t *testing.T) {
	server, _ := testServer(t)
	body := getWorkspace(t, server)
	workspace := body.Workspace
	if got := len(workspace.Accesses); got != 2 {
		t.Fatalf("accesses = %d, want 2", got)
	}
	// The policy itself is not serialized; the rows carry the cells the
	// table renders, and the order and the role names come along.
	zai := workspace.Accesses[0]
	if zai.Access != "zai" || zai.Cells["heavy"] != "glm-5.3@high" {
		t.Fatalf("zai row = %+v, want the fixture cell", zai)
	}
	if strings.Join(workspace.Order, ",") != "zai,codex" || len(workspace.RoleNames) != 6 {
		t.Fatalf("order = %v, roles = %v", workspace.Order, workspace.RoleNames)
	}
}

func TestOperationWritesFile(t *testing.T) {
	server, path := testServer(t)
	status, body := postOp(t, server, `{"op":"SetCell","access":"codex","role":"executor","value":"gpt-6-luna@high"}`)
	if status != http.StatusOK || !body.OK || body.Workspace == nil {
		t.Fatalf("POST /api/op = (%d, %+v), want 200 with a workspace", status, body)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `executor   = "gpt-6-luna@high"`) {
		t.Fatalf("the operation did not write the file:\n%s", data)
	}
	if body.Workspace.Accesses[1].Cells["executor"] != "gpt-6-luna@high" {
		t.Fatalf("the returned workspace lost the change: %+v", body.Workspace.Accesses)
	}
}

func TestSetAccessKind(t *testing.T) {
	server, path := testServer(t)
	if status, body := postOp(t, server, `{"op":"SetAccessKind","name":"zai","kind":"local"}`); status != http.StatusOK || body.Workspace.Accesses[0].Kind != "local" {
		t.Fatalf("SetAccessKind local = (%d, %+v), want 200 with the kind", status, body)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `kind = "local"`) {
		t.Fatalf("the kind was not written:\n%s", data)
	}
	if status, body := postOp(t, server, `{"op":"SetAccessKind","name":"zai","kind":"cloud"}`); status != http.StatusBadRequest || !strings.Contains(body.Error, "kind must be") {
		t.Fatalf("SetAccessKind cloud = (%d, %+v), want 400", status, body)
	}
	if status, _ := postOp(t, server, `{"op":"SetAccessKind","name":"zai","kind":""}`); status != http.StatusOK {
		t.Fatalf("SetAccessKind empty = %d, want 200", status)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "kind") {
		t.Fatalf("the empty kind left the key:\n%s", data)
	}
}

func TestOperationRefusal(t *testing.T) {
	server, path := testServer(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	status, body := postOp(t, server, `{"op":"SetCell","access":"nope","role":"executor","value":"m"}`)
	if status != http.StatusBadRequest || body.OK || !strings.Contains(body.Error, "unknown access") {
		t.Fatalf("POST /api/op = (%d, %+v), want 400 with the validation message", status, body)
	}
	if status, body := postOp(t, server, `{"op":"SetCell","access":"zai","role":"executor","value":"glm@"}`); status != http.StatusBadRequest || !strings.Contains(body.Error, "effort") {
		t.Fatalf("POST broken cell = (%d, %+v), want 400", status, body)
	}
	if status, body := postOp(t, server, `{"op":"Bogus"}`); status != http.StatusBadRequest || !strings.Contains(body.Error, "unknown op") {
		t.Fatalf("POST bogus op = (%d, %+v), want 400 unknown op", status, body)
	}
	if status, body := postOp(t, server, `{broken`); status != http.StatusBadRequest || body.Error == "" {
		t.Fatalf("POST broken json = (%d, %+v), want 400", status, body)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("a refused operation rewrote the file:\n%s", after)
	}
}

func TestIndexPage(t *testing.T) {
	server, _ := testServer(t)
	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Таблица ролей", "app.js", "style.css"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("GET / lost %q", want)
		}
	}
}

// TestEditorScenario walks the editor's basic path end to end: a policy
// is created, overrides a cell, switches an access off, moves another up
// and becomes the one in force — with the files on disk carrying it.
func TestEditorScenario(t *testing.T) {
	installCatalog(t)
	server, path := testServer(t)
	variantsPath := roster.VariantsPath(path)
	for _, op := range []string{
		`{"op":"CreateVariant","name":"codex-first"}`,
		`{"op":"MoveVariantAccess","name":"codex-first","access":"codex","dir":"up"}`,
		`{"op":"SetVariantCell","name":"codex-first","access":"codex","role":"heavy","value":"gpt-6.1-sol@high"}`,
		`{"op":"SetVariantCell","name":"codex-first","access":"zai","role":"heavy","value":""}`,
		`{"op":"SetVariantOff","name":"codex-first","access":"zai","off":true}`,
		`{"op":"SetPreset","name":"codex-first"}`,
	} {
		if status, body := postOp(t, server, op); status != http.StatusOK || !body.OK {
			t.Fatalf("%s = (%d, %+v), want 200", op, status, body)
		}
	}
	body := getWorkspace(t, server)
	if body.Workspace.Preset != "codex-first" || strings.Join(body.Workspace.Order, ",") != "codex,zai" {
		t.Fatalf("preset = %q, order = %v", body.Workspace.Preset, body.Workspace.Order)
	}
	variants, err := os.ReadFile(variantsPath)
	if err != nil {
		t.Fatalf("the variants file was not created: %v", err)
	}
	for _, want := range []string{"[preset.codex-first]", `order = ["codex", "zai"]`, `off = ["zai"]`, "[preset.codex-first.roles.codex]", `heavy      = "gpt-6.1-sol@high"`, `heavy      = ""`} {
		if !strings.Contains(string(variants), want) {
			t.Fatalf("the variants file lacks %q:\n%s", want, variants)
		}
	}
	// The default table stays as it was.
	policy, _ := os.ReadFile(path)
	if strings.Contains(string(policy), "gpt-6.1-sol@high") {
		t.Fatalf("a policy edit reached roster.toml:\n%s", policy)
	}
}

// installCatalog puts the scenario catalog where LoadCatalog reads it.
func installCatalog(t *testing.T) {
	t.Helper()
	catalog, err := roster.LoadCatalog("../../roster/testdata/catalog-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := catalog.Save(filepath.Join(cache, "docflow", "roster", "catalog.json")); err != nil {
		t.Fatal(err)
	}
}
