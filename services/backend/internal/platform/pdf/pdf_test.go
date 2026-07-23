package pdf

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestRenderHTML_Success(t *testing.T) {
	inputHTML := []byte("<html><body>hello</body></html>")
	wantPDF := []byte("%PDF-1.4 fake")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/forms/chromium/convert/html") {
			t.Errorf("path = %q, want suffix /forms/chromium/convert/html", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("Content-Type = %q, want multipart/form-data prefix", ct)
		}

		if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // bounded 1MB parse in a test server
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		file, _, err := r.FormFile("index.html")
		if err != nil {
			t.Fatalf("FormFile(index.html): %v", err)
		}
		defer func() { _ = file.Close() }()
		got, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("read file part: %v", err)
		}
		if !bytes.Equal(got, inputHTML) {
			t.Errorf("file part = %q, want %q", got, inputHTML)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(wantPDF)
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	pdf, err := c.RenderHTML(context.Background(), inputHTML)
	if err != nil {
		t.Fatalf("RenderHTML: unexpected error: %v", err)
	}
	if !bytes.Equal(pdf, wantPDF) {
		t.Errorf("pdf = %q, want %q", pdf, wantPDF)
	}
}

func TestRenderHTML_NonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client())
	pdf, err := c.RenderHTML(context.Background(), []byte("<html></html>"))
	if err == nil {
		t.Fatal("RenderHTML: expected error, got nil")
	}
	if pdf != nil {
		t.Errorf("pdf = %q, want nil", pdf)
	}
	if got := errs.KindOf(err); got != errs.KindUnavailable {
		t.Errorf("KindOf(err) = %v, want KindUnavailable", got)
	}
}
