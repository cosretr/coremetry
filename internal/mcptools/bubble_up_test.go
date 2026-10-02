package mcptools

// v0.10.993 — dış skill denetimi V1 dilim 3: bubble_up MCP aracı. Argüman
// normalleştirme ve zarf saf ve tablo testli; aracın YALNIZ dış MCP'ye
// açıldığı (sohbet kataloğu bayt bayt eski hâlinde) ayrıca pinli.

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

func TestNormalizeBubbleUpArgs(t *testing.T) {
	cases := []struct {
		name       string
		in         bubbleUpArgs
		wantErr    string
		wantSubset bool
		wantRange  int
		wantLimit  int
	}{
		{"varsayılanlar: errors, 600 sn, 5", bubbleUpArgs{Service: " checkout "}, "", true, 600, 5},
		{"previous", bubbleUpArgs{Service: "checkout", Compare: "Previous"}, "", false, 600, 5},
		{"errors açıkça", bubbleUpArgs{Service: "checkout", Compare: "errors", RangeS: 900, Limit: 3}, "", true, 900, 3},
		{"range alt sınıra çıkar", bubbleUpArgs{Service: "checkout", RangeS: 60}, "", true, 300, 5},
		{"range tavana iner (7 gün istenmiş)", bubbleUpArgs{Service: "checkout", RangeS: 604800}, "", true, 3600, 5},
		{"negatif range → varsayılan", bubbleUpArgs{Service: "checkout", RangeS: -5}, "", true, 600, 5},
		{"limit tavanı 10", bubbleUpArgs{Service: "checkout", Limit: 500}, "", true, 600, 10},
		{"servis zorunlu", bubbleUpArgs{Service: "  "}, "service gerekli", false, 0, 0},
		{"bilinmeyen compare", bubbleUpArgs{Service: "checkout", Compare: "slow"}, `compare "slow"`, false, 0, 0},
	}
	for _, c := range cases {
		svc, subset, rangeS, limit, err := normalizeBubbleUpArgs(c.in)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: hata %q beklenir, gelen %v", c.name, c.wantErr, err)
			}
			continue
		}
		if err != nil || svc != "checkout" || subset != c.wantSubset || rangeS != c.wantRange || limit != c.wantLimit {
			t.Errorf("%s: svc=%q subset=%v range=%d limit=%d err=%v", c.name, svc, subset, rangeS, limit, err)
		}
	}
	// Maliyet sözleşmesi: ham spans taraması en çok 1 saat.
	if bubbleUpMaxRangeS != 3600 || bubbleUpMinRangeS != 300 {
		t.Errorf("pencere kelepçesi [300, 3600] kalmalı: [%d, %d]", bubbleUpMinRangeS, bubbleUpMaxRangeS)
	}
}

// buToolVal — chstore'un gerçek birimi: pay = sayım / toplam (oran).
func buToolVal(value string, sel, selTotal, base, baseTotal int64) chstore.BubbleUpValue {
	sp, bp := float64(sel)/float64(selTotal), float64(base)/float64(baseTotal)
	return chstore.BubbleUpValue{Value: value, SelectionCount: sel, BaselineCount: base, SelectionPct: sp, BaselinePct: bp, Score: sp - bp}
}

func TestBubbleUpEnvelope(t *testing.T) {
	attrs := []chstore.BubbleUpAttribute{
		{Key: "http.route", Values: []chstore.BubbleUpValue{buToolVal("/v1/pay-now", 32, 40, 99, 900)}},
		{Key: "k8s.pod.name", Values: []chstore.BubbleUpValue{buToolVal("api-gw-7f", 24, 40, 270, 900)}},
		{Key: "noise", Values: []chstore.BubbleUpValue{buToolVal("x", 5, 40, 90, 900)}}, // 2,5 puan → girmez
		{Key: "service.version", Values: []chstore.BubbleUpValue{buToolVal("2.4.1", 20, 40, 90, 900)}},
	}
	bu := &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: attrs}

	out := bubbleUpEnvelope("checkout", true, 600, 5, bu)
	rows := out["attributes"].([]bubbleUpRow)
	if out["compare"] != "errors_vs_all" || out["window_s"] != 600 || out["count"] != 3 || out["has_more"] != false ||
		out["selection_total"] != int64(40) || out["baseline_total"] != int64(900) || out["min_diff_pct"] != 5.0 {
		t.Fatalf("zarf: %+v", out)
	}
	if _, has := out["note"]; has {
		t.Errorf("satır varken not olmamalı: %v", out["note"])
	}
	want := bubbleUpRow{Key: "http.route", Value: "/v1/pay-now", SelectionPct: 80, BaselinePct: 11, DiffPct: 69, SelectionCount: 32, BaselineCount: 99}
	if rows[0] != want {
		t.Errorf("yüzdeler 0–100 ve tek ondalık: %+v", rows[0])
	}
	if rows[1].Key != "k8s.pod.name" || rows[2].Key != "service.version" {
		t.Errorf("sıra puana göre, eşik altı (noise) yok: %+v", rows)
	}

	// limit < nitelikli sayı → has_more (sessiz kırpma yok, v0.10.407 doktrini).
	out = bubbleUpEnvelope("checkout", false, 900, 2, bu)
	if out["compare"] != "window_vs_previous" || out["count"] != 2 || out["has_more"] != true {
		t.Errorf("limit 2: %+v", out)
	}

	for _, c := range []struct {
		name   string
		subset bool
		bu     *chstore.BubbleUpResult
		note   string
	}{
		{"hatalı span yok", true, &chstore.BubbleUpResult{BaselineTotal: 900, Attributes: attrs}, "no error spans"},
		{"pencerede span yok", false, &chstore.BubbleUpResult{BaselineTotal: 900}, "no spans for this service in the window"},
		{"önceki pencere boş", false, &chstore.BubbleUpResult{SelectionTotal: 40}, "no spans in the previous window"},
		{"ayrışma yok", true, &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: attrs[2:3]}, "does not concentrate"},
		{"nil sonuç", true, nil, "no result"},
	} {
		out := bubbleUpEnvelope("checkout", c.subset, 600, 5, c.bu)
		note, _ := out["note"].(string)
		if !strings.Contains(note, c.note) || out["count"] != 0 || len(out["attributes"].([]bubbleUpRow)) != 0 {
			t.Errorf("%s: not %q, zarf %+v", c.name, note, out)
		}
	}

	long := &chstore.BubbleUpResult{SelectionTotal: 10, BaselineTotal: 100, Attributes: []chstore.BubbleUpAttribute{
		{Key: "db.statement", Values: []chstore.BubbleUpValue{buToolVal(strings.Repeat("ş", 500), 9, 10, 10, 100)}},
	}}
	if v := bubbleUpEnvelope("s", true, 600, 5, long)["attributes"].([]bubbleUpRow)[0].Value; len([]rune(v)) > bubbleUpValueMax+1 {
		t.Errorf("değer kırpılmalı: %d rune", len([]rune(v)))
	}
}

