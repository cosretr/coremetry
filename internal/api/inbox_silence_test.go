package api

// inbox_silence_test.go — v0.10.1042 regresyon testi.
//
// Operatör: "Anomalide 'Mute' sonrası satır listeden düşsün." Problems
// sayfasında (birleşik kuyruk, /api/inbox) anomali detayından Mute… yazılıyor,
// sayfa kuyruğa dönüyor ve satır HÂLÂ orada: inbox satırları aktif
// anomaly_events'ten kuruluyordu, rozet SQL COUNT'tan — ikisi de susturmalara
// bakmıyordu; susturma yazım uçları da yalnız "anomaly:" önbelleğini
// düşürüyordu.
//
// Sözleşme:
//   - open görünümü: aktif susturmalı anomali satırı listelenmez ve sayılmaz
//     (liste, kind/prio çipleri, total, kenar çubuğu rozeti AYNI küme);
//   - all görünümü: satır kalır, durumu "muted";
//   - susturma okunamazsa hiçbir şey gizlenmez (okunamayan süzgeç süzmez);
//   - anomali dışı satırlara dokunulmaz;
//   - susturma yazımı/silinmesi inbox önbelleğini düşürür — bir sonraki
//     istek mute öncesi gövdeyi alamaz.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	silFpMuted   = "a1b2c3d4e5f60718" // aktif susturması var
	silFpMuted2  = "0f1e2d3c4b5a6978" // aktif susturması var (cleared olay)
	silFpVisible = "1111222233334444" // susturması yok
)

// silenceFixture — karışık kuyruk: iki susturulmuş anomali (biri cleared),
// bir susturulmamış anomali, ve kimliği susturulmuş parmak iziyle ÇAKIŞAN
// anomali-dışı satırlar (eşleşme türe bağlı olmalı, kimlik metnine değil).
func silenceFixture() []InboxItem {
	an := func(fp, status, prio string) InboxItem {
		it := anomalyToInbox(chstore.AnomalyEvent{
			ID: fp, Kind: "log_pattern", Pattern: "ORA-00001", Service: "svc-a",
			Status: status, PeakRatio: 6,
		})
		it.Priority = prio
		return it
	}
	return []InboxItem{
		{ID: "problem:" + silFpMuted, Kind: "problem", Priority: "P3", Status: "open",
			Problem: &InboxProblemRef{ID: silFpMuted}},
		an(silFpMuted, "active", "P3"),
		{ID: "exception:" + silFpMuted, Kind: "exception", Priority: "P1", Status: "new",
			Exception: &InboxExceptionRef{Fingerprint: silFpMuted, Occurrences: 50}},
		an(silFpVisible, "active", "P3"),
		an(silFpMuted2, "cleared", "P3"),
		{ID: "incident:i1", Kind: "incident", Priority: "P2", Status: "open"},
		// Ref'siz anomali satırı (savunmacı): susturma anahtarı yok → dokunulmaz.
		{ID: "anomaly:" + silFpMuted, Kind: "anomaly", Priority: "P3", Status: "active"},
	}
}

func silenceSet() map[string]bool {
	return map[string]bool{silFpMuted: true, silFpMuted2: true}
}

type silRow struct{ id, status string }

func silRows(items []InboxItem) []silRow {
	out := make([]silRow, 0, len(items))
	for _, it := range items {
		out = append(out, silRow{it.ID, it.Status})
	}
	return out
}

