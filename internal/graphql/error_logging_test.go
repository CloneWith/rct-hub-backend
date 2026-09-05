package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// safeBuffer 在并发写入时不会触发 race detector（仅测试本地使用）。
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newCapturingLogger 返回一个把每条日志写到 buf 的 zap.Logger（JSON encoder）。
// level 设为 Debug 以便我们看到 Error 级别。
func newCapturingLogger(buf *safeBuffer) *zap.Logger {
	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	})
	core := zapcore.NewCore(enc, zapcore.AddSync(buf), zapcore.DebugLevel)
	return zap.New(core)
}

// TestErrorPresenterLogsResolverError 验证：resolver 报错时日志会被写入，
// 且日志字段包含 path / message / err。
func TestErrorPresenterLogsResolverError(t *testing.T) {
	resolver := NewResolver(nil).WithLogger(newCapturingLogger(&safeBuffer{}))

	// 构造一个会抛错的查询：调用 match(id:"") 让 resolver 走 bson 解析
	// 失败分支并返回错误。
	query := `{"query":"{ match(id:\"\") { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv := NewHandler(resolver)
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected errors in response, got none")
	}
}

// TestErrorPresenterDirectInvocation 直接调用 loggingErrorPresenter，
// 验证日志字段与 DefaultErrorPresenter 的产物一致（path/message/code）。
func TestErrorPresenterDirectInvocation(t *testing.T) {
	buf := &safeBuffer{}
	log := newCapturingLogger(buf)
	resolver := NewResolver(nil).WithLogger(log)

	presenter := loggingErrorPresenter(resolver)

	// 用一个假的 field path 模拟 resolver 上下文。
	fieldName := "match"
	pathCtx := graphql.WithPathContext(context.Background(), graphql.NewPathWithField(fieldName))

	err := fmt.Errorf("boom")
	gqlErr := presenter(pathCtx, err)
	if gqlErr == nil {
		t.Fatal("expected non-nil gqlerror")
	}
	if gqlErr.Message != "boom" {
		t.Fatalf("expected message 'boom', got %q", gqlErr.Message)
	}
	if !contains(gqlErr.Path, ast.PathName("match")) {
		t.Fatalf("expected path to include 'match', got %v", gqlErr.Path)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected log output, got empty")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &entry); err != nil {
		t.Fatalf("log entry is not JSON: %v\n%s", err, out)
	}
	if entry["msg"] != "graphql resolver returned error" {
		t.Fatalf("unexpected msg: %v", entry["msg"])
	}
	if entry["message"] != "boom" {
		t.Fatalf("expected message 'boom' in log, got %v", entry["message"])
	}
}

// TestErrorPresenterIncludesExtensionsCode 验证 Extensions["code"] 会被
// 复制到日志的 code 字段（command 路径常见 MatchErrorCode）。
func TestErrorPresenterIncludesExtensionsCode(t *testing.T) {
	buf := &safeBuffer{}
	log := newCapturingLogger(buf)
	resolver := NewResolver(nil).WithLogger(log)
	presenter := loggingErrorPresenter(resolver)

	pathCtx := graphql.WithPathContext(context.Background(), graphql.NewPathWithField("startMatch"))

	gqlErrInput := &gqlerror.Error{
		Message: "match command failed",
		Path:    graphql.GetPath(pathCtx),
		Extensions: map[string]any{
			"code": "AUTH_REQUIRED",
		},
	}
	out := presenter(pathCtx, gqlErrInput)
	if out == nil {
		t.Fatal("expected presenter to return non-nil")
	}

	entries := parseLogEntries(t, buf.String())
	if len(entries) == 0 {
		t.Fatal("expected log entry")
	}
	if entries[0]["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected code=AUTH_REQUIRED in log, got %v", entries[0]["code"])
	}
}

// TestErrorPresenterWithoutLogger 验证 resolver 上未挂 logger 时，
// 行为退化为 DefaultErrorPresenter，不写日志、不 panic。
func TestErrorPresenterWithoutLogger(t *testing.T) {
	resolver := NewResolver(nil) // 不调 WithLogger
	presenter := loggingErrorPresenter(resolver)
	gqlErr := presenter(context.Background(), fmt.Errorf("quiet"))
	if gqlErr == nil {
		t.Fatal("expected presenter to return non-nil")
	}
	if gqlErr.Message != "quiet" {
		t.Fatalf("expected message 'quiet', got %q", gqlErr.Message)
	}
}

// TestPathString 验证 pathString 对空路径、混合索引路径都正确。
func TestPathString(t *testing.T) {
	cases := []struct {
		name string
		in   ast.Path
		want []string
	}{
		{"nil", nil, nil},
		{"empty", ast.Path{}, nil},
		{"name only", ast.Path{ast.PathName("rooms")}, []string{"rooms"}},
		{"nested name", ast.Path{ast.PathName("rooms"), ast.PathName("nodes")}, []string{"rooms", "nodes"}},
		{"with index", ast.Path{ast.PathName("items"), ast.PathIndex(2)}, []string{"items", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathString(tc.in)
			if !equal(got, tc.want) {
				t.Fatalf("pathString(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIntToString 验证负数 / 零 / 正数都能转。
func TestIntToString(t *testing.T) {
	cases := map[int]string{
		0:  "0",
		1:  "1",
		42: "42",
		-7: "-7",
	}
	for in, want := range cases {
		if got := intToString(in); got != want {
			t.Errorf("intToString(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestToString 验证 toString 对 string / []byte / 其它类型的行为。
func TestToString(t *testing.T) {
	if got := toString("abc"); got != "abc" {
		t.Errorf("toString(string) = %q", got)
	}
	if got := toString([]byte("xyz")); got != "xyz" {
		t.Errorf("toString([]byte) = %q", got)
	}
	if got := toString(123); got != "" {
		t.Errorf("toString(int) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func contains(path ast.Path, want ast.PathElement) bool {
	for _, p := range path {
		if p == want {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func parseLogEntries(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid JSON in log line: %v\n%s", err, line)
		}
		out = append(out, entry)
	}
	return out
}
