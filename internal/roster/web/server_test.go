package web

import (
	"encoding/json"
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
const fixture = `version = 3

[access.zai]
agents = ["pi"]
budget = "lean"

[access.codex]
agents  = ["codex"]
trusted = true

[route."zai/glm-5.3"]
high = "strong"

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"
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
	// The policy itself is not serialized; its routes and role
	// requirements are, because the editor renders from them.
	if len(workspace.Routes) != 1 || workspace.Routes[0] != (roster.Route{Access: "zai", Model: "glm-5.3", Eff: "high", Class: "strong"}) {
		t.Fatalf("routes = %v, want the fixture route", workspace.Routes)
	}
	if workspace.RoleRequirements["executor"] != "standard" {
		t.Fatalf("role requirements = %v, want the executor row", workspace.RoleRequirements)
	}
}

func TestOperationWritesFile(t *testing.T) {
	server, path := testServer(t)
	status, body := postOp(t, server, `{"op":"SetAccessBudget","name":"zai","level":"prefer"}`)
	if status != http.StatusOK || !body.OK || body.Workspace == nil {
		t.Fatalf("POST /api/op = (%d, %+v), want 200 with a workspace", status, body)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `budget  = "prefer"`) && !strings.Contains(string(data), `budget = "prefer"`) {
		t.Fatalf("the operation did not write the file:\n%s", data)
	}
	// The returned workspace carries the change too.
	found := false
	for _, access := range body.Workspace.Accesses {
		if access.Access == "zai" && access.Budget == "prefer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the returned workspace lost the change: %+v", body.Workspace.Accesses)
	}
}

func TestOperationRefusal(t *testing.T) {
	server, path := testServer(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	status, body := postOp(t, server, `{"op":"SetAccessBudget","name":"nope","level":"lean"}`)
	if status != http.StatusBadRequest || body.OK || body.Error == "" {
		t.Fatalf("POST /api/op = (%d, %+v), want 400 with an error", status, body)
	}
	if !strings.Contains(body.Error, "unknown access") {
		t.Fatalf("error = %q, want the validation message", body.Error)
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

func TestOperationSetRoleValidates(t *testing.T) {
	server, path := testServer(t)
	// A role requirement the parser refuses leaves the file as it was.
	status, body := postOp(t, server, `{"op":"SetRole","role":"executor","requirement":"mighty"}`)
	if status != http.StatusBadRequest || body.Error == "" {
		t.Fatalf("POST /api/op = (%d, %+v), want 400 with an error", status, body)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "mighty") {
		t.Fatalf("the refused requirement was written:\n%s", data)
	}
	status, body = postOp(t, server, `{"op":"SetRole","role":"executor","requirement":"strong"}`)
	if status != http.StatusOK || !body.OK {
		t.Fatalf("POST /api/op = (%d, %+v), want 200", status, body)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), `executor   = "strong"`) && !strings.Contains(string(data), `executor = "strong"`) {
		t.Fatalf("the role change was not written:\n%s", data)
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
}
