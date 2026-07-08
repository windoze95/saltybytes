package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// ensureReadOnlyHint guarantees every tool in a tools/list response carries an
// explicit readOnlyHint. The Go MCP SDK marshals ToolAnnotations.ReadOnlyHint
// with `omitempty`, so a write tool's correct value (readOnlyHint:false) is
// dropped from the wire. OpenAI's ChatGPT Apps submission, however, requires
// readOnlyHint, openWorldHint, and destructiveHint to be present (true or
// false) on every tool. This shim re-adds the spec-default readOnlyHint:false
// wherever a tool's annotations object omits it (i.e. our save_recipe tool).
//
// Only tools/list responses are buffered and rewritten; every other request —
// including streaming tool calls — passes through untouched so streaming is
// preserved.
func ensureReadOnlyHint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		// Peek the request body to see if this is a tools/list call, then
		// restore it for the wrapped handler.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil || !bytes.Contains(body, []byte(`"tools/list"`)) {
			next.ServeHTTP(w, r)
			return
		}

		rec := &bufferingWriter{header: make(http.Header), body: &bytes.Buffer{}}
		next.ServeHTTP(rec, r)
		out := fixToolsListBody(rec.body.Bytes())

		h := w.Header()
		for k, v := range rec.header {
			h[k] = v
		}
		h.Set("Content-Length", strconv.Itoa(len(out)))
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(out)
	})
}

// bufferingWriter captures a handler's response so it can be rewritten before
// being flushed to the real client.
type bufferingWriter struct {
	header http.Header
	status int
	body   *bytes.Buffer
}

func (b *bufferingWriter) Header() http.Header         { return b.header }
func (b *bufferingWriter) WriteHeader(code int)        { b.status = code }
func (b *bufferingWriter) Write(p []byte) (int, error) { return b.body.Write(p) }

// fixToolsListBody rewrites a tools/list response body (either a plain JSON
// response or an SSE-framed one) to backfill missing readOnlyHint annotations.
func fixToolsListBody(body []byte) []byte {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if fixed, ok := fixReadOnlyHintJSON(body); ok {
			return fixed
		}
		return body
	}
	// SSE framing (text/event-stream): rewrite each data: line's JSON payload.
	if bytes.Contains(body, []byte("data:")) {
		lines := bytes.Split(body, []byte("\n"))
		changed := false
		for i, ln := range lines {
			t := bytes.TrimLeft(ln, " \t")
			if !bytes.HasPrefix(t, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(t[len("data:"):])
			if fixed, ok := fixReadOnlyHintJSON(payload); ok {
				lines[i] = append([]byte("data: "), fixed...)
				changed = true
			}
		}
		if changed {
			return bytes.Join(lines, []byte("\n"))
		}
	}
	return body
}

// fixReadOnlyHintJSON parses a single JSON-RPC response object and, for every
// tool in result.tools that has an annotations object without a readOnlyHint
// key, adds readOnlyHint:false. It returns the (possibly rewritten) payload and
// whether any change was made. On any parse failure it returns the input
// unchanged so a malformed body is never corrupted.
func fixReadOnlyHintJSON(payload []byte) ([]byte, bool) {
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return payload, false
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(msg["result"], &result); err != nil {
		return payload, false
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(result["tools"], &tools); err != nil {
		return payload, false
	}

	changed := false
	for _, tool := range tools {
		annRaw, ok := tool["annotations"]
		if !ok {
			continue
		}
		var ann map[string]json.RawMessage
		if err := json.Unmarshal(annRaw, &ann); err != nil {
			continue
		}
		if _, has := ann["readOnlyHint"]; has {
			continue
		}
		ann["readOnlyHint"] = json.RawMessage("false")
		newAnn, err := json.Marshal(ann)
		if err != nil {
			continue
		}
		tool["annotations"] = newAnn
		changed = true
	}
	if !changed {
		return payload, false
	}

	newTools, err := json.Marshal(tools)
	if err != nil {
		return payload, false
	}
	result["tools"] = newTools
	newResult, err := json.Marshal(result)
	if err != nil {
		return payload, false
	}
	msg["result"] = newResult
	out, err := json.Marshal(msg)
	if err != nil {
		return payload, false
	}
	return out, true
}
