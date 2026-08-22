package richcontent

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMermaidFallbackAndReplacement(t *testing.T) {
	body := "before\n```mermaid\ngraph LR\n A-->B\n```\nafter"
	sources := MermaidSources(body)
	if len(sources) != 1 || sources[0] != "graph LR\n A-->B" {
		t.Fatalf("sources = %#v", sources)
	}
	if got := ReplaceMermaid(body, nil); got != body {
		t.Fatalf("fallback changed source: %q", got)
	}
	got := ReplaceMermaid(body, map[string]string{sources[0]: "A ──▶ B"})
	if !strings.Contains(got, "```text\nA ──▶ B\n```") || strings.Contains(got, "```mermaid") {
		t.Fatalf("replacement = %q", got)
	}
}

func TestRenderMermaidUsesOptionalCLI(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "termaid")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'A ──▶ B\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := RenderMermaidContext(context.Background(), "graph LR; A-->B", 40)
	if err != nil || got != "A ──▶ B" {
		t.Fatalf("render = %q, %v", got, err)
	}
}

func TestImageColorStaysOneRepresentativeColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{B: 255, A: 255})
	if got := imageColor(img); got != "#7f007f" {
		t.Fatalf("color = %q", got)
	}
}

func TestAvatarColorContextRejectsUntrustedURLs(t *testing.T) {
	for _, raw := range []string{
		"http://avatars.githubusercontent.com/u/1", // wrong scheme
		"https://evil.example.com/u/1",             // wrong host
		"://nope",                                  // unparseable
	} {
		if _, err := AvatarColorContext(context.Background(), raw); err == nil {
			t.Errorf("AvatarColorContext(%q) = nil error; want rejection before any network call", raw)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// routeAvatarHost reroutes requests for the allow-listed avatar host to a
// local httptest server. AvatarColorContext builds its http.Client with a nil
// Transport, so every request flows through http.DefaultTransport; swapping
// that package variable keeps the test off the network with no production seam.
func routeAvatarHost(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Hostname() != "avatars.githubusercontent.com" {
			return nil, fmt.Errorf("unexpected avatar request %s", req.URL)
		}
		if req.URL.Path == "/transport-error" {
			return nil, errors.New("avatar transport unreachable")
		}
		target, err := url.Parse(server.URL)
		if err != nil {
			return nil, err
		}
		rewritten := req.Clone(req.Context())
		rewritten.URL.Scheme, rewritten.URL.Host = target.Scheme, target.Host
		return original.RoundTrip(rewritten)
	})
	t.Cleanup(func() { http.DefaultTransport = original })
}

func TestAvatarColorContextFetchAndDecodeFallbacks(t *testing.T) {
	var solidHits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/http-failure":
			http.Error(w, "upstream broke", http.StatusInternalServerError)
		case "/redirect":
			http.Redirect(w, r, "https://evil.example.com/u/1", http.StatusFound)
		case "/garbage":
			_, _ = fmt.Fprint(w, "definitely not an image")
		case "/oversized":
			_, _ = w.Write(make([]byte, 5<<20+1))
		case "/solid":
			solidHits.Add(1)
			img := image.NewRGBA(image.Rect(0, 0, 2, 2))
			for y := 0; y < 2; y++ {
				for x := 0; x < 2; x++ {
					img.Set(x, y, color.RGBA{R: 255, A: 255})
				}
			}
			if err := png.Encode(w, img); err != nil {
				t.Errorf("encode avatar: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	routeAvatarHost(t, server)

	const base = "https://avatars.githubusercontent.com"
	for name, test := range map[string]struct {
		path    string
		wantErr string
	}{
		"transport failure":       {path: "/transport-error", wantErr: "avatar transport unreachable"},
		"http failure":            {path: "/http-failure", wantErr: "avatar HTTP 500"},
		"untrusted redirect":      {path: "/redirect", wantErr: "avatar redirect rejected"},
		"undecodable image":       {path: "/garbage", wantErr: "invalid avatar dimensions"},
		"oversized response body": {path: "/oversized", wantErr: "avatar exceeds 5 MiB"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := AvatarColorContext(context.Background(), base+test.path)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("AvatarColorContext(%s) error = %v, want %q", test.path, err, test.wantErr)
			}
		})
	}

	t.Run("success extracts and caches the color", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			got, err := AvatarColorContext(context.Background(), base+"/solid")
			if err != nil || got != "#ff0000" {
				t.Fatalf("call %d = %q, %v; want #ff0000", i+1, got, err)
			}
		}
		if hits := solidHits.Load(); hits != 1 {
			t.Fatalf("avatar fetched %d times; want the second call served from cache", hits)
		}
	})
}
