//go:build rag_integration

package ragclient_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ai-bud/services/api/internal/access/adapters/demo"
	"ai-bud/services/api/internal/access/adapters/memory"
	"ai-bud/services/api/internal/access/application"
	"ai-bud/services/api/internal/access/domain"
	"ai-bud/services/api/internal/ragclient"
	"ai-bud/services/api/internal/transport/httpapi"
)

type tickets struct{ calls int }

func (g *tickets) Create(context.Context, string, string, domain.Parameters) (domain.Ticket, error) {
	g.calls++
	return domain.Ticket{ID: "ticket", Status: domain.Pending}, nil
}
func (g *tickets) Lookup(context.Context, string) (domain.Ticket, error) {
	return domain.Ticket{}, domain.ErrNotFound
}

func TestGoRESTToPythonGRPC(t *testing.T) {
	python := os.Getenv("RAG_TEST_PYTHON")
	if python == "" {
		t.Fatal("RAG_TEST_PYTHON must point to Python with rag[server,dev] installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, python, "-u", "-c", `
import sys
from rag.application import RagApplication
from rag.storage import SQLiteStore
from rag.ingestion.indexing import DEMO_MANIFEST, index_manifest
from rag.transport.grpc_server import create_server
store = SQLiteStore(sys.argv[1])
index_manifest(DEMO_MANIFEST, store)
server, executor, port = create_server(RagApplication(store), "integration-token", "127.0.0.1:0")
server.start()
print(port, flush=True)
server.wait_for_termination()
`, t.TempDir()+"/index.db")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	var port string
	select {
	case port = <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("Python startup timed out")
	}
	if port == "" {
		t.Fatal("Python did not start")
	}
	client, err := ragclient.New("127.0.0.1:"+port, "integration-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	g := &tickets{}
	svc := application.New(memory.New(), g, demo.Policy{})
	h := httpapi.New(svc, httpapi.DemoAuth{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.WithRAG(client))
	call := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "integration-proposal")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("/api/rag/answers", `{"question":"Jira 403","mode":"offline"}`, "demo-intern-platform")
	var result ragclient.Answer
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Status != "sources_found" || result.Action == nil || *result.Action != "preview_jira_access" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if strings.Contains(w.Body.String(), "source_path") || g.calls != 0 {
		t.Fatal("source metadata leaked or ticket created")
	}
	for _, query := range []string{"MENTOR-482", "SEARCH-730"} {
		w = call("/api/rag/search", `{"query":"`+query+`"}`, "demo-intern-platform")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"sources":[]`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = call("/api/rag/search", `{"query":"MENTOR-482"}`, "demo-mentor-platform")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "MENTOR-482") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("/api/access-requests", `{"resource":"jira:BUD","access":"read","reason":"First task"}`, "demo-intern-platform")
	var proposal struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(w.Body.Bytes(), &proposal) != nil || w.Code != 201 || g.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("/api/access-requests/"+proposal.ID+"/confirm", `{"version":1}`, "demo-intern-platform")
	if w.Code != 200 || g.calls != 1 {
		t.Fatal(w.Code, w.Body.String(), g.calls)
	}
	w = call("/api/access-requests", `{"resource":"jira:BUD","access":"read","reason":"First task"}`, "demo-intern-search")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
