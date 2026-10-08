package devops

// wiki.go — v0.10.1122 ("karma": CoSRE sohbeti kurumun on-prem Azure DevOps
// wiki'lerinden cevap versin). Wiki REST istemcisi — YALNIZ okuma.
//
// Kimlik ve TLS mevcut "devops_connection" ayarından gelir (BaseURL +
// koleksiyon + PAT + InsecureSkipVerify); YENİ kimlik bilgisi yok. PAT'in
// "Wiki (Read)" kapsamı olmalı; arama için ayrıca Search uzantısı (opsiyonel).
//
// Uçlar (Azure DevOps Server 2019+ ve TFS 2018 aynı şekli konuşur):
//
//	GET  {koll}/_apis/projects                                   — proje listesi
//	GET  {koll}/{proje}/_apis/wiki/wikis                         — wiki listesi
//	GET  {koll}/{proje}/_apis/wiki/wikis/{id}/pages?path=/&recursionLevel=full
//	GET  {koll}/{proje}/_apis/wiki/wikis/{id}/pages?path=P&includeContent=true  (+ETag)
//	GET  {koll}/{proje}/_apis/git/repositories/{repo}/items?recursionLevel=Full (objectId — değişim tespiti)
//	POST {koll}[/{proje}]/_apis/search/wikisearchresults          — opsiyonel canlı arama
//
// api-version: kodun geri kalanıyla aynı aday sırası (tespit edilen önce,
// sonra 6.0 / 4.1; apiVersionCandidates). Önizleme sürümü isteyen eski
// sunucuya (400 "preview") aynı sürüm "-preview.1" ekiyle bir kez daha
// denenir; çalışan sürüm süreç içinde hatırlanır (wikiVer).
//
// Güvenlik: PAT hiçbir hata metnine girmez (sanitize), sayfa içeriği hiçbir
// log satırına yazılmaz; çağıran da yazmaz (internal/wiki).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	// wikiReqTimeout — tek isteğin tavanı (client'ın 20 sn'lik tavanıyla aynı;
	// ctx üzerinden de uygulanır ki iptal anında kesilsin).
	wikiReqTimeout = 20 * time.Second
	// WikiPageBodyCap — tek sayfa yanıtı (içerik dahil) okuma tavanı.
	WikiPageBodyCap = 2 << 20
	// wikiTreeBodyCap — sayfa ağacı / git öğe listesi okuma tavanı.
	wikiTreeBodyCap = 16 << 20
	// wikiListBodyCap — proje / wiki listesi ve arama yanıtı tavanı.
	wikiListBodyCap = 2 << 20
	// wikiProjectsMax — taranan proje sayısı tavanı (kaçak sınırı).
	wikiProjectsMax = 2000
)

// ErrWikiSearchUnavailable — sunucuda Search uzantısı / wiki arama ucu yok
// (404) ya da sürüm aralığı dışında. Çağıran bunu "kalıcı kapalı" sayar ve
// önbelleğe alır; geçici hata DEĞİLDİR.
var ErrWikiSearchUnavailable = errors.New("wiki search unavailable on this server")

// ErrWikiNotModified — koşullu GET'te sayfa değişmemiş (304).
var ErrWikiNotModified = errors.New("wiki page not modified")

// WikiInfo — bir wiki'nin kullandığımız yarısı.
type WikiInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"` // projectWiki | codeWiki
	Project      string `json:"project"`
	RepositoryID string `json:"repositoryId"`
	MappedPath   string `json:"mappedPath"`
	Version      string `json:"version"` // dal (codeWiki) ya da wikiMaster
	RemoteURL    string `json:"remoteUrl"`
}

// WikiPageRef — sayfa ağacının düzleştirilmiş tek düğümü.
type WikiPageRef struct {
	Path        string
	GitItemPath string
	RemoteURL   string
}

// WikiPage — içerikli sayfa okuması.
type WikiPage struct {
	Path        string
	GitItemPath string
	RemoteURL   string
	Content     string
	ETag        string
}

