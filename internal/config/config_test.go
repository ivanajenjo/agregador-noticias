package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsAndEnv(t *testing.T) {
	t.Setenv("AGREGADOR_LISTEN", ":9999")
	t.Setenv("AGREGADOR_CHAT_BASE_URL", "http://mac:11434/v1")
	t.Setenv("AGREGADOR_CHAT_MODEL", "qwen3")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":9999" || c.Chat.Model != "qwen3" || c.Ingest.Cron == "" {
		t.Fatalf("unexpected %+v", c)
	}
}

func TestLoadYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("listen: ':1'\nscreenshots:\n  retention: 48h\n"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":1" || c.Screenshots.Retention != 48*time.Hour || c.Screenshots.Quality != 70 {
		t.Fatalf("unexpected %+v", c)
	}
}

func TestValidate(t *testing.T) {
	t.Setenv("AGREGADOR_EMBEDDINGS_BASE_URL", "http://x/v1")
	if _, err := Load(""); err == nil {
		t.Fatal("expected error: model missing")
	}
}
