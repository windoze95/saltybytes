package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// toolAnnotations unmarshals a JSON-RPC tools/list body and returns each tool's
// annotations keyed by tool name.
func toolAnnotations(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v\nbody: %s", err, body)
	}
	out := make(map[string]map[string]any)
	for _, tool := range parsed.Result.Tools {
		out[tool.Name] = tool.Annotations
	}
	return out
}

func TestFixReadOnlyHintJSON_BackfillsMissing(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
		`{"name":"save_recipe","annotations":{"destructiveHint":false,"openWorldHint":true}},` +
		`{"name":"get_recipe","annotations":{"readOnlyHint":true,"destructiveHint":false,"openWorldHint":false}}` +
		`]}}`

	out, changed := fixReadOnlyHintJSON([]byte(in))
	if !changed {
		t.Fatal("expected the body to be changed")
	}
	ann := toolAnnotations(t, out)

	save := ann["save_recipe"]
	if v, ok := save["readOnlyHint"].(bool); !ok || v != false {
		t.Fatalf("save_recipe readOnlyHint = %v (%T), want false", save["readOnlyHint"], save["readOnlyHint"])
	}
	// Its other annotations must survive intact.
	if v, ok := save["destructiveHint"].(bool); !ok || v != false {
		t.Fatalf("save_recipe destructiveHint = %v, want false", save["destructiveHint"])
	}
	if v, ok := save["openWorldHint"].(bool); !ok || v != true {
		t.Fatalf("save_recipe openWorldHint = %v, want true", save["openWorldHint"])
	}
	// A tool that already declares readOnlyHint keeps its value.
	if v, ok := ann["get_recipe"]["readOnlyHint"].(bool); !ok || v != true {
		t.Fatalf("get_recipe readOnlyHint = %v, want true", ann["get_recipe"]["readOnlyHint"])
	}
}

func TestFixReadOnlyHintJSON_NoChangeWhenComplete(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
		`{"name":"get_recipe","annotations":{"readOnlyHint":true,"destructiveHint":false,"openWorldHint":false}}` +
		`]}}`
	if _, changed := fixReadOnlyHintJSON([]byte(in)); changed {
		t.Fatal("expected no change when readOnlyHint already present")
	}
}

func TestFixReadOnlyHintJSON_MalformedUntouched(t *testing.T) {
	in := []byte(`not json at all`)
	out, changed := fixReadOnlyHintJSON(in)
	if changed || string(out) != string(in) {
		t.Fatal("malformed body must be returned unchanged")
	}
}

func TestFixToolsListBody_SSE(t *testing.T) {
	body := "event: message\n" +
		`data: {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"save_recipe","annotations":{"openWorldHint":true}}]}}` +
		"\n\n"
	out := fixToolsListBody([]byte(body))
	if !strings.Contains(string(out), `"readOnlyHint":false`) {
		t.Fatalf("expected readOnlyHint backfilled in SSE data line, got: %s", out)
	}
	if !strings.HasPrefix(string(out), "event: message\n") {
		t.Fatalf("SSE framing not preserved: %s", out)
	}
}

func TestEnsureReadOnlyHint_Middleware(t *testing.T) {
	// Stub MCP handler that returns a tools/list response missing readOnlyHint.
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[`+
			`{"name":"save_recipe","annotations":{"destructiveHint":false,"openWorldHint":true}}`+
			`]}}`)
	})
	h := ensureReadOnlyHint(stub)

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	ann := toolAnnotations(t, rec.Body.Bytes())
	if v, ok := ann["save_recipe"]["readOnlyHint"].(bool); !ok || v != false {
		t.Fatalf("middleware did not backfill readOnlyHint:false, got: %s", rec.Body.Bytes())
	}
}

func TestEnsureReadOnlyHint_PassesThroughNonToolsList(t *testing.T) {
	const raw = `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, raw)
	})
	h := ensureReadOnlyHint(stub)

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"save_recipe"}}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Body.String() != raw {
		t.Fatalf("tools/call body must pass through untouched, got: %s", rec.Body.String())
	}
}
