package wiki

// v0.10.1122 — test sahteleri: httptest üzerinde sahte Azure DevOps Server
// (projeler, wiki'ler, sayfa ağacı, ETag'li sayfa, git öğeleri, opsiyonel
// arama ucu) ve CH'nin bellek-içi ikizi (aynı has/countEqual anlamı).
// Tüm adlar sentetik (example.test, svc-orders).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/devops"
)

const fakePAT = "pat-test-secret"

type fakePage struct {
	content string
	obj     string // git objectId
	etag    string
}

type fakeWiki struct {
	id, name, project, repo string
	pages                   map[string]*fakePage // sayfa yolu → sayfa
}

type fakeDevOps struct {
	mu            sync.Mutex
	wikis         []*fakeWiki
	itemsOff      bool   // git öğe listesi 404 → koşullu GET yolu
	search        string // "absent" (404) | "present"
	pageGets      map[string]int
	condHits      int // 304 sayısı
	searchCalls   int
	searchTexts   []string
	requests      int
	htmlSignIn    bool
	treeFailsWiki string
	// onPageGet — içerikli sayfa okumasında çağrılır (kilit TUTULURKEN).
	onPageGet func(n int)
}

func gitPathOf(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		s = strings.ReplaceAll(s, "-", "%2D")
		segs[i] = strings.ReplaceAll(s, " ", "-")
	}
	return "/" + strings.Join(segs, "/") + ".md"
}

func (f *fakeDevOps) wikiByID(id string) *fakeWiki {
	for _, w := range f.wikis {
		if w.id == id {
			return w
		}
	}
	return nil
}