func TestApplyInboxAnomalySilences(t *testing.T) {
	cases := []struct {
		name   string
		muted  map[string]bool
		status string
		want   []silRow
	}{
		{
			name:  "open görünüm: susturulmuş anomali düşer, diğerleri aynen",
			muted: silenceSet(), status: "open",
			want: []silRow{
				{"problem:" + silFpMuted, "open"},
				{"exception:" + silFpMuted, "new"},
				{"anomaly:" + silFpVisible, "active"},
				{"incident:i1", "open"},
				{"anomaly:" + silFpMuted, "active"}, // ref'siz — dokunulmaz
			},
		},
		{
			name:  "all görünüm: susturulmuş anomali kalır, durumu muted (cleared olan da)",
			muted: silenceSet(), status: "all",
			want: []silRow{
				{"problem:" + silFpMuted, "open"},
				{"anomaly:" + silFpMuted, "muted"},
				{"exception:" + silFpMuted, "new"},
				{"anomaly:" + silFpVisible, "active"},
				{"anomaly:" + silFpMuted2, "muted"},
				{"incident:i1", "open"},
				{"anomaly:" + silFpMuted, "active"},
			},
		},
		{
			name:  "susturma okunamadı (nil): open görünümde hiçbir şey gizlenmez",
			muted: nil, status: "open",
			want: silRows(silenceFixture()),
		},
		{
			name:  "susturma okunamadı (nil): all görünümde damga yok",
			muted: nil, status: "all",
			want: silRows(silenceFixture()),
		},
		{
			name:  "aktif susturma yok (boş küme): dokunulmaz",
			muted: map[string]bool{}, status: "open",
			want: silRows(silenceFixture()),
		},
		{
			name:  "tanınmayan pivot open gibi davranır (savunmacı)",
			muted: map[string]bool{silFpMuted: true}, status: "",
			want: []silRow{
				{"problem:" + silFpMuted, "open"},
				{"exception:" + silFpMuted, "new"},
				{"anomaly:" + silFpVisible, "active"},
				{"anomaly:" + silFpMuted2, "cleared"},
				{"incident:i1", "open"},
				{"anomaly:" + silFpMuted, "active"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := silRows(applyInboxAnomalySilences(silenceFixture(), c.muted, c.status))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("applyInboxAnomalySilences(%q)\n got  %v\n want %v", c.status, got, c.want)
			}
		})
	}
}

// Anomali dışı satırların HİÇBİR alanı değişmez (yalnız id/status değil).
func TestApplyInboxAnomalySilencesLeavesOtherKindsIntact(t *testing.T) {
	for _, status := range []string{"open", "all"} {
		want := map[string]InboxItem{}
		for _, it := range silenceFixture() {
			if it.Kind != "anomaly" {
				want[it.ID] = it
			}
		}
		for _, it := range applyInboxAnomalySilences(silenceFixture(), silenceSet(), status) {
			if it.Kind == "anomaly" {
				continue
			}
			if !reflect.DeepEqual(it, want[it.ID]) {
				t.Errorf("%s: anomali dışı satır değişti: %+v → %+v", status, want[it.ID], it)
			}
			delete(want, it.ID)
		}
		if len(want) != 0 {
			t.Errorf("%s: anomali dışı satırlar düştü: %v", status, want)
		}
	}
}

func TestInboxAnomalyExcludeIDs(t *testing.T) {
	muted := map[string]bool{"cc": true, "aa": true, "bb": false, "dd": true}
	if got := inboxAnomalyExcludeIDs(muted, "open"); !reflect.DeepEqual(got, []string{"aa", "cc", "dd"}) {
		t.Errorf("open: %v, sıralı aktif küme bekleniyordu (false değer elenir)", got)
	}
	if got := inboxAnomalyExcludeIDs(muted, "all"); got != nil {
		t.Errorf("all: %v — o görünüm susturulmuşu GÖSTERİR, SQL elemesi olmamalı", got)
	}
	if got := inboxAnomalyExcludeIDs(nil, "open"); got != nil {
		t.Errorf("okuma hatası (nil): %v — okunamayan süzgeç süzmez", got)
	}
	if got := inboxAnomalyExcludeIDs(muted, ""); len(got) != 3 {
		t.Errorf("tanınmayan pivot open gibi: %v", got)
	}
}