// WikiSearchHit — canlı aramanın sonucu (sayfa yoluna çevrilmiş).
type WikiSearchHit struct {
	Project  string
	WikiID   string
	WikiName string
	Path     string // wiki sayfa yolu ("/Runbooks/Restart svc")
	Snippet  string // vurgulanan parça (HTML etiketleri soyulmuş)
}

// wikiCfg — canlı ayarın anlık görüntüsü + client; yapılandırılmamışsa hata.
func (s *Service) wikiCfg() (Settings, *http.Client, error) {
	if s == nil || !s.Configured() {
		return Settings{}, nil, errors.New("azure DevOps bağlantısı yapılandırılmamış (Ayarlar → Kod entegrasyonu)")
	}
	cfg := s.CurrentSettings()
	return cfg, s.clientFor(cfg.InsecureSkipVerify), nil
}

// wikiVersions — denenecek api-version listesi: süreçte çalıştığı görülen
// sürüm önce, sonra genel aday sırası.
func (s *Service) wikiVersions(cfg Settings) []string {
	s.mu.RLock()
	known := s.wikiVer
	s.mu.RUnlock()
	return wikiVersionOrder(known, s.apiVersionCandidates(cfg))
}

// wikiVersionOrder — SAF: bilinen sürüm başa, tekrarsız.
func wikiVersionOrder(known string, cands []string) []string {
	out := make([]string, 0, len(cands)+1)
	seen := map[string]bool{}
	if known != "" {
		out = append(out, known)
		seen[known] = true
	}
	for _, v := range cands {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) rememberWikiVersion(v string) {
	s.mu.Lock()
	s.wikiVer = v
	s.mu.Unlock()
}

// wikiResp — tek isteğin sonucu.
type wikiResp struct {
	status int
	header http.Header
	body   []byte
}

// wikiDo — kimlikli tek istek. 401/403 Wiki (Read) kapsamını adlandırır;
// 2xx'te JSON olmayan gövde (form oturum açma sayfası) hatadır. 304 ve
// 404 ayrı döner (çağıran sınıflandırır). Hata metinleri sanitize'dan geçer.
func wikiDo(ctx context.Context, cli *http.Client, cfg Settings, method, rawURL string, body []byte, ifNoneMatch string, limit int64) (wikiResp, error) {
	ctx, cancel := context.WithTimeout(ctx, wikiReqTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return wikiResp{}, errors.New(sanitize(err.Error(), cfg))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "coremetry-devops/1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	if cfg.PAT != "" || cfg.Username != "" {
		req.SetBasicAuth(cfg.Username, cfg.PAT)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return wikiResp{}, errors.New(sanitize(err.Error(), cfg))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, limit))
	out := wikiResp{status: resp.StatusCode, header: resp.Header, body: raw}
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return out, ErrWikiNotModified
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return out, fmt.Errorf("http %d — PAT'i ve kapsamlarını kontrol edin (Wiki: Read)", resp.StatusCode)
	case resp.StatusCode >= 300:
		return out, fmt.Errorf("http %d: %s", resp.StatusCode, sanitize(firstLine(string(raw)), cfg))
	}
	if readErr != nil {
		return out, fmt.Errorf("yanıt gövdesi okunamadı: %s", sanitize(readErr.Error(), cfg))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "json") {
		return out, fmt.Errorf("sunucu JSON yerine %s döndü — uç büyük olasılıkla bir oturum açma sayfasının arkasında", firstLine(ct))
	}
	return out, nil
}

// wikiPreviewRetry — SAF: 400 cevabı önizleme sürümü mü istiyor?
func wikiPreviewRetry(status int, body []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), "preview")
}

