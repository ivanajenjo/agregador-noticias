package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
	"github.com/ivanajenjo/agregador-noticias/internal/store"
	"github.com/ivanajenjo/agregador-noticias/internal/web"
)

func main() {
	ctx := context.Background()
	os.Remove("/tmp/annodev.db")
	s, err := store.Open("/tmp/annodev.db")
	if err != nil {
		panic(err)
	}
	src := []domain.Source{{ID: "pais", Name: "El País", Leaning: "centro-izquierda"}, {ID: "pub", Name: "Público", Leaning: "izquierda"},
		{ID: "mundo", Name: "El Mundo", Leaning: "centro-derecha"}, {ID: "abc", Name: "ABC", Leaning: "derecha"}, {ID: "tiny", Name: "Medio con pocas noticias", Leaning: "centro"}}
	_ = s.UpsertSources(ctx, src)
	rnd := rand.New(rand.NewSource(1))
	tones, intents, frames := domain.AnnoTones, domain.AnnoIntents, domain.AnnoFrames
	var first int64
	for i := 0; i < 400; i++ {
		so := src[rnd.Intn(4)]
		if i%80 == 0 {
			so = src[4]
		}
		a := domain.Article{SourceID: so.ID, URL: fmt.Sprint("http://x/", i), CanonicalURL: fmt.Sprint("http://x/", i), Title: fmt.Sprintf("Titular de prueba número %d sobre política y economía", i),
			Summary: "Resumen de ejemplo.", PublishedAt: time.Now().Add(-time.Duration(rnd.Intn(20*24)) * time.Hour)}
		if _, err := s.InsertArticle(ctx, &a); err != nil {
			panic(err)
		}
		if first == 0 {
			first = a.ID
		}
		conf := 0.4 + rnd.Float64()*0.6
		put := func(k, l string) {
			_ = s.SaveAnnotation(ctx, domain.Annotation{ArticleID: a.ID, Kind: k, Model: "qwen2.5:7b", Label: l, Score: conf, Data: `{"rationale":"Justificación breve de ejemplo."}`})
		}
		w := rnd.Intn(10)
		tn := tones[0]
		if w > 5 {
			tn = tones[rnd.Intn(6)]
		}
		put("tone", tn)
		put("intent", intents[rnd.Intn(2)*rnd.Intn(5)])
		put("frame", frames[rnd.Intn(6)])
		put("opinion", "no")
	}
	h := web.New(web.Deps{Store: s, Annotations: s, AnnotationsEnabled: true, AnnotationModel: "qwen2.5:7b"}, web.Options{Version: "dev"})
	fmt.Println("first", first)
	panic(http.ListenAndServe("127.0.0.1:8766", h))
}
