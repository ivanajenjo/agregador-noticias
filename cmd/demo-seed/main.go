// Command demo-seed writes a deterministic, fictional demo data dir that the
// real application can serve (see docs/DEMO.md). Dev tool: no network.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/demo"
)

func main() {
	out := flag.String("out", "./demo-data", "data directory to (re)create")
	days := flag.Int("days", 14, "days of background articles")
	seed := flag.Int64("seed", 42, "random seed (same seed + same -now = same data)")
	sources := flag.String("sources", "config/sources.yaml", "sources file")
	nowFlag := flag.String("now", "", "reference time (RFC3339); default: current time")
	quiet := flag.Bool("q", false, "no progress output")
	flag.Parse()

	o := demo.Options{Out: *out, Days: *days, Seed: *seed, SourcesFile: *sources}
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: -now:", err)
			os.Exit(2)
		}
		o.Now = t
	}
	if !*quiet {
		o.Log = func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
	}
	start := time.Now()
	s, err := demo.Generate(context.Background(), o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("demo data in %s (%s): %d articles (%d in stories), %d revisions, %d annotations, %d screenshots, %d digests, hash %s\n",
		*out, time.Since(start).Round(time.Millisecond), s.Articles, s.StoryArts, s.Revisions, s.Annotations, s.Screenshots, s.Digests, s.Hash[:12])
}
