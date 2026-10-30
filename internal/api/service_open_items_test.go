package api

// v0.10.1694 (operator-reported) — CoSRE "açık problemler" ↔ servis sayfası
// şeridi paritesi. Belirti: şerit 13 açık konu (exception + httperror
// grupları) gösterirken service_health_sweep yalnız problems tablosunu okudu
// ve "veri yok" dedi. Tarama artık inboxOpen'ı Attention ile okur: /api/inbox
// ile AYNI inboxView + önbellek girdisi + şeridin süzgeci (serviceAttentionItems).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func openItemsFixture() []InboxItem {
	return []InboxItem{
		{ID: "exception:fp1", Kind: "exception", Priority: "P1", Service: "orders-api", Title: "java.net.SocketTimeoutException", Status: "open",
			Exception: &InboxExceptionRef{Fingerprint: "fp1"}},
		{ID: "httperror:fp2", Kind: "httperror", Priority: "P2", Service: "orders-api", Title: "HTTP 503 GET /orders", Status: "open",
			Exception: &InboxExceptionRef{Fingerprint: "fp2"}},
		{ID: "problem:p1", Kind: "problem", Priority: "P3", Service: "orders-api", Title: "SLO burn", Status: "open",
			Problem: &InboxProblemRef{ID: "p1", RuleID: "slo:s1:fast"}},
		{ID: "anomaly:a1", Kind: "anomaly", Priority: "P3", Service: "orders-api", Title: "log spike",
			Anomaly: &InboxAnomalyRef{ID: "a1", Kind: "log_pattern", Pattern: "timeout"}},
		{ID: "exception:fp3", Kind: "exception", Priority: "P3", Service: "orders-api", Title: "java.lang.IllegalStateException", Status: "open",
			Exception: &InboxExceptionRef{Fingerprint: "fp3"}},
		{ID: "problem:p2", Kind: "problem", Priority: "P3", Service: "orders-api", Title: "error rate", Status: "open",
			Problem: &InboxProblemRef{ID: "p2", RuleID: "r-err"}},
	}
}

func TestServiceAttentionItems(t *testing.T) {
	for _, c := range []struct {
		name    string
		noise   map[string]struct{}
		wantIDs string
		wantSLO int
	}{
		{"karar yok: SLO dipnot, anomali dışarıda", nil, "exception:fp1,httperror:fp2,exception:fp3,problem:p2", 1},
		{"problem değil: exception imzası gizlenir", map[string]struct{}{"e:fp2": {}}, "exception:fp1,exception:fp3,problem:p2", 1},
		{"problem değil: kural imzası gizlenir", map[string]struct{}{"p:r-err|orders-api": {}}, "exception:fp1,httperror:fp2,exception:fp3", 1},
		{"problem değil: SLO kuralı sayıdan da düşer", map[string]struct{}{"p:slo:s1:fast|orders-api": {}}, "exception:fp1,httperror:fp2,exception:fp3,problem:p2", 0},
	} {
		kept, slo := serviceAttentionItems(openItemsFixture(), c.noise)
		ids := make([]string, 0, len(kept))
		for _, it := range kept {
			ids = append(ids, it.ID)
		}
		if got := strings.Join(ids, ","); got != c.wantIDs || slo != c.wantSLO {
			t.Errorf("%s: %s slo=%d, want %s slo=%d", c.name, got, slo, c.wantIDs, c.wantSLO)
		}
	}
	if got := inboxItemVerdictSignature(InboxItem{Kind: "anomaly", Service: "s", Anomaly: &InboxAnomalyRef{Kind: "log_pattern", Pattern: "p"}}); got != "a:log_pattern|s|p" {
		t.Errorf("anomali imzası %q", got)
	}
	if got := inboxItemVerdictSignature(InboxItem{Kind: "incident", Incident: &InboxIncidentRef{ID: "i"}}); got != "" {
		t.Errorf("incident imzası %q, want boş", got)
	}
}

// TestServiceOpenItemsParityWithInbox — aynı girdi: /api/inbox handler'ının
// şerit sorgusuna verdiği gövdeden şeridin saydığı (serviceAttentionItems)
// == service_health_sweep'in raporladığı. 0 problem satırı + exception/
// httperror kalemleri taramada görünmeli.
func TestServiceOpenItemsParityWithInbox(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	body, err := json.Marshal(map[string]any{"items": openItemsFixture(), "total": 6, "limit": 20, "truncated": false})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := s.inboxView(inboxOpenValues(mcptools.ServiceAttentionQuery("orders-api")))
	s.l1.set(key, body, time.Minute)

	// Handler yolu — şeridin isteği birebir.
	rec := httptest.NewRecorder()
	s.inbox(rec, httptest.NewRequest(http.MethodGet, "/api/inbox?service=orders-api&status=open&limit=20", nil))
	var view serviceOpenItemsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("handler gövdesi: %v (%s)", err, rec.Body.String())
	}
	stripRows, _ := serviceAttentionItems(view.Items, nil)

	got, err := s.inboxOpen(context.Background(), mcptools.ServiceAttentionQuery("orders-api"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != len(stripRows) || len(got.Rows) != 4 || got.Total != 4 || got.SLOCount != 1 {
		t.Fatalf("parite: sweep %d (rows %d, slo %d), şerit %d (want 4)", got.Total, len(got.Rows), got.SLOCount, len(stripRows))
	}

	res, err := mcptools.ReadServiceSweep(context.Background(), mcptools.Deps{InboxOpen: s.inboxOpen},
		mcptools.SweepRequest{Service: "orders-api", Window: "1h", From: time.Now().Add(-time.Hour), To: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var sum string
	for _, sec := range res.Sections {
		if sec.Name == "problems" {
			sum = sec.Summary
		}
	}
	if !strings.HasPrefix(sum, "4 açık konu:") || !strings.Contains(sum, "SocketTimeoutException") {
		t.Errorf("tarama özeti %q, want \"4 açık konu: …\"", sum)
	}
}

// TestServiceAttentionStripQueryPinned — şeridin sorgusu değişirse taramanın
// sorgusu da değişmeli (mcptools.ServiceAttentionQuery); ikisi ayrışırsa sayı ayrışır.
func TestServiceAttentionStripQueryPinned(t *testing.T) {
	src, err := os.ReadFile("../../frontend/src/components/ServiceAttentionStrip.tsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "api.inbox({ service, status: 'open', limit: 20 })") {
		t.Error("ServiceAttentionStrip sorgusu değişti: mcptools.ServiceAttentionQuery'yi aynı güncelle")
	}
	if !strings.Contains(string(src), "filterByVerdictView(q.data.items, verdictIndex(verdictsQ.data?.verdicts), 'triage')") {
		t.Error("şeridin karar süzgeci değişti: serviceAttentionItems'ı aynı güncelle")
	}
	if q := inboxOpenValues(mcptools.ServiceAttentionQuery("orders-api")); q.Encode() != "limit=20&service=orders-api&status=open" {
		t.Errorf("sorgu %q", q.Encode())
	}
	if h := mcptools.ServiceInboxHref("orders-api"); h != "/inbox?service=orders-api&prio=P1,P2,P3&env=" {
		t.Errorf("href %q", h)
	}
	if !strings.Contains(string(src), "serviceInboxHref(service)") {
		t.Error("şeridin bağlantısı değişti: mcptools.ServiceInboxHref'i aynı güncelle")
	}
}
