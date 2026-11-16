package mcptools

// v0.10.1554 — read_wiki_page `find`: yüklenen tablo belgesinin tamamı taranır.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/wiki"
)

func wikiTableFixture(n, owners int) wiki.PageRecord {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		owner := "Payments"
		if i%2 == 0 && owners > 0 {
			owner, owners = "PlatformTeam", owners-1
		}
		fmt.Fprintf(&b, "vserver · satır %d — Load Balancer: lb-orders-scoring-prod-east.example.com_443 · Load Balancer IpAddress: 192.0.2.%d · Environment: PROD · CC Name: Örnek Sipariş Raporlama Ambarı Etl · CC Owner: %s · Application Name: Rapor Ambari\n", i+1, i%250, owner)
	}
	return wiki.PageRecord{Project: wiki.LocalProject, WikiName: wiki.LocalWikiName, Path: "/vserver_cc_iliskisi.xlsx", Title: "vserver_cc_iliskisi.xlsx", Content: b.String()}
}

func TestWikiPageFindRowsScansWholeDocument(t *testing.T) {
	rec := wikiTableFixture(600, 120) // ≈120 KB > WikiPageMaxRunes
	if len([]rune(rec.Content)) <= WikiPageMaxRunes {
		t.Fatal("fixture sayfa tavanını aşmalı")
	}
	out := WikiPageFindRows(rec, "platformteam")
	if out["matched_rows"] != 120 || len(out["rows"].([]string)) != 120 || out["total_rows"] != 600 {
		t.Fatalf("matched=%v rows=%d total=%v", out["matched_rows"], len(out["rows"].([]string)), out["total_rows"])
	}
	if last := out["rows"].([]string)[119]; !strings.Contains(last, "satır 241 ") || !strings.Contains(last, "CC Owner: PlatformTeam") {
		t.Fatalf("son satır tavan ötesinden gelmeli: %q", last)
	}
	if !strings.Contains(out["note"].(string), "HEPSİ") {
		t.Fatalf("not: %v", out["note"])
	}
	if out := WikiPageFindRows(rec, "yok-boyle-deger"); out["matched_rows"] != 0 || !strings.Contains(out["note"].(string), "hiçbir hücre") {
		t.Fatalf("eşleşmesiz: %v", out["note"])
	}
	if out := WikiPageFindRows(wikiTableFixture(600, 300), "PlatformTeam"); out["matched_rows"] != 300 || len(out["rows"].([]string)) != WikiFindRowsMax || !strings.Contains(out["note"].(string), "ilk 200") {
		t.Fatalf("tavan: matched=%v note=%v", out["matched_rows"], out["note"])
	}
	plain := wiki.PageRecord{Content: "# Runbook\n\nnormal metin"}
	if out := WikiPageFindRows(plain, "x"); out["found"] != true || !strings.Contains(out["note"].(string), "tablo belgesi değil") {
		t.Fatalf("tablo dışı: %v", out)
	}
}

func TestReadWikiPageFindArgAndTableHint(t *testing.T) {
	rec := wikiTableFixture(100, 10)
	src := &fakeWikiSource{page: &rec}
	tool := readWikiPageTool(Deps{Wiki: src})
	ctx := context.Background()
	got, err := tool.Handler(ctx, json.RawMessage(`{"project":"p","wiki":"w","path":"/vserver_cc_iliskisi.xlsx","find":"PlatformTeam"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); m["matched_rows"] != 10 {
		t.Fatalf("find: %v", m["matched_rows"])
	}
	got, err = tool.Handler(ctx, json.RawMessage(`{"project":"p","wiki":"w","path":"/vserver_cc_iliskisi.xlsx"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); !strings.Contains(fmt.Sprint(m["table_hint"]), "find=") || m["content"] == nil {
		t.Fatalf("ipucu: %v", m["table_hint"])
	}
}