// wikiGetJSON — api-version adaylarını sırayla dener (gerekirse "-preview.1"
// ekiyle). pathQuery `?` ya da `&` ile biten sorgu kökünü taşır; api-version
// sona eklenir. Çalışan sürüm hatırlanır.
func (s *Service) wikiGetJSON(ctx context.Context, cfg Settings, cli *http.Client, base string, ifNoneMatch string, limit int64) (wikiResp, error) {
	var firstErr error
	for _, v := range s.wikiVersions(cfg) {
		for _, ver := range []string{v, v + "-preview.1"} {
			resp, err := wikiDo(ctx, cli, cfg, http.MethodGet, base+"api-version="+ver, nil, ifNoneMatch, limit)
			if err == nil || errors.Is(err, ErrWikiNotModified) {
				s.rememberWikiVersion(ver)
				return resp, err
			}
			if ctx.Err() != nil {
				return resp, err
			}
			if firstErr == nil {
				firstErr = err
			}
			if resp.status == http.StatusNotFound || resp.status == http.StatusUnauthorized || resp.status == http.StatusForbidden {
				// Kaynak yok / yetki yok — başka sürüm cevabı değiştirmez.
				return resp, err
			}
			if !wikiPreviewRetry(resp.status, resp.body) {
				break // önizleme eki bu hatayı çözmez; sonraki sürüme geç
			}
		}
	}
	if firstErr == nil {
		firstErr = errors.New("api-version adayı yok")
	}
	return wikiResp{}, firstErr
}

// projectURL — {koll}/{proje}
func projectURL(cfg Settings, project string) string {
	return collectionURL(cfg) + "/" + url.PathEscape(strings.Trim(strings.TrimSpace(project), "/"))
}

// ListWikiProjects — koleksiyondaki proje adları (sıralı). Sayfalı ($top/$skip).
func (s *Service) ListWikiProjects(ctx context.Context) ([]string, error) {
	cfg, cli, err := s.wikiCfg()
	if err != nil {
		return nil, err
	}
	var out []string
	const page = 500
	for skip := 0; skip < wikiProjectsMax; skip += page {
		base := fmt.Sprintf("%s/_apis/projects?$top=%d&$skip=%d&", collectionURL(cfg), page, skip)
		resp, err := s.wikiGetJSON(ctx, cfg, cli, base, "", wikiListBodyCap)
		if err != nil {
			return nil, err
		}
		var pr projectsResponse
		if err := json.Unmarshal(resp.body, &pr); err != nil {
			return nil, errors.New("beklenmeyen yanıt (proje listesi değil)")
		}
		for _, p := range pr.Value {
			if n := strings.TrimSpace(p.Name); n != "" {
				out = append(out, n)
			}
		}
		if len(pr.Value) < page {
			break
		}
	}
	sort.Strings(out)
	return out, nil
}

type wikisResponse struct {
	Value []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Type         string `json:"type"`
		RepositoryID string `json:"repositoryId"`
		MappedPath   string `json:"mappedPath"`
		RemoteURL    string `json:"remoteUrl"`
		Versions     []struct {
			Version string `json:"version"`
		} `json:"versions"`
	} `json:"value"`
}

// ListWikis — projedeki wiki'ler (proje wiki'si + kod wiki'leri).
func (s *Service) ListWikis(ctx context.Context, project string) ([]WikiInfo, error) {
	cfg, cli, err := s.wikiCfg()
	if err != nil {
		return nil, err
	}
	resp, err := s.wikiGetJSON(ctx, cfg, cli, projectURL(cfg, project)+"/_apis/wiki/wikis?", "", wikiListBodyCap)
	if err != nil {
		if resp.status == http.StatusNotFound {
			return nil, nil // wiki'siz proje
		}
		return nil, fmt.Errorf("proje %q wiki listesi: %w", project, err)
	}
	return parseWikis(resp.body, project)
}