// Liste ile sayaçlar AYNI kümeyi söyler. Liste: SQL elemesi
// (inboxAnomalyExcludeIDs → ExcludeIDs, LIMIT'ten önce) + Go bekçisi; çipler
// (inboxFacetCounts) ve total bekçiden SONRAKİ satırlardan; rozet:
// CountActiveAnomalyEvents aynı eleme kümesiyle (`id NOT IN`). Burada SQL
// tarafı Go'da taklit edilir — iki SQL'in AYNI yüklemi kullandığını
// chstore/anomaly_silence_exclude_test.go çiviler.
func TestInboxSilenceListAndCountersAgree(t *testing.T) {
	events := []chstore.AnomalyEvent{
		{ID: silFpMuted, Kind: "log_pattern", Pattern: "p1", Service: "svc-a", Status: "active", PeakRatio: 6},
		{ID: silFpVisible, Kind: "trace_op", Pattern: "GET /x", Service: "svc-b", Status: "active", PeakRatio: 3},
		{ID: "5555666677778888", Kind: "trace_op", Pattern: "GET /y", Service: "svc-c", Status: "active", PeakRatio: 1.2},
		{ID: silFpMuted2, Kind: "log_pattern", Pattern: "p2", Service: "svc-d", Status: "active", PeakRatio: 9},
		{ID: "9999aaaabbbbcccc", Kind: "log_pattern", Pattern: "p3", Service: "svc-e", Status: "cleared", PeakRatio: 4},
	}
	muted := silenceSet()
	other := []InboxItem{
		{ID: "problem:p", Kind: "problem", Priority: "P3"},
		{ID: "exception:e", Kind: "exception", Priority: "P1"},
	}
	sqlKeeps := func(excl []string, e chstore.AnomalyEvent) bool {
		for _, id := range excl {
			if id == e.ID {
				return false
			}
		}
		return true
	}

	// ── open görünümü — handler'ın sırası: SQL (ActiveOnly + ExcludeIDs) →
	// satır eşleme → bekçi → forceNonExceptionP3 → sayaçlar.
	excl := inboxAnomalyExcludeIDs(muted, "open")
	items := append([]InboxItem(nil), other...)
	badge := 0
	for _, e := range events {
		if e.Status != "active" {
			continue
		}
		if !sqlKeeps(excl, e) {
			continue
		}
		badge++ // CountActiveAnomalyEvents: aktif VE id NOT IN excl
		items = append(items, anomalyToInbox(e))
	}
	before := len(items)
	items = applyInboxAnomalySilences(items, muted, "open")
	if len(items) != before {
		t.Fatalf("open: SQL elemesinden sonra bekçi %d satır daha düşürdü — LIMIT sonrası süzgeç olurdu", before-len(items))
	}
	forceNonExceptionP3(items)
	counts := inboxFacetCounts(items)
	listed := 0
	for _, it := range items {
		if it.Kind == "anomaly" {
			listed++
			if muted[it.Anomaly.ID] {
				t.Errorf("open: susturulmuş anomali listede: %s", it.ID)
			}
		}
	}
	if listed != 2 {
		t.Errorf("open: %d anomali satırı, 2 bekleniyordu (svc-b + svc-c)", listed)
	}
	if counts["anomaly"] != listed {
		t.Errorf("open: anomali çipi %d, liste %d — çip olmayan satırı vaat ediyor", counts["anomaly"], listed)
	}
	if badge != listed {
		t.Errorf("open: rozet anomali terimi %d, liste %d — kenar çubuğu sayfadan farklı", badge, listed)
	}
	if total := len(items); counts["P1"]+counts["P2"]+counts["P3"] != total {
		t.Errorf("open: öncelik çipleri toplamı %d, total %d", counts["P1"]+counts["P2"]+counts["P3"], total)
	}

	// ── all görünümü — SQL elemesi YOK, susturulmuş satır damgalı listelenir
	// ve sayılır (liste ile çip yine aynı küme).
	if x := inboxAnomalyExcludeIDs(muted, "all"); x != nil {
		t.Fatalf("all: SQL elemesi %v — görünüm susturulmuşu göstermeli", x)
	}
	all := append([]InboxItem(nil), other...)
	for _, e := range events {
		all = append(all, anomalyToInbox(e))
	}
	all = applyInboxAnomalySilences(all, muted, "all")
	c := inboxFacetCounts(all)
	mutedN, anomN := 0, 0
	for _, it := range all {
		if it.Kind != "anomaly" {
			continue
		}
		anomN++
		if it.Status == inboxMutedStatus {
			mutedN++
		}
	}
	if anomN != len(events) || c["anomaly"] != anomN {
		t.Errorf("all: %d anomali satırı, çip %d; beklenen ikisi de %d", anomN, c["anomaly"], len(events))
	}
	if mutedN != 2 {
		t.Errorf("all: %d satır muted damgalı, 2 bekleniyordu", mutedN)
	}
}

