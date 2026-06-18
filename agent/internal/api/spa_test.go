package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFakeWebUIDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa-root</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log('app')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	return dir
}

func TestSPAFileServer_ServesRealFile(t *testing.T) {
	dir := newFakeWebUIDir(t)
	h := spaFileServer(dir)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if rr.Body.String() != "console.log('app')" {
		t.Errorf("expected real asset content, got %q", rr.Body.String())
	}
}

func TestSPAFileServer_FallsBackToIndexForClientRoute(t *testing.T) {
	dir := newFakeWebUIDir(t)
	h := spaFileServer(dir)

	for _, path := range []string{"/settings", "/analytics", "/incidents/abc123/trace", "/"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("[%s] want 200, got %d", path, rr.Code)
		}
		if rr.Body.String() != "<html>spa-root</html>" {
			t.Errorf("[%s] expected index.html fallback, got %q", path, rr.Body.String())
		}
	}
}

func TestSPAFileServer_DoesNotEscapeRoot(t *testing.T) {
	dir := newFakeWebUIDir(t)
	h := spaFileServer(dir)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/../../../../etc/passwd", nil))
	// net/http's ServeFile rejects any request path containing ".." outright
	// (400), independent of our own filepath.Clean/Join containment — either
	// way, nothing outside dir is ever read or served.
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (http.ServeFile's built-in .. rejection), got %d: %s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "root:") {
		t.Error("response must never contain /etc/passwd content")
	}
}