// parseWikis — SAF.
func parseWikis(body []byte, project string) ([]WikiInfo, error) {
	var wr wikisResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, errors.New("beklenmeyen yanıt (wiki listesi değil)")
	}
	out := make([]WikiInfo, 0, len(wr.Value))
	for _, w := range wr.Value {
		if strings.TrimSpace(w.ID) == "" {
			continue
		}
		wi := WikiInfo{
			ID: w.ID, Name: w.Name, Type: w.Type, Project: project,
			RepositoryID: w.RepositoryID, MappedPath: w.MappedPath, RemoteURL: w.RemoteURL,
		}
		if len(w.Versions) > 0 {
			wi.Version = w.Versions[0].Version
		}
		out = append(out, wi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// wikiPageNode — pages yanıtının (özyinelemeli) düğümü.
type wikiPageNode struct {
	Path        string         `json:"path"`
	GitItemPath string         `json:"gitItemPath"`
	RemoteURL   string         `json:"remoteUrl"`
	Content     string         `json:"content"`
	SubPages    []wikiPageNode `json:"subPages"`
}

// pagesURL — {proje}/_apis/wiki/wikis/{id}/pages?path=…&
func pagesURL(cfg Settings, project, wikiID, path string, extra string) string {
	q := url.Values{}
	q.Set("path", path)
	return projectURL(cfg, project) + "/_apis/wiki/wikis/" + url.PathEscape(wikiID) + "/pages?" + q.Encode() + "&" + extra
}

// WikiPageTree — wiki'nin tüm sayfa yolları (recursionLevel=full), düz liste;
// kök "/" düğümü (gerçek sayfa değil) dışarıda. limit>0 ise en çok o kadar
// sayfa döner ve truncated=true.
func (s *Service) WikiPageTree(ctx context.Context, project, wikiID string, limit int) (refs []WikiPageRef, truncated bool, err error) {
	cfg, cli, err := s.wikiCfg()
	if err != nil {
		return nil, false, err
	}
	resp, err := s.wikiGetJSON(ctx, cfg, cli, pagesURL(cfg, project, wikiID, "/", "recursionLevel=full&includeContent=false&"), "", wikiTreeBodyCap)
	if err != nil {
		return nil, false, err
	}
	refs, truncated, err = flattenWikiTree(resp.body, limit)
	return refs, truncated, err
}

// flattenWikiTree — SAF: ağaç → düz liste (önce-derinlik, yol sırası).
func flattenWikiTree(body []byte, limit int) ([]WikiPageRef, bool, error) {
	var root wikiPageNode
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false, errors.New("beklenmeyen yanıt (sayfa ağacı değil)")
	}
	var out []WikiPageRef
	truncated := false
	var walk func(n wikiPageNode)
	walk = func(n wikiPageNode) {
		if truncated {
			return
		}
		if p := strings.TrimSpace(n.Path); p != "" && p != "/" {
			if limit > 0 && len(out) >= limit {
				truncated = true
				return
			}
			out = append(out, WikiPageRef{Path: p, GitItemPath: n.GitItemPath, RemoteURL: n.RemoteURL})
		}
		for _, c := range n.SubPages {
			walk(c)
		}
	}
	walk(root)
	return out, truncated, nil
}

// GetWikiPage — tek sayfanın Markdown içeriği + ETag. ifNoneMatch doluysa
// koşullu istek; 304 → ErrWikiNotModified.
func (s *Service) GetWikiPage(ctx context.Context, project, wikiID, path, ifNoneMatch string) (WikiPage, error) {
	cfg, cli, err := s.wikiCfg()
	if err != nil {
		return WikiPage{}, err
	}
	resp, err := s.wikiGetJSON(ctx, cfg, cli, pagesURL(cfg, project, wikiID, path, "includeContent=true&"), ifNoneMatch, WikiPageBodyCap)
	if err != nil {
		return WikiPage{}, err
	}
	var n wikiPageNode
	if err := json.Unmarshal(resp.body, &n); err != nil {
		return WikiPage{}, errors.New("beklenmeyen yanıt (sayfa değil)")
	}
	p := n.Path
	if p == "" {
		p = path
	}
	return WikiPage{
		Path: p, GitItemPath: n.GitItemPath, RemoteURL: n.RemoteURL,
		Content: n.Content, ETag: strings.Trim(resp.header.Get("ETag"), `"`),
	}, nil
}

// WikiItemVersions — wiki deposundaki dosyaların objectId'leri (git blob
// SHA'sı): gitItemPath → objectId. Değişim tespitinin ucuz yolu (wiki başına
// TEK istek); hata dönerse çağıran sayfa başına koşullu GET'e düşer.
func (s *Service) WikiItemVersions(ctx context.Context, w WikiInfo) (map[string]string, error) {
	if strings.TrimSpace(w.RepositoryID) == "" {
		return nil, errors.New("wiki deposu bilinmiyor")
	}
	cfg, cli, err := s.wikiCfg()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	scope := w.MappedPath
	if strings.TrimSpace(scope) == "" {
		scope = "/"
	}
	q.Set("scopePath", scope)
	q.Set("recursionLevel", "Full")
	if v := strings.TrimSpace(w.Version); v != "" {
		q.Set("versionDescriptor.version", v)
		q.Set("versionDescriptor.versionType", "branch")
	}
	base := projectURL(cfg, w.Project) + "/_apis/git/repositories/" + url.PathEscape(w.RepositoryID) + "/items?" + q.Encode() + "&"
	resp, err := s.wikiGetJSON(ctx, cfg, cli, base, "", wikiTreeBodyCap)
	if err != nil {
		return nil, err
	}
	return parseItemVersions(resp.body, w.MappedPath)
}

// parseItemVersions — SAF: items yanıtı → gitItemPath (mappedPath'e göre
// göreli, "/" ile başlar) → objectId. Yalnız blob'lar.
func parseItemVersions(body []byte, mappedPath string) (map[string]string, error) {
	var ir struct {
		Value []struct {
			ObjectID      string `json:"objectId"`
			GitObjectType string `json:"gitObjectType"`
			Path          string `json:"path"`
			IsFolder      bool   `json:"isFolder"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &ir); err != nil {
		return nil, errors.New("beklenmeyen yanıt (öğe listesi değil)")
	}
	mp := strings.TrimRight(strings.TrimSpace(mappedPath), "/")
	out := make(map[string]string, len(ir.Value))
	for _, it := range ir.Value {
		if it.IsFolder || (it.GitObjectType != "" && it.GitObjectType != "blob") || it.ObjectID == "" {
			continue
		}
		p := it.Path
		if mp != "" && strings.HasPrefix(strings.ToLower(p), strings.ToLower(mp)+"/") {
			p = p[len(mp):]
		}
		out[p] = it.ObjectID
	}
	return out, nil
}

// wikiSearchVersions — arama ucunun sürüm sırası. 7.0 on-prem'de kod
// aramasında doğrulanmış (codesearch.go v0.10.98); eski sunucular
// önizleme sürümleri ister.
var wikiSearchVersions = []string{"7.0", "6.0-preview.1", "5.1-preview.1", "5.0-preview.1"}

type wikiSearchBody struct {
	SearchText string              `json:"searchText"`
	Skip       int                 `json:"$skip"`
	Top        int                 `json:"$top"`
	Filters    map[string][]string `json:"filters,omitempty"`
}

// SearchWiki — Azure DevOps Search ile wiki araması. Uç yoksa (404) ya da
// sunucu sürüm aralığı dışında kalırsa ErrWikiSearchUnavailable; diğer
// hatalar geçicidir.
func (s *Service) SearchWiki(ctx context.Context, text, project string, top int) ([]WikiSearchHit, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	cfg, cli, err := s.wikiCfg()
	if err != nil {
		return nil, err
	}
	if top <= 0 || top > 25 {
		top = 10
	}
	body := wikiSearchBody{SearchText: text, Top: top}
	if p := strings.TrimSpace(project); p != "" {
		body.Filters = map[string][]string{"Project": {p}}
	}
	raw, _ := json.Marshal(body)
	u := collectionURL(cfg) + "/_apis/search/wikisearchresults?api-version="
	for _, ver := range wikiSearchVersions {
		resp, err := wikiDo(ctx, cli, cfg, http.MethodPost, u+ver, raw, "", wikiListBodyCap)
		if err == nil {
			return parseWikiSearch(resp.body)
		}
		if ctx.Err() != nil {
			return nil, err
		}
		switch resp.status {
		case http.StatusNotFound:
			return nil, ErrWikiSearchUnavailable
		case http.StatusBadRequest:
			continue // sürüm aralığı / önizleme — sonraki sürüm
		default:
			return nil, err
		}
	}
	// Tüm sürümler 400 — bu sunucu wiki aramasını konuşmuyor.
	return nil, ErrWikiSearchUnavailable
}

// parseWikiSearch — SAF: arama yanıtı → sayfa yolu.
func parseWikiSearch(body []byte) ([]WikiSearchHit, error) {
	var sr struct {
		Results []struct {
			Path string `json:"path"`
			Wiki struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				MappedPath string `json:"mappedPath"`
			} `json:"wiki"`
			Project struct {
				Name string `json:"name"`
			} `json:"project"`
			Hits []struct {
				Highlights []string `json:"highlights"`
			} `json:"hits"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, errors.New("beklenmeyen yanıt (wiki arama sonucu değil)")
	}
	out := make([]WikiSearchHit, 0, len(sr.Results))
	for _, r := range sr.Results {
		p := WikiPagePathFromGitPath(r.Path, r.Wiki.MappedPath)
		if p == "" || r.Wiki.ID == "" {
			continue
		}
		h := WikiSearchHit{Project: r.Project.Name, WikiID: r.Wiki.ID, WikiName: r.Wiki.Name, Path: p}
		for _, hit := range r.Hits {
			if len(hit.Highlights) > 0 {
				h.Snippet = stripHighlightTags(hit.Highlights[0])
				break
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// WikiPagePathFromGitPath — SAF: wiki deposundaki dosya yolu → sayfa yolu.
// Azure DevOps sayfa adını dosya adına şöyle kodlar: boşluk → "-", gerçek
// "-" → "%2D", diğer özel karakterler yüzde-kodlu; uzantı ".md".
// "/Runbooks/Restart-svc%2Dorders.md" → "/Runbooks/Restart svc-orders".
func WikiPagePathFromGitPath(gitPath, mappedPath string) string {
	p := strings.TrimSpace(gitPath)
	if p == "" {
		return ""
	}
	mp := strings.TrimRight(strings.TrimSpace(mappedPath), "/")
	if mp != "" && strings.HasPrefix(strings.ToLower(p), strings.ToLower(mp)+"/") {
		p = p[len(mp):]
	}
	if !strings.HasSuffix(strings.ToLower(p), ".md") {
		return ""
	}
	p = p[:len(p)-3]
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, seg := range segs {
		seg = strings.ReplaceAll(seg, "-", " ")
		if dec, err := url.PathUnescape(seg); err == nil {
			seg = dec
		}
		segs[i] = seg
	}
	return "/" + strings.Join(segs, "/")
}

// stripHighlightTags — arama vurgularındaki <highlighthit> benzeri etiketleri soyar.
func stripHighlightTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// WikiPageWebURL — operatörün tarayıcıda açacağı sayfa adresi. Sunucunun
// verdiği remoteUrl varsa o (kanonik); yoksa
// {koll}/{proje}/_wiki/wikis/{wiki}?pagePath={yol}.
func (s *Service) WikiPageWebURL(project, wikiName, path, remoteURL string) string {
	if u := strings.TrimSpace(remoteURL); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	if s == nil {
		return ""
	}
	cfg := s.CurrentSettings()
	return WikiWebURL(cfg, project, wikiName, path)
}

// WikiWebURL — SAF yapıcı (WikiPageWebURL'in yedeği).
func WikiWebURL(cfg Settings, project, wikiName, path string) string {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(project) == "" || strings.TrimSpace(wikiName) == "" {
		return ""
	}
	q := url.Values{}
	q.Set("pagePath", path)
	return projectURL(cfg, project) + "/_wiki/wikis/" + url.PathEscape(wikiName) + "?" + q.Encode()
}