// Kaynak pinleri — saf işlevlerin handler'da DOĞRU yerde ve TEK okumayla
// kullanıldığını sabitler (CH'siz test edilemeyen kısım).
func TestInboxSilenceWiring(t *testing.T) {
	src := readSrc(t, "inbox.go")
	h := funcBody(src, "inbox")
	if h == "" {
		t.Fatal("inbox handler gövdesi bulunamadı")
	}
	// Tek okuma, satır döngüsünün DIŞINDA.
	if n := strings.Count(h, `s.activeSilencedAnomalies(ctx, "inbox list")`); n != 1 {
		t.Errorf("liste derlemesinde susturma okuması %d kez, 1 bekleniyordu", n)
	}
	loop := strings.Index(h, "for _, e := range evs {")
	read := strings.Index(h, `s.activeSilencedAnomalies(ctx, "inbox list")`)
	if loop < 0 || read < 0 || read > loop {
		t.Error("susturma okuması anomali satır döngüsünden ÖNCE olmalı (satır başına okuma yok)")
	}
	// SQL elemesi LIMIT'li sorguya iner.
	if !strings.Contains(h, "ExcludeIDs: inboxAnomalyExcludeIDs(muted, statusFilter)") {
		t.Error("open görünümün susturma elemesi ListAnomalyEvents'e inmiyor — Go'da LIMIT sonrası süzgeç olurdu")
	}
	// Bekçi sayaçlardan ÖNCE.
	guard := strings.Index(h, "items = applyInboxAnomalySilences(items, muted, statusFilter)")
	counts := strings.Index(h, "counts := inboxFacetCounts(items)")
	// v0.10.1081 — total artık inboxSortAndCap'in dönüşü.
	total := strings.Index(h, "items, total := inboxSortAndCap(items, sortID, sortDir, limit)")
	if guard < 0 || counts < 0 || total < 0 || guard > counts || guard > total {
		t.Error("applyInboxAnomalySilences çip sayaçlarından ve total'den ÖNCE çağrılmalı — yoksa çip olmayan satırı sayar")
	}
	// Rozet aynı kümeyi aynı SQL yükleminden eler.
	cnt := funcBody(src, "computeInboxCountFor")
	if !strings.Contains(cnt, `inboxAnomalyExcludeIDs(s.activeSilencedAnomalies(ctx, "inbox count"), "open")`) ||
		!strings.Contains(cnt, "s.store.CountActiveAnomalyEvents(gctx, 0, envServices, badgeMuted)") {
		t.Error("rozet susturulmuş anomalileri listeyle aynı kümeden elemiyor — kenar çubuğu sayfadan büyük olur")
	}
	// Okuma tek kapıdan: yumuşak-hata yönü tek yerde. inbox.go ve api.go
	// (canlı uçlar) depoyu DOĞRUDAN okumaz; kapı anomaly_extra.go'da.
	if n := strings.Count(src, "s.store.ActiveSilencedFingerprints("); n != 0 {
		t.Errorf("inbox.go depoyu %d kez doğrudan okuyor — tek kapı activeSilencedAnomalies", n)
	}
	apiSrc := readSrc(t, "api.go")
	if n := strings.Count(apiSrc, "s.store.ActiveSilencedFingerprints("); n != 0 {
		t.Errorf("api.go depoyu %d kez doğrudan okuyor — canlı uçlar hatayı yine yutar", n)
	}
	for _, w := range []string{
		`muted := s.activeSilencedAnomalies(ctx, "trace-ops")`,
		`muted := s.activeSilencedAnomalies(ctx, "log-patterns")`,
	} {
		if !strings.Contains(apiSrc, w) {
			t.Errorf("api.go canlı ucu kapıdan geçmiyor: %q", w)
		}
	}
	extra := readSrc(t, "anomaly_extra.go")
	if n := strings.Count(extra, "s.store.ActiveSilencedFingerprints(ctx)"); n != 1 {
		t.Errorf("anomaly_extra.go'da %d okuma — tek kapı bekleniyordu", n)
	}
	reader := funcBody(extra, "activeSilencedAnomalies")
	if !strings.Contains(reader, "return nil") || !strings.Contains(reader, "log.Printf(") {
		t.Error("okuma hatası nil + tek log dönmeli (okunamayan süzgeç süzmez)")
	}
}