// Şema ↔ argüman yapısı birebir (mcp-tools skill adım 7): eşleşmeyen alan
// sessizce sıfır değere çözülür.
func TestBubbleUpSchemaMirrorsArgs(t *testing.T) {
	tool := bubbleUpTool(Deps{})
	props := tool.InputSchema["properties"].(map[string]any)
	var schema, fields []string
	for k := range props {
		schema = append(schema, k)
	}
	rt := reflect.TypeOf(bubbleUpArgs{})
	for i := 0; i < rt.NumField(); i++ {
		fields = append(fields, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(schema)
	sort.Strings(fields)
	if !reflect.DeepEqual(schema, fields) {
		t.Errorf("şema %v ≠ argümanlar %v", schema, fields)
	}
	if req, _ := tool.InputSchema["required"].([]string); len(req) != 1 || req[0] != "service" {
		t.Errorf("yalnız service zorunlu: %v", tool.InputSchema["required"])
	}
	// Maliyet dürüstlüğü: ham spans taraması söylenir, ucuzluk iddiası yok.
	if !strings.Contains(tool.Description, "scans raw spans") || !strings.Contains(tool.Description, "15 s") {
		t.Error("açıklama ham spans taramasını ve 15 sn tavanını söylemeli")
	}
}

// bubble_up YALNIZ dış MCP'de: Register kaydeder, sohbet kataloğu
// (ChatToolList) görmez ve geri kalanı ToolList ile aynı sırada taşır.
func TestBubbleUpIsExternalOnly(t *testing.T) {
	// v0.10.1050 — koşullu araçlar (chatOffered) bağımlılıkları olmadan sohbete
	// sunulmaz: Deps{} ile read_source_code da düşer; sayı ve sıra ondan arındırılır.
	all, chat := ToolList(Deps{}), ChatToolList(Deps{})
	absent := 0
	for _, tl := range all {
		if !externalOnlyTools[tl.Name] && !chatOffered(Deps{}, tl.Name) {
			absent++
		}
	}
	if absent != 1 {
		t.Fatalf("Deps{} ile sunulmayan koşullu araç sayısı %d (beklenen 1: read_source_code)", absent)
	}
	if len(chat) != len(all)-len(externalOnlyTools)-absent {
		t.Fatalf("sohbet kataloğu %d, tam katalog %d, dış-yalnız %d, koşullu-yok %d", len(chat), len(all), len(externalOnlyTools), absent)
	}
	inAll := map[string]bool{}
	for _, tl := range all {
		inAll[tl.Name] = true
	}
	for name := range externalOnlyTools {
		if !inAll[name] {
			t.Errorf("externalOnlyTools %q ToolList'te yok — ad bayat", name)
		}
		if chatOnlyTools[name] {
			t.Errorf("%q hem sohbet-yalnız hem dış-yalnız olamaz", name)
		}
	}
	i := 0
	for _, tl := range all {
		if externalOnlyTools[tl.Name] || !chatOffered(Deps{}, tl.Name) {
			continue
		}
		if chat[i].Name != tl.Name {
			t.Fatalf("sohbet kataloğu sırası ToolList'ten sapıyor: %d. %q ≠ %q", i, chat[i].Name, tl.Name)
		}
		i++
	}
	for _, tl := range chat {
		if tl.Name == "bubble_up" {
			t.Fatal("bubble_up sohbet kataloğunda — küçük modele prefetch adımı olarak gider, tool olarak değil")
		}
	}
	srv := mcp.New("coremetry", "test")
	Register(srv, Deps{})
	if got, want := srv.ToolCount(), len(all)-len(chatOnlyTools); got != want {
		t.Errorf("dış MCP %d tool kaydetmeli (bubble_up dahil), kayıtlı %d", want, got)
	}
	// Sohbet yolları tam kataloğu DEĞİL ChatToolList'i okur (kaynak pini).
	for _, f := range []string{"../api/copilot_chat.go", "../api/trace_investigate.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if src := string(b); !strings.Contains(src, "mcptools.ChatToolList(s.mcpDeps())") || strings.Contains(src, "mcptools.ToolList(") {
			t.Errorf("%s ChatToolList okumalı", f)
		}
	}
}
