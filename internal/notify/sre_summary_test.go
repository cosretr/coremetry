package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func sufficientDigest() *InvestigationDigest {
	return &InvestigationDigest{
		InvestigationID: "sre-p-1", ProblemID: "p-1", State: "done", Finished: true, Sufficient: true,
		TopKind: "deploy", TopLabel: "Deploy",
		Evidence: []DigestEvidence{{Check: "deploy_near", Source: "deploys"}, {Check: "after_deploy", Source: "sweep"}},
		Blind:    []DigestReach{{Source: "pivot", Status: "timed_out"}},
	}
}

func TestSREBlockLinesTR(t *testing.T) {
	got := strings.Join(sreBlockLines(sufficientDigest(), true, "https://coremetry.example/sre/report/sre-p-1"), "\n")
	want := strings.Join([]string{
		"CoSRE araştırması",
		"En olası neden: Deploy",
		"Kanıt: Başlangıca yakın deploy (Deploy)",
		"Kanıt: Hatalar deploy sonrasında (Servis taraması)",
		"Kör noktalar: Trace pivotu (zaman aşımı)",
		"Rapor: https://coremetry.example/sre/report/sre-p-1",
	}, "\n")
	if got != want {
		t.Fatalf("TR blok:\n%s\nbeklenen:\n%s", got, want)
	}
}

func TestSREBlockLinesENAndInsufficient(t *testing.T) {
	d := sufficientDigest()
	d.TopKind = "db_slow"
	d.TopLabel = "Veritabanı yavaşlığı"
	en := strings.Join(sreBlockLines(d, false, ""), "\n")
	if !strings.Contains(en, "Most likely cause: Database slowness") || strings.Contains(en, "Report:") {
		t.Fatalf("EN blok: %s", en)
	}
	bad := &InvestigationDigest{Finished: true, Sufficient: false,
		Missing: []DigestGap{{Code: "single_source", Source: "deploys"}, {Code: "blind_source", Source: "events", Status: "error"}},
		Blind:   []DigestReach{{Source: "events", Status: "error"}}}
	tr := strings.Join(sreBlockLines(bad, true, ""), "\n")
	if strings.Contains(tr, "En olası neden") {
		t.Fatalf("yetersizde neden yazıldı: %s", tr)
	}
	for _, w := range []string{"Kanıt yetersiz", "Eksik: en olası hipotez tek kaynağa dayanıyor (Deploy)", "Kör noktalar: Olaylar (okunamadı)"} {
		if !strings.Contains(tr, w) {
			t.Fatalf("%q yok: %s", w, tr)
		}
	}
	if sreBlockLines(&InvestigationDigest{State: "running"}, true, "") != nil {
		t.Fatal("bitmemiş kayıt blok çizdi")
	}
}

func TestAwaitInvestigation(t *testing.T) {
	on := InvestigationInfo{Enabled: true, AutoPriorities: []string{"P1", "P2"}}
	open := chstore.Problem{ID: "p", Status: "open", Priority: "P1"}
	if !awaitInvestigation(on, open) {
		t.Fatal("P1 açık problem beklenmeli")
	}
	if awaitInvestigation(on, chstore.Problem{Status: "open", Priority: "P3"}) {
		t.Fatal("P3 otomatik listede değil")
	}
	if awaitInvestigation(on, chstore.Problem{Status: "resolved", Priority: "P1"}) {
		t.Fatal("çözülmüş problem beklenmez")
	}
	on.Digest = &InvestigationDigest{State: "running"}
	if !awaitInvestigation(on, chstore.Problem{Status: "open", Priority: "P3"}) {
		t.Fatal("koşan kayıt beklenmeli")
	}
	if awaitInvestigation(InvestigationInfo{}, open) {
		t.Fatal("kapalı ayar beklemez")
	}
}

func TestDecideFollowUp(t *testing.T) {
	now := time.Unix(10_000, 0)
	rec := chstore.NotifySREFollowUp{CreatedAt: now.Add(-time.Minute).UnixNano()}
	on := InvestigationInfo{Enabled: true}
	if decideFollowUp(rec, on, now) != followUpWait {
		t.Fatal("kayıt yok → bekle")
	}
	on.Digest = &InvestigationDigest{Finished: true}
	if decideFollowUp(rec, on, now) != followUpDeliver {
		t.Fatal("bitti → teslim")
	}
	on.Digest = &InvestigationDigest{State: "error"}
	if decideFollowUp(rec, on, now) != followUpDrop {
		t.Fatal("hata → düş")
	}
	old := chstore.NotifySREFollowUp{CreatedAt: now.Add(-2 * time.Hour).UnixNano()}
	if decideFollowUp(old, InvestigationInfo{Enabled: true}, now) != followUpDrop {
		t.Fatal("eski künye düşmeli")
	}
}

func TestMergeFollowUpItems(t *testing.T) {
	got := mergeFollowUpItems(
		[]chstore.NotifySREFollowUpItem{{ChannelID: "a", MessageID: "1"}, {ChannelID: "b"}},
		[]chstore.NotifySREFollowUpItem{{ChannelID: "a", MessageID: "2"}, {ChannelID: "c"}})
	if len(got) != 3 || got[0].MessageID != "2" || got[2].ChannelID != "c" {
		t.Fatalf("%+v", got)
	}
}

// fakeZoom — OAuth + sohbet uçları; istekleri kaydeder.
type fakeZoom struct {
	mu   sync.Mutex
	reqs []string
	body []map[string]any
	put  int // PUT yanıt kodu
}