// ── Önbellek ─────────────────────────────────────────────────────────────

// prefixMemCache — Get/Set/DelPrefix çalışan cache.Cache (memCache +
// gerçek önek silme). Redis'in SCAN+UNLINK'ine karşılık.
type prefixMemCache struct{ *memCache }

func (c prefixMemCache) DelPrefix(_ context.Context, prefix string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
	return nil
}

// Susturma yazımından sonra inbox listesi ve rozet önbellekten mute öncesi
// gövdeyi DÖNMEZ: bir sonraki okuma upstream'e gider (MISS). Emsal: ack /
// exception durumu / incident yazımlarının açık önek düşürmesi
// (inboxListCachePrefix). L1 ve L2 ikisi birden.
func TestSilenceWriteDropsCachedInboxPayload(t *testing.T) {
	ctx := context.Background()
	s := &Server{cache: prefixMemCache{newMemCache()}, l1: newL1Cache(64), stats: newCacheStats()}
	const ttl = 15 * time.Second
	listKey := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5,
		inboxKindsAll, inboxPriosAll, inboxSubjectService) + ":since=:cat=:floorDefault=true"
	keys := []string{listKey, inboxCountKey(""), inboxCountKey("prod"), "anomaly:log-patterns:window=5m"}

	pre, _ := json.Marshal(map[string]any{"items": []string{"anomaly:" + silFpMuted}})
	for _, k := range keys {
		s.storeCached(ctx, k, pre, ttl)
	}
	upstream := 0
	fresh := func(context.Context) (any, error) {
		upstream++
		return map[string]any{"items": []string{}}, nil
	}
	// Ön koşul: önbellek sıcak — düşürme olmadan mute öncesi gövde döner.
	if body, tier, err := s.cachedJSON(ctx, listKey, ttl, false, fresh); err != nil || tier == "MISS" ||
		!strings.Contains(string(body), silFpMuted) {
		t.Fatalf("ön koşul: sıcak önbellek bekleniyordu (tier=%s err=%v)", tier, err)
	}
	if upstream != 0 {
		t.Fatal("ön koşul: sıcak okuma upstream'e gitmemeli")
	}

	// Yazım uçlarının (oluştur / sil / toplu sil) çağırdığı düşürme.
	s.invalidateSilenceReaders(ctx)

	for _, k := range keys {
		body, tier, err := s.cachedJSON(ctx, k, ttl, false, fresh)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if tier != "MISS" {
			t.Errorf("%s: tier=%s — susturma yazımından sonra önbellekten servis edildi", k, tier)
		}
		if strings.Contains(string(body), silFpMuted) {
			t.Errorf("%s: mute öncesi gövde döndü: %s", k, body)
		}
	}
	if upstream != len(keys) {
		t.Errorf("upstream %d kez, %d bekleniyordu", upstream, len(keys))
	}
}

// Üç yazım ucu da düşürmeyi STORE yazımından SONRA çağırır.
func TestSilenceHandlersInvalidateInbox(t *testing.T) {
	src := readSrc(t, "anomaly_extra.go")
	for fn, write := range map[string]string{
		"createAnomalySilence":      "s.store.UpsertAnomalySilence(",
		"deleteAnomalySilence":      "s.store.DeleteAnomalySilence(",
		"bulkDeleteAnomalySilences": "s.store.DeleteAnomalySilences(",
	} {
		body := funcBody(src, fn)
		w, inv := strings.Index(body, write), strings.Index(body, "s.invalidateSilenceReaders(r.Context())")
		if w < 0 || inv < 0 || inv < w {
			t.Errorf("%s: invalidateSilenceReaders store yazımından SONRA çağrılmalı (write=%d inv=%d)", fn, w, inv)
		}
	}
	helper := funcBody(src, "invalidateSilenceReaders")
	for _, want := range []string{
		`s.cacheInvalidatePrefix(ctx, "anomaly:")`,
		"s.cacheInvalidatePrefix(ctx, inboxListCachePrefix)",
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("invalidateSilenceReaders %q düşürmüyor", want)
		}
	}
}
