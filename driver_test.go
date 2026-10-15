package gofastmeili

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhoudm1743/go-fast-framework/contracts"
)

// recordedRequest 记录一次收到的请求。
type recordedRequest struct {
	method string
	path   string // path + query
	header http.Header
	body   []byte
}

// fakeMeili 假 Meilisearch 服务器：按路由返回预设响应并记录请求。
type fakeMeili struct {
	mu         sync.Mutex
	records    []recordedRequest
	status     int // 写操作（及错误场景）返回的状态码，0 表示 202
	searchResp any // /search 的响应体，nil 时用默认值
}

func (f *fakeMeili) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		f.mu.Lock()
		f.records = append(f.records, recordedRequest{r.Method, r.URL.RequestURI(), r.Header.Clone(), body})
		f.mu.Unlock()

		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/search") {
			resp := f.searchResp
			if resp == nil {
				resp = map[string]any{
					"hits":               []map[string]any{{"id": 1, "title": "hello"}},
					"estimatedTotalHits": 1,
					"processingTimeMs":   3,
				}
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		status := f.status
		if status == 0 {
			status = http.StatusAccepted
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 1})
	}
}

func (f *fakeMeili) snapshot() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.records))
	copy(out, f.records)
	return out
}

func newTestDriver(t *testing.T, srv *httptest.Server, cfg DriverConfig) *Driver {
	t.Helper()
	if cfg.Host == "" {
		cfg.Host = srv.URL
	}
	if cfg.PrimaryKey == "" {
		cfg.PrimaryKey = "id"
	}
	d, err := NewDriver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNewDriver_EmptyHostError(t *testing.T) {
	if _, err := NewDriver(DriverConfig{}); err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestUpsert_MapDocuments(t *testing.T) {
	f := &fakeMeili{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	d := newTestDriver(t, srv, DriverConfig{APIKey: "secret"})
	err := d.Index("posts").Upsert(
		map[string]any{"id": 1, "title": "hello"},
		map[string]any{"id": 2, "title": "world"},
	)
	if err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	reqs := f.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodPut {
		t.Fatalf("method = %s, want PUT", r.method)
	}
	if !strings.HasPrefix(r.path, "/indexes/posts/documents") {
		t.Fatalf("path = %q", r.path)
	}
	if !strings.Contains(r.path, "primaryKey=id") {
		t.Fatalf("path missing primaryKey: %q", r.path)
	}
	if got := r.header.Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("authorization = %q", got)
	}
	var docs []map[string]any
	if err := json.Unmarshal(r.body, &docs); err != nil {
		t.Fatalf("body is not a JSON array: %v", err)
	}
	if len(docs) != 2 || docs[0]["title"] != "hello" || docs[1]["title"] != "world" {
		t.Fatalf("body = %s", r.body)
	}
}

type post struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func TestUpsert_StructDocuments(t *testing.T) {
	f := &fakeMeili{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	d := newTestDriver(t, srv, DriverConfig{})
	if err := d.Index("posts").Upsert(post{ID: 1, Title: "rust"}, post{ID: 2, Title: "go"}); err != nil {
		t.Fatalf("Upsert() error: %v", err)
	}

	var docs []map[string]any
	if err := json.Unmarshal(f.snapshot()[0].body, &docs); err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0]["id"] != float64(1) || docs[0]["title"] != "rust" {
		t.Fatalf("body = %s", f.snapshot()[0].body)
	}
}

func TestUpsert_EmptyNoRequest(t *testing.T) {
	f := &fakeMeili{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	d := newTestDriver(t, srv, DriverConfig{})
	if err := d.Index("posts").Upsert(); err != nil {
		t.Fatal(err)
	}
	if n := len(f.snapshot()); n != 0 {
		t.Fatalf("empty Upsert must not send request, got %d", n)
	}
}

func TestSearch_FullOptions(t *testing.T) {
	f := &fakeMeili{searchResp: map[string]any{
		"hits":               []map[string]any{{"id": float64(7), "title": "rust"}},
		"estimatedTotalHits": 42,
		"processingTimeMs":   9,
	}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	d := newTestDriver(t, srv, DriverConfig{})
	res, err := d.Index("posts").Search("rust",
		contracts.WithSearchLimit(10),
		contracts.WithSearchOffset(5),
		contracts.WithSearchFilter("lang = rust"),
		contracts.WithSearchSort("price:asc", "id:desc"),
	)
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if res.Total != 42 {
		t.Fatalf("Total = %d, want 42", res.Total)
	}
	if res.Took != 9*time.Millisecond {
		t.Fatalf("Took = %v, want 9ms", res.Took)
	}
	if len(res.Hits) != 1 || res.Hits[0]["id"] != float64(7) || res.Hits[0]["title"] != "rust" {
		t.Fatalf("Hits = %+v", res.Hits)
	}

	r := f.snapshot()[0]
	if r.method != http.MethodPost {
		t.Fatalf("method = %s, want POST", r.method)
	}
	if !strings.HasSuffix(r.path, "/indexes/posts/search") {
		t.Fatalf("path = %q", r.path)
	}
	var body map[string]any
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["q"] != "rust" {
		t.Fatalf("q = %v", body["q"])
	}
	if body["limit"] != float64(10) || body["offset"] != float64(5) {
		t.Fatalf("limit/offset = %v/%v", body["limit"], body["offset"])
	}
	if body["filter"] != "lang = rust" {
		t.Fatalf("filter = %v", body["filter"])
	}
	sort, _ := body["sort"].([]any)
	if len(sort) != 2 || sort[0] != "price:asc" || sort[1] != "id:desc" {
		t.Fatalf("sort = %v", body["sort"])
	}
}

func TestSearch_NoOptionsOmitsFields(t *testing.T) {
	f := &fakeMeili{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	d := newTestDriver(t, srv, DriverConfig{})
	if _, err := d.Index("posts").Search("x"); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(f.snapshot()[0].body, &body); err != nil {
		t.Fatal(err)
	}
	if body["q"] != "x" {
		t.Fatalf("q = %v", body["q"])
	}
	for _, k := range []string{"limit", "offset", "filter", "sort"} {
		if _, ok := body[k]; ok {
			t.Fatalf("field %q should be omitted", k)
		}
	}
}

func TestSearch_NilHitsNormalized(t *testing.T) {
	f := &fakeMeili{searchResp: map[string]any{"hits": nil, "estimatedTotalHits": 0, "processingTimeMs": 0}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res, err := newTestDriver(t, srv, DriverConfig{}).Index("posts").Search("x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Hits == nil {
		t.Fatal("nil hits must be normalized to empty slice")
	}
}

func TestDelete(t *testing.T) {
	f := &fakeMeili{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	if err := newTestDriver(t, srv, DriverConfig{}).Index("posts").Delete(1, "two"); err != nil {
		t.Fatal(err)
	}
	r := f.snapshot()[0]
	if r.method != http.MethodPost {
		t.Fatalf("method = %s, want POST", r.method)
	}
	if !strings.HasSuffix(r.path, "/indexes/posts/documents/delete-batch") {
		t.Fatalf("path = %q", r.path)
	}
	var ids []any
	if err := json.Unmarshal(r.body, &ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != float64(1) || ids[1] != "two" {
		t.Fatalf("body = %s", r.body)
	}
}

func TestClear(t *testing.T) {
	f := &fakeMeili{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	if err := newTestDriver(t, srv, DriverConfig{}).Index("posts").Clear(); err != nil {
		t.Fatal(err)
	}
	r := f.snapshot()[0]
	if r.method != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", r.method)
	}
	if !strings.HasSuffix(r.path, "/indexes/posts/documents") {
		t.Fatalf("path = %q", r.path)
	}
	if got := r.header.Get("Authorization"); got != "" {
		t.Fatalf("no api key configured, authorization should be empty, got %q", got)
	}
}

func TestError_Non2xx(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		f := &fakeMeili{status: status}
		srv := httptest.NewServer(f.handler())
		defer srv.Close()

		err := newTestDriver(t, srv, DriverConfig{}).Index("posts").Upsert(map[string]any{"id": 1})
		if err == nil {
			t.Fatalf("status %d: expected error", status)
		}
		msg := err.Error()
		if !strings.Contains(msg, strconv.Itoa(status)) {
			t.Fatalf("status %d: error should contain status code: %v", status, err)
		}
		if !strings.Contains(msg, "taskUid") {
			t.Fatalf("status %d: error should contain body summary: %v", status, err)
		}
	}
}
