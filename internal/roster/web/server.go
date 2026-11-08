// Package web serves the roster workspace and its operations over local
// HTTP: the browser editor (P059) reads GET /api/workspace and drives the
// workspace operations of the roster package through POST /api/op. The
// package imports the roster package and the standard library only.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"bsv-docflow/internal/roster"
)

//go:embed static
var static embed.FS

// Request is one operation of POST /api/op. The field names match the
// workspace operations: an op names the method, the rest are its
// arguments, and an argument the op does not take is ignored.
type Request struct {
	Op      string   `json:"op"`
	Name    string   `json:"name,omitempty"`
	Next    string   `json:"next,omitempty"`
	Access  string   `json:"access,omitempty"`
	Role    string   `json:"role,omitempty"`
	Value   string   `json:"value,omitempty"`
	Agents  []string `json:"agents,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	Check   bool     `json:"check,omitempty"`
	Off     bool     `json:"off,omitempty"`
	Inherit bool     `json:"inherit,omitempty"`
	Project bool     `json:"project,omitempty"`
	CopyOf  string   `json:"copy_of,omitempty"`
}

// Response is the JSON of every endpoint: the workspace after a
// successful operation, or the validation error of a refused one.
type Response struct {
	OK        bool              `json:"ok"`
	Workspace *roster.Workspace `json:"workspace,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// Server is the roster editor backend: the roster path the workspace is
// loaded from and the mutex that serializes the writing requests, so two
// concurrent POSTs never write roster.toml at once.
type Server struct {
	rosterPath string
	mu         sync.Mutex
}

// New returns a server for the roster at path ("" means the default
// roster).
func New(rosterPath string) *Server {
	return &Server{rosterPath: rosterPath}
}

// Handler returns the HTTP routes of the server: the editor pages, the
// workspace JSON and the operation endpoint.
func (s *Server) Handler() http.Handler {
	pages, err := fs.Sub(static, "static")
	if err != nil {
		panic("roster web: embedded static is broken: " + err.Error())
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(pages)))
	mux.HandleFunc("GET /api/workspace", s.workspace)
	mux.HandleFunc("POST /api/op", s.operation)
	return mux
}

// Run starts the server on 127.0.0.1 at addr ("" means a random port,
// a bare port is taken as one on 127.0.0.1), prints the URL and serves
// until SIGINT or SIGTERM.
func Run(addr, rosterPath string, stdout io.Writer) error {
	switch {
	case addr == "":
		addr = "127.0.0.1:0"
	case !hasPort(addr):
		addr = "127.0.0.1:" + addr
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("docflow roster web: %v", err)
	}
	fmt.Fprintf(stdout, "docflow roster web: http://%s — Ctrl-C stops the server\n", listener.Addr().String())
	server := &http.Server{Handler: New(rosterPath).Handler()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		return nil
	case err := <-served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("docflow roster web: %v", err)
		}
		return nil
	}
}

// hasPort reports whether an address already carries a port.
func hasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}

// workspace answers GET /api/workspace with the roster as the editor
// sees it: the policy, the variants, the catalog rows and the conditions.
func (s *Server) workspace(w http.ResponseWriter, r *http.Request) {
	loaded, err := roster.LoadWorkspace(s.rosterPath, roster.ProjectRoot())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, Response{OK: true, Workspace: loaded})
}

// operation answers POST /api/op: one workspace operation on a freshly
// loaded workspace, serialized with the mutex, and the reloaded
// workspace on success. A refused operation is 400 and changes nothing.
func (s *Server) operation(w http.ResponseWriter, r *http.Request) {
	var req Request
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: fmt.Sprintf("docflow roster web: bad request: %v", err)})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded, err := roster.LoadWorkspace(s.rosterPath, roster.ProjectRoot())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: err.Error()})
		return
	}
	if err := s.apply(loaded, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: err.Error()})
		return
	}
	reloaded, err := roster.LoadWorkspace(s.rosterPath, roster.ProjectRoot())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, Response{OK: true, Workspace: reloaded})
}

// apply dispatches one request to the workspace operation of the same
// name; the names are the operation surface P059.1.3 fixed.
func (s *Server) apply(workspace *roster.Workspace, req *Request) error {
	switch req.Op {
	case "AddAccess":
		return workspace.AddAccess(req.Name, req.Kind, req.Agents)
	case "RemoveAccess":
		return workspace.RemoveAccess(req.Name)
	case "SetAccessCheck":
		return workspace.SetAccessCheck(req.Name, req.Check)
	case "SetAccessKind":
		return workspace.SetAccessKind(req.Name, req.Kind)
	case "MoveAccess":
		return workspace.MoveAccess(req.Name, req.Dir)
	case "SetCell":
		return workspace.SetCell(req.Access, req.Role, req.Value)
	case "CreateVariant":
		return workspace.CreateVariant(req.Name, req.Project, req.CopyOf)
	case "RenameVariant":
		return workspace.RenameVariant(req.Name, req.Next)
	case "DeleteVariant":
		return workspace.DeleteVariant(req.Name)
	case "SetVariantOff":
		return workspace.SetVariantOff(req.Name, req.Access, req.Off)
	case "MoveVariantAccess":
		return workspace.MoveVariantAccess(req.Name, req.Access, req.Dir)
	case "SetVariantCell":
		return workspace.SetVariantCell(req.Name, req.Access, req.Role, req.Value, req.Inherit)
	case "SetPreset":
		if req.Name == "" {
			return workspace.ClearPreset()
		}
		return workspace.SetPreset(req.Name)
	default:
		return fmt.Errorf("docflow roster web: unknown op '%s'", req.Op)
	}
}

// writeJSON sends one response as JSON.
func writeJSON(w http.ResponseWriter, status int, body Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