func writeJSONT(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeDevOps) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if _, pw, ok := r.BasicAuth(); !ok || pw != fakePAT {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.htmlSignIn {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>Sign in</html>"))
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/DefaultCollection")
		segs := strings.Split(strings.Trim(p, "/"), "/")
		q := r.URL.Query()
		switch {
		case p == "/_apis/projects":
			set := map[string]bool{}
			for _, wk := range f.wikis {
				set[wk.project] = true
			}
			var v []map[string]string
			for name := range set {
				v = append(v, map[string]string{"name": name})
			}
			sort.Slice(v, func(i, j int) bool { return v[i]["name"] < v[j]["name"] })
			writeJSONT(w, map[string]any{"count": len(v), "value": v})
		case p == "/_apis/search/wikisearchresults":
			f.searchCalls++
			if f.search == "error" {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if f.search != "present" && f.search != "badreq" {
				http.NotFound(w, r)
				return
			}
			if f.search == "badreq" {
				// Sürümle İLGİSİZ 400 (ör. geçersiz süzgeç) — uç VAR.
				var bb struct {
					SearchText string `json:"searchText"`
				}
				_ = json.NewDecoder(r.Body).Decode(&bb)
				f.searchTexts = append(f.searchTexts, bb.SearchText)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"The filter Project is invalid."}`))
				return
			}
			var body struct {
				SearchText string `json:"searchText"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.searchTexts = append(f.searchTexts, body.SearchText)
			// ADO anlamı: boşlukla ayrılmış terimler AND, " OR " ile ayrılmışlar OR.
			isOr := strings.Contains(body.SearchText, " OR ")
			var needles []string
			if isOr {
				needles = strings.Split(body.SearchText, " OR ")
			} else {
				needles = strings.Fields(body.SearchText)
			}
			match := func(text string) bool {
				n := 0
				for _, nd := range needles {
					if strings.Contains(Fold(text), Fold(nd)) {
						n++
					}
				}
				if isOr {
					return n > 0
				}
				return n == len(needles)
			}
			var res []map[string]any
			for _, wk := range f.wikis {
				for path, pg := range wk.pages {
					if match(pg.content + " " + path) {
						res = append(res, map[string]any{
							"path":    gitPathOf(path),
							"wiki":    map[string]string{"id": wk.id, "name": wk.name, "mappedPath": "/"},
							"project": map[string]string{"name": wk.project},
							"hits":    []map[string]any{{"highlights": []string{"<highlighthit>" + body.SearchText + "</highlighthit>"}}},
						})
					}
				}
			}
			writeJSONT(w, map[string]any{"count": len(res), "results": res})
		case len(segs) == 4 && segs[1] == "_apis" && segs[2] == "wiki" && segs[3] == "wikis":
			var v []map[string]any
			for _, wk := range f.wikis {
				if wk.project == segs[0] {
					v = append(v, map[string]any{"id": wk.id, "name": wk.name, "type": "projectWiki",
						"repositoryId": wk.repo, "mappedPath": "/", "versions": []map[string]string{{"version": "wikiMaster"}}})
				}
			}
			writeJSONT(w, map[string]any{"value": v})
		case len(segs) == 6 && segs[5] == "pages":
			wk := f.wikiByID(segs[4])
			if wk == nil {
				http.NotFound(w, r)
				return
			}
			path := q.Get("path")
			if q.Get("recursionLevel") == "full" {
				if f.treeFailsWiki == wk.id {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				writeJSONT(w, buildTree(wk))
				return
			}
			pg, ok := wk.pages[path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if inm := strings.Trim(r.Header.Get("If-None-Match"), `"`); inm != "" && inm == pg.etag {
				f.condHits++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if f.pageGets == nil {
				f.pageGets = map[string]int{}
			}
			f.pageGets[wk.id+path]++
			if f.onPageGet != nil {
				n := 0
				for _, c := range f.pageGets {
					n += c
				}
				f.onPageGet(n)
			}
			w.Header().Set("ETag", `"`+pg.etag+`"`)
			writeJSONT(w, map[string]any{"path": path, "gitItemPath": gitPathOf(path), "content": pg.content,
				"remoteUrl": "https://devops.example.test/DefaultCollection/" + url.PathEscape(wk.project) + "/_wiki/wikis/" + url.PathEscape(wk.name) + "?pagePath=" + url.QueryEscape(path)})
		case len(segs) == 6 && segs[2] == "git" && segs[5] == "items":
			if f.itemsOff {
				http.NotFound(w, r)
				return
			}
			var wk *fakeWiki
			for _, x := range f.wikis {
				if x.repo == segs[4] {
					wk = x
				}
			}
			if wk == nil {
				http.NotFound(w, r)
				return
			}
			var v []map[string]any
			for path, pg := range wk.pages {
				v = append(v, map[string]any{"objectId": pg.obj, "gitObjectType": "blob", "path": gitPathOf(path)})
			}
			writeJSONT(w, map[string]any{"value": v})
		default:
			t.Logf("sahte sunucu: bilinmeyen yol %s", r.URL.String())
			http.NotFound(w, r)
		}
	})
}

// buildTree — yollardan iç içe ağaç (ara düğümler de sayfa sayılır).
func buildTree(wk *fakeWiki) map[string]any {
	type node struct {
		path string
		kids map[string]*node
	}
	root := &node{path: "/", kids: map[string]*node{}}
	var paths []string
	for p := range wk.pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		cur := root
		acc := ""
		for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
			acc += "/" + seg
			n, ok := cur.kids[acc]
			if !ok {
				n = &node{path: acc, kids: map[string]*node{}}
				cur.kids[acc] = n
			}
			cur = n
		}
	}
	var conv func(n *node) map[string]any
	conv = func(n *node) map[string]any {
		var keys []string
		for k := range n.kids {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var subs []map[string]any
		for _, k := range keys {
			subs = append(subs, conv(n.kids[k]))
		}
		m := map[string]any{"path": n.path, "subPages": subs}
		if n.path != "/" {
			m["gitItemPath"] = gitPathOf(n.path)
		}
		return m
	}
	return conv(root)
}

func newFakeDevOps(t *testing.T, f *fakeDevOps) (*devops.Service, *httptest.Server) {
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: ts.URL, Collection: "DefaultCollection", PAT: fakePAT, Flavor: devops.FlavorServer})
	return dv, ts
}

// ── bellek-içi Store ────────────────────────────────────────────────────────

