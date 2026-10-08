package devops

// v0.10.1124 — wiki arama ucunun on-prem Azure DevOps Server sözleşmesi:
// koleksiyon adresi (almsearch yok), gövde şekli (searchText/$top/$skip/
// filters.Project/includeFacets), yanıt şekli (fileName/path/project/wiki/
// hits), önizleme sürümleri, sürümle ilgisiz 400'ün "unavailable" sayılmaması
// ve tanı (sınıf + http durumu + sürüm). Adlar sentetik.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestSearchWikiOnPremShapeAndPreviewVersions(t *testing.T) {
	var tried []string
	var body map[string]any
	var gotPath string
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		ver := r.URL.Query().Get("api-version")
		tried = append(tried, ver)
		gotPath = r.URL.Path
		// Azure DevOps Server 2019 (5.0): yalnız 5.0-preview.1 konuşur.
		if ver != "5.0-preview.1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The requested REST API version of ` + ver + ` is out of range for this server. The latest REST API version this server supports is 5.0.","typeKey":"VssVersionOutOfRangeException"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		jsonOK(w, map[string]any{"count": 2, "results": []map[string]any{
			{"fileName": "Restart-svc%2Dorders.md", "path": "/Runbooks/Restart-svc%2Dorders.md",
				"project": map[string]string{"name": "Platform"},
				"wiki":    map[string]string{"id": "w1", "name": "Platform.wiki", "mappedPath": "/"},
				"hits": []map[string]any{
					{"fieldReferenceName": "fileNames", "highlights": []string{"<highlighthit>Restart</highlighthit>"}},
					{"fieldReferenceName": "content", "highlights": []string{"kubectl rollout <highlighthit>restart</highlighthit>"}},
				}},
			// path'siz eski şekil → fileName'e düşülür.
			{"fileName": "Mimari.md", "project": map[string]string{"name": "Platform"},
				"wiki": map[string]string{"id": "w1", "name": "Platform.wiki"}},
		}})
	})
	hits, info, err := s.SearchWiki(context.Background(), "restart", "Platform", 5)
	if err != nil {
		t.Fatalf("arama: %v (%+v)", err, info)
	}
	if gotPath != "/DefaultCollection/_apis/search/wikisearchresults" {
		t.Errorf("on-prem uç koleksiyon adresinde olmalı: %s", gotPath)
	}
	if len(hits) != 2 || hits[0].Path != "/Runbooks/Restart svc-orders" || hits[0].Snippet != "kubectl rollout restart" || hits[1].Path != "/Mimari" {
		t.Fatalf("yanıt şekli: %+v", hits)
	}
	if info.Class != WikiSearchOK || info.APIVersion != "5.0-preview.1" || info.HTTPStatus != 200 || info.Hits != 2 {
		t.Errorf("tanı: %+v", info)
	}
	f, _ := body["filters"].(map[string]any)
	pr, _ := f["Project"].([]any)
	if len(pr) != 1 || pr[0] != "Platform" || body["searchText"] != "restart" || body["$top"].(float64) != 5 {
		t.Errorf("gövde: %v", body)
	}
	if v, ok := body["includeFacets"]; !ok || v != false {
		t.Errorf("includeFacets açıkça false gitmeli: %v", body)
	}
	if _, ok := body["$skip"]; !ok {
		t.Errorf("$skip gövdede olmalı: %v", body)
	}
	// 7.0 → (önizleme eki istenmedi) 6.0-preview.1 → 5.1-preview.1 → 5.0-preview.1
	if strings.Join(tried, ",") != "7.0,6.0-preview.1,5.1-preview.1,5.0-preview.1" {
		t.Errorf("sürüm sırası: %v", tried)
	}
	// Çalışan sürüm hatırlanır: ikinci arama doğrudan onu dener.
	tried = nil
	if _, _, err := s.SearchWiki(context.Background(), "restart", "", 5); err != nil {
		t.Fatal(err)
	}
	if len(tried) != 1 || tried[0] != "5.0-preview.1" {
		t.Errorf("bilinen sürüm önce: %v", tried)
	}
}

func TestSearchWikiPreviewSuffixRetry(t *testing.T) {
	var tried []string
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		ver := r.URL.Query().Get("api-version")
		tried = append(tried, ver)
		if ver == "7.0" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The requested version \"7.0\" of the resource is under preview. The -preview flag must be supplied in the api-version for such requests."}`))
			return
		}
		jsonOK(w, map[string]any{"count": 0, "results": []any{}})
	})
	_, info, err := s.SearchWiki(context.Background(), "x", "", 5)
	if err != nil || info.APIVersion != "7.0-preview.1" || strings.Join(tried, ",") != "7.0,7.0-preview.1" {
		t.Fatalf("önizleme eki tekrar denemesi: %v %+v %v", err, info, tried)
	}
}

func TestSearchWikiNonVersion400IsTransient(t *testing.T) {
	calls := 0
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Invalid filter: Project 'Nope' does not exist."}`))
	})
	_, info, err := s.SearchWiki(context.Background(), "x", "Nope", 5)
	if err == nil || errors.Is(err, ErrWikiSearchUnavailable) {
		t.Fatalf("sürümle ilgisiz 400 'unavailable' DEĞİL: %v", err)
	}
	if info.Class != WikiSearchBadRequest || info.HTTPStatus != 400 || calls != 1 {
		t.Errorf("tanı: %+v çağrı=%d", info, calls)
	}
	if strings.Contains(err.Error(), "pat-xyz") {
		t.Error("hata PAT sızdırmamalı")
	}
	auth := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	if _, info, err := auth.SearchWiki(context.Background(), "x", "", 5); err == nil || info.Class != WikiSearchAuth {
		t.Errorf("401 sınıfı auth: %+v %v", info, err)
	}
}

func TestWikiSearchVersionReject(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`The requested REST API version of 7.0 is out of range for this server.`, true},
		{`VssVersionOutOfRangeException`, true},
		{`The -preview flag must be supplied in the api-version`, true},
		{`Invalid filter value`, false},
		{`searchText cannot be empty`, false},
	}
	for _, c := range cases {
		if got := wikiSearchVersionReject(http.StatusBadRequest, []byte(c.body)); got != c.want {
			t.Errorf("%q → %v, want %v", c.body, got, c.want)
		}
	}
	if wikiSearchVersionReject(http.StatusInternalServerError, []byte("api-version")) {
		t.Error("yalnız 400")
	}
}
