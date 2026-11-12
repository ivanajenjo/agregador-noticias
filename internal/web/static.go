package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type staticFile struct {
	data []byte
	etag string
	ctyp string
}

func ctxTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func loadStatic() map[string]staticFile {
	out := map[string]staticFile{}
	sub := staticSub()
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		ct := mime.TypeByExtension(path.Ext(p))
		switch path.Ext(p) {
		case ".webmanifest":
			ct = "application/manifest+json"
		case ".js":
			ct = "text/javascript; charset=utf-8"
		case ".css":
			ct = "text/css; charset=utf-8"
		case ".svg":
			ct = "image/svg+xml"
		}
		out[p] = staticFile{data: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, ctyp: ct}
		return nil
	})
	return out
}

func (s *server) writeStatic(w http.ResponseWriter, r *http.Request, name, cache string) {
	f, ok := s.static[name]
	if !ok {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", f.ctyp)
	w.Header().Set("ETag", f.etag)
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.data))
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	s.writeStatic(w, r, r.PathValue("file"), "public, max-age=604800")
}

// serveRootStatic serves a static file from a root path (service worker needs
// scope "/", so it cannot live under /static/). Always revalidated.
func (s *server) serveRootStatic(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if name == "sw.js" {
			w.Header().Set("Service-Worker-Allowed", "/")
		}
		s.writeStatic(w, r, name, "no-cache")
	}
}

var shotExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// serveShot serves files below ShotsDir read-only. os.Root guarantees that
// nothing outside the directory (including via symlinks) can be opened.
func (s *server) serveShot(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if s.d.ShotsDir == "" || rel == "" || strings.ContainsAny(rel, "\x00\\") || !shotExt[strings.ToLower(path.Ext(rel))] {
		s.notFound(w, r)
		return
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || seg == "." || seg == "" {
			s.notFound(w, r)
			return
		}
	}
	root, err := os.OpenRoot(s.d.ShotsDir)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		s.notFound(w, r)
		return
	}
	// Paths embed the capture timestamp, so content never changes.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, path.Base(rel), st.ModTime(), f)
}