type memChunk struct {
	PageRecord
	ChunkRecord
}

type memStore struct {
	mu       sync.Mutex
	settings map[string][]byte
	pages    map[string]PageRecord // wikiID\x00path
	chunks   map[string][]ChunkRecord
	upserts  int
	deletes  int
}

func newMemStore() *memStore {
	return &memStore{settings: map[string][]byte{}, pages: map[string]PageRecord{}, chunks: map[string][]ChunkRecord{}}
}

func (m *memStore) GetSetting(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[k], nil
}

func (m *memStore) PutSetting(_ context.Context, k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[k] = append([]byte(nil), v...)
	return nil
}

func (m *memStore) WikiPageIndex(context.Context) ([]PageMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PageMeta
	for _, p := range m.pages {
		out = append(out, PageMeta{WikiID: p.WikiID, Path: p.Path, Project: p.Project, WikiName: p.WikiName,
			Version: p.Version, Hash: p.Hash, Chunks: p.Chunks})
	}
	return out, nil
}

func (m *memStore) UpsertWikiPage(_ context.Context, p PageRecord, chunks []ChunkRecord, _ uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.Chunks = uint32(len(chunks))
	m.pages[p.WikiID+"\x00"+p.Path] = p
	m.chunks[p.WikiID+"\x00"+p.Path] = chunks
	m.upserts++
	return nil
}

func (m *memStore) DeleteWikiPage(_ context.Context, pm PageMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pages, pm.WikiID+"\x00"+pm.Path)
	delete(m.chunks, pm.WikiID+"\x00"+pm.Path)
	m.deletes++
	return nil
}

func (m *memStore) all(project string) []memChunk {
	var out []memChunk
	for k, cs := range m.chunks {
		p := m.pages[k]
		if project != "" && p.Project != project {
			continue
		}
		for _, c := range cs {
			out = append(out, memChunk{p, c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Idx < out[j].Idx
	})
	return out
}

func has(xs []string, t string) bool {
	for _, x := range xs {
		if x == t {
			return true
		}
	}
	return false
}

func (m *memStore) WikiTermStats(_ context.Context, terms []string, project string) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.all(project)
	st := Stats{N: uint64(len(all)), DF: make([]uint64, len(terms))}
	var dl float64
	for _, c := range all {
		dl += float64(len(c.Tokens))
		for i, t := range terms {
			if has(c.Tokens, t) {
				st.DF[i]++
			}
		}
	}
	if len(all) > 0 {
		st.AvgDL = dl / float64(len(all))
	}
	return st, nil
}

func (m *memStore) WikiCandidates(_ context.Context, terms []string, project string, limit int) ([]Candidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Candidate
	for _, c := range m.all(project) {
		any := false
		for _, t := range terms {
			if has(c.Tokens, t) {
				any = true
			}
		}
		if !any {
			continue
		}
		out = append(out, candidateFromChunk(c.PageRecord, c.ChunkRecord, terms))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memStore) WikiSemantic(_ context.Context, emb []float32, project string, k int) ([]SemHit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SemHit
	for _, c := range m.all(project) {
		if len(c.Embedding) != len(emb) {
			continue
		}
		var dot float64
		for i := range emb {
			dot += float64(emb[i] * c.Embedding[i])
		}
		out = append(out, SemHit{ChunkRef: ChunkRef{Project: c.Project, WikiID: c.WikiID, WikiName: c.WikiName,
			Path: c.Path, Title: c.Title, URL: c.URL, Heading: c.Heading, Idx: c.Idx, Text: c.Text}, Cos: dot})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cos > out[j].Cos })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func (m *memStore) GetWikiPage(_ context.Context, project, wikiRef, path string) (*PageRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pages {
		if p.Project == project && (p.WikiName == wikiRef || p.WikiID == wikiRef) && p.Path == path {
			cp := p
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memStore) WikiCounts(context.Context) (uint64, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var c uint64
	for _, cs := range m.chunks {
		c += uint64(len(cs))
	}
	return uint64(len(m.pages)), c, nil
}