func (f *fakeZoom) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
			return
		}
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
		f.body = append(f.body, m)
		f.mu.Unlock()
		if r.Method == http.MethodPut {
			w.WriteHeader(f.put)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg-42"}`)
	})
}

func zoomChannel(t *testing.T, base string) chstore.NotificationChannel {
	t.Helper()
	cfg, _ := json.Marshal(ZoomChatChannelConfig{AccountID: "acc", ClientID: "cid", ClientSecret: "sec",
		ChannelID: "chan-1", APIBaseURL: base, OAuthBaseURL: base})
	return chstore.NotificationChannel{ID: "zc-1", Type: "zoomchat", Name: "ops", Config: cfg, Enabled: true}
}

func TestSendZoomChatCapturesMessageIDAndAppendsBlock(t *testing.T) {
	fz := &fakeZoom{put: http.StatusNoContent}
	srv := httptest.NewServer(fz.handler())
	defer srv.Close()
	n := New(nil)
	n.SetPublicURL("https://coremetry.example")
	c := zoomChannel(t, srv.URL)
	p := chstore.Problem{ID: "p-1", Service: "checkout", Severity: "critical", Status: "open"}

	// Bitmiş özet → blok mesajda.
	ctx := context.WithValue(context.Background(), sreDigestKey{}, sufficientDigest())
	if err := n.sendZoomChat(ctx, c, p); err != nil {
		t.Fatal(err)
	}
	msg, _ := fz.body[0]["message"].(string)
	if !strings.Contains(msg, "CoSRE investigation") || !strings.Contains(msg, "/sre/report/sre-p-1") {
		t.Fatalf("blok yok: %s", msg)
	}

	// Bekleniyor → künye toplayıcı mesaj kimliğini alır.
	sink := &sreSink{}
	ctx = context.WithValue(context.Background(), sreSinkKey{}, sink)
	if err := n.sendZoomChat(ctx, c, p); err != nil {
		t.Fatal(err)
	}
	noteSent(ctx, c, nil)
	items := sink.snapshot()
	if len(items) != 1 || items[0].MessageID != "msg-42" || items[0].Text == "" || strings.Contains(items[0].Text, "CoSRE") {
		t.Fatalf("künye: %+v", items)
	}

	// Takip: düzenleme (PUT …/messages/msg-42) ilk metin + blok.
	if err := n.zoomFollowUp(context.Background(), c, items[0], sreBlockLines(sufficientDigest(), true, ""), true); err != nil {
		t.Fatal(err)
	}
	last := len(fz.reqs) - 1
	if fz.reqs[last] != "PUT /v2/chat/users/me/messages/msg-42" {
		t.Fatalf("istek: %v", fz.reqs)
	}
	edited, _ := fz.body[last]["message"].(string)
	if !strings.HasPrefix(edited, items[0].Text) || !strings.Contains(edited, "CoSRE araştırması") || fz.body[last]["to_channel"] != "chan-1" {
		t.Fatalf("düzenleme gövdesi: %v", fz.body[last])
	}
}

func TestZoomFollowUpFallsBackToThreadReply(t *testing.T) {
	fz := &fakeZoom{put: http.StatusBadRequest}
	srv := httptest.NewServer(fz.handler())
	defer srv.Close()
	n := New(nil)
	c := zoomChannel(t, srv.URL)
	it := chstore.NotifySREFollowUpItem{ChannelID: c.ID, Type: c.Type, MessageID: "msg-1", Text: "ilk"}
	lines := sreBlockLines(sufficientDigest(), true, "")
	// Yeni mesaja izin yok → yanıt da yok.
	if err := n.zoomFollowUp(context.Background(), c, it, lines, false); err == nil {
		t.Fatal("düzenleme hatası dönmeli")
	}
	if len(fz.reqs) != 1 {
		t.Fatalf("izin yokken yanıt gitti: %v", fz.reqs)
	}
	if err := n.zoomFollowUp(context.Background(), c, it, lines, true); err != nil {
		t.Fatal(err)
	}
	last := len(fz.reqs) - 1
	if fz.reqs[last] != "POST /v2/chat/users/me/messages" || fz.body[last]["reply_main_message_id"] != "msg-1" {
		t.Fatalf("konu yanıtı: %v %v", fz.reqs, fz.body[last])
	}
}

func TestWithSREEmailInsertsBeforeBodyEnd(t *testing.T) {
	n := New(nil)
	ctx := context.WithValue(context.Background(), sreDigestKey{}, sufficientDigest())
	plain, htm := n.withSREEmail(ctx, "gövde", "<html><body><p>x</p></body></html>")
	if !strings.Contains(plain, "\n\nCoSRE investigation\n• Most likely cause: Deploy") {
		t.Fatalf("düz: %q", plain)
	}
	if !strings.HasSuffix(htm, "</div></body></html>") {
		t.Fatalf("html: %s", htm)
	}
	p2, h2 := n.withSREEmail(context.Background(), "a", "b")
	if p2 != "a" || h2 != "b" {
		t.Fatal("özet yokken gövde değişti")
	}
}

func TestFollowUpMailHeaders(t *testing.T) {
	h := followUpMailHeaders(chstore.NotifySREFollowUp{ThreadRoot: "<cm-thread-1@coremetry>"}, time.Unix(1, 0))
	if len(h) != 3 || h[1] != "In-Reply-To: <cm-thread-1@coremetry>" {
		t.Fatalf("%v", h)
	}
	if followUpMailHeaders(chstore.NotifySREFollowUp{}, time.Unix(1, 0)) != nil {
		t.Fatal("kök yokken başlık")
	}
}
