package ingest

import (
	"net/url"
	"os"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

func TestSourcesYAML(t *testing.T) {
	raw, err := os.ReadFile("../../config/sources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Sources []domain.Source `yaml:"sources"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) < 12 {
		t.Fatalf("only %d sources, want >= 12", len(cfg.Sources))
	}
	slug := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	leanings := map[string]bool{"izquierda": true, "centro-izquierda": true, "centro": true, "centro-derecha": true, "derecha": true}
	ids, feeds := map[string]bool{}, map[string]bool{}
	for _, s := range cfg.Sources {
		if !slug.MatchString(s.ID) {
			t.Errorf("id %q is not a slug", s.ID)
		}
		if ids[s.ID] {
			t.Errorf("duplicate id %q", s.ID)
		}
		ids[s.ID] = true
		if s.Name == "" || !leanings[s.Leaning] {
			t.Errorf("%s: bad name/leaning (%q, %q)", s.ID, s.Name, s.Leaning)
		}
		if u, err := url.Parse(s.Homepage); err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("%s: bad homepage %q", s.ID, s.Homepage)
		}
		if len(s.Feeds) == 0 {
			t.Errorf("%s: no feeds", s.ID)
		}
		for _, f := range s.Feeds {
			u, err := url.Parse(f.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				t.Errorf("%s: feed URL must be https: %q", s.ID, f.URL)
			}
			if feeds[f.URL] {
				t.Errorf("duplicate feed URL %q", f.URL)
			}
			feeds[f.URL] = true
		}
	}
}
