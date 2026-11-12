package web

import (
	"html/template"
	"net/http"
	"sort"
	"sync"
)

// NavItem is a section of the app. Features register theirs from an init() in
// their own file with registerNav; templates never hard-code the menu.
//
// Primary items appear in the mobile bottom bar; the rest are reached through
// "Más" (/mas) on mobile and appear inline in the desktop header.
type NavItem struct {
	Key     string // matches the `nav` argument passed to render()
	Label   string
	Href    string
	Desc    string        // one-line description shown on /mas
	Icon    template.HTML // inner SVG markup for a 24x24 viewBox, stroke="currentColor"
	Primary bool
	Order   int
	Show    func(*server) bool // nil = always
}

var (
	navMu    sync.Mutex
	navItems []NavItem
	// routeHooks let features add their routes without editing web.go.
	routeHooks []func(*server, *http.ServeMux)
)

func registerNav(i NavItem) {
	navMu.Lock()
	defer navMu.Unlock()
	navItems = append(navItems, i)
}

// registerRoutes lets a feature file mount its handlers (call from init()).
func registerRoutes(f func(*server, *http.ServeMux)) {
	navMu.Lock()
	defer navMu.Unlock()
	routeHooks = append(routeHooks, f)
}

// registerPage adds page templates (templates/<name>.html) to be parsed with base.html.
func registerPage(names ...string) { pages = append(pages, names...) }

func (s *server) visibleNav() []NavItem {
	navMu.Lock()
	defer navMu.Unlock()
	out := make([]NavItem, 0, len(navItems))
	for _, i := range navItems {
		if i.Show == nil || i.Show(s) {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Order < out[b].Order })
	return out
}

func ic(inner string) template.HTML { return template.HTML(inner) }

func init() {
	st := func(p string) template.HTML {
		return ic(`<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + p + `</g>`)
	}
	registerNav(NavItem{Key: "home", Label: "Portada", Href: "/", Primary: true, Order: 10,
		Icon: st(`<path d="M3 11l9-8 9 8M5 10v10h5v-6h4v6h5V10"/>`)})
	registerNav(NavItem{Key: "search", Label: "Buscar", Href: "/buscar", Primary: true, Order: 20,
		Icon: st(`<circle cx="11" cy="11" r="7"/><path d="M16.5 16.5L21 21"/>`)})
	registerNav(NavItem{Key: "ask", Label: "Preguntar", Href: "/preguntar", Primary: true, Order: 30,
		Icon: st(`<path d="M4 5h16v11H9l-5 4z"/>`)})
	registerNav(NavItem{Key: "trends", Label: "Tendencias", Href: "/tendencias", Primary: true, Order: 40,
		Desc: "Términos en alza e historias que cubren varios medios",
		Show: func(s *server) bool { return s.d.Trends != nil },
		Icon: st(`<path d="M3 17l6-6 4 4 8-8M15 7h6v6"/>`)})
	registerNav(NavItem{Key: "shots", Label: "Capturas", Href: "/capturas", Order: 60,
		Desc: "Portadas de cada medio a lo largo del día",
		Icon: st(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 16l5-5 4 4 3-3 6 6"/>`)})
	registerNav(NavItem{Key: "status", Label: "Estado", Href: "/estado", Order: 99,
		Desc: "Salud de las fuentes, trabajos y espacio en disco",
		Icon: st(`<path d="M3 12h4l3-7 4 14 3-7h4"/>`)})
}

type moreView struct{ Items []NavItem }

func (s *server) pageMore(w http.ResponseWriter, r *http.Request) {
	var sec []NavItem
	for _, i := range s.visibleNav() {
		if !i.Primary {
			sec = append(sec, i)
		}
	}
	s.render(w, r, http.StatusOK, "more", "Más", "more", moreView{Items: sec})
}

func init() {
	registerPage("more")
	registerRoutes(func(s *server, m *http.ServeMux) { m.HandleFunc("GET /mas", s.pageMore) })
}
