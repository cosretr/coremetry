package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

// copilot_explain_stream.go — tek-atış ✨ Explain yüzeylerinin SSE
// varyantı (v0.9.1127, AI Assistant Faz 1.5;
// docs/plans/ai-assistant-design-2026-08-16.md).
//
// SORUN: sohbet v0.8.404'ten beri token token akıyor, ✨ Explain ise
// 15-40 saniye boyunca dönen bir spinner gösterip cevabı TEK PARÇA
// basıyordu. Yerel 2B-sınıfı modelde bu fark ölçülebilir: aynı cevap,
// akarken "hızlı", biriktirilirken "asılı kalmış" hissettiriyor.
//
// ŞEKİL: handler'lar cevaplarını artık writeJSON ile DEĞİL, buradaki TEK
// deliverExplain'den yazıyor. Kip isteğin kendisinden geliyor (`?stream=1`);
// bayraksız istek bugünkü buffered gövdeyi BAYT BAYT alıyor. Bu yüzden
// yeni bir route, yeni bir handler ya da handler başına kip dalı YOK —
// aksi halde 8 yüzeyde 8 kopya doğar ve ilk drift orada başlar (1024
// bütçesinin üç ayrı builder'da yaşadığı Faz 1.2 dersi).
//
// ÇERÇEVE ŞEKLİ copilot_drawer.go'nunkiyle BİREBİR aynı (delta{text} →
// answer{text,exchangeId,…} → done{ok}); frontend'in elle yazılmış SSE
// okuyucusu iki yüzeyi de değişmeden okuyabilsin diye. Handler'ların
// ekstra alanları (evidenceSpanIds, code, similarCount…) `answer`
// çerçevesine biner — buffered gövdedeki anahtar kümesiyle aynı.

// explainWantsStream — istek akan varyantı mı istiyor?
//
// Sorgu parametresi, gövde bayrağı değil: bu uçların bir kısmı gövdesiz
// POST alıyor (decodeExplainOptions'ın geriye uyumluluk sözleşmesi) ve
// gövdeyi zorunlu kılmak eski istemcileri kırardı. Ayrıca `?stream=1`
// tarayıcı ağ sekmesinde ve erişim loglarında GÖRÜNÜR — hangi çağrının
// akan yolda olduğu bir gövdeyi açmadan okunur.
func explainWantsStream(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	switch strings.TrimSpace(r.URL.Query().Get("stream")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// explainRun — bir explain'in ÜRETİM yarısı. onDelta nil ise buffered
// çağrı beklenir (bugünkü yol), dolu ise akan ikiz. Sıfır delta üretip
// tam metin dönmek GEÇERLİdir: StreamText akıyamayan uçta şeffaf biçimde
// buffered'a düşer ve kod bağlamlı yol bilerek akmaz (aşağıya bakınız).
type explainRun func(onDelta func(string)) (string, error)

// explainPrompt — standart system+user üretim yarısı. Kip seçimini TEK
// yerde yapar: handler ne buffered ne stream bilir, yalnız prompt'unu
// verir.
func (s *Server) explainPrompt(r *http.Request, system, user string) explainRun {
	return func(onDelta func(string)) (string, error) {
		if onDelta == nil {
			return s.copilotExplain(r, system, user)
		}
		return s.copilotExplainStream(r, system, user, onDelta)
	}
}

// explainPromptBuffered — akmayacağı BİLİNEN üretim yarısı. Bugün tek
// kullanıcısı "Kodu da incele" yolu (copilotExplainCode): o yol bağlam
// taşmasında kod bloğunu yarıya indirip çağrıyı BAŞTAN yapıyor. Yarısı
// akmış bir cevabın üstüne ikinci bir cevap yazmak, operatöre iki farklı
// açıklamayı arka arkaya göstermek olurdu — akmamak burada doğru karar.
// SSE kipinde yine SSE ile cevaplanır, yalnız delta üretmez (answer
// çerçevesi aynen düşer, FE hiç fark etmez).
func explainPromptBuffered(call func() (string, error)) explainRun {
	return func(func(string)) (string, error) { return call() }
}

// explainBody — buffered gövde. Anahtar kümesi v0.9.1126'daki handler
// gövdeleriyle BİREBİR aynı (map olduğu için json.Marshal anahtarları
// zaten sıralı basıyor) — akan varyantın bedeli, bayraksız istemcinin
// gördüğü tek bir bayt bile OLMAMALI.
func explainBody(text, xid string, extra map[string]any) map[string]any {
	m := map[string]any{"explanation": text, "exchangeId": xid}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// explainAnswerFrame — SSE `answer` çerçevesi. Metin alanı `text`:
// copilot_drawer.go'nun emit ettiği şekil budur ve frontend okuyucusu o
// şekle göre yazılmıştır. Ekstra alanlar (evidenceSpanIds, code,
// similarCount…) aynı çerçeveye biner — akan kipte kaybolurlarsa
// waterfall kutulaması, kaynak dipnotu ve "N geçmiş çözüm" satırı sessizce
// yok olurdu.
func explainAnswerFrame(text, xid string, extra map[string]any) map[string]any {
	m := map[string]any{"text": text, "exchangeId": xid}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// SSE çerçeve yazıcısı v0.10.535'te ai/agent/blocks.Emitter'a taşındı (tek
// yazım: sohbet + explain + insight). Explain TEMBEL başlık ister: ilk çerçeve
// düşene kadar gövdeye bayt gitmez, üretim ilk bayttan önce patlarsa istemci
// gerçek HTTP statüsü görür (Started() kararı aşağıda).

// deliverExplain — bir explain cevabının TEK çıkışı.
//
// Buffered kip (bayraksız istek, ya da akışı desteklemeyen bir
// ResponseWriter): bugünkü davranış — üret, hata varsa writeErr, yoksa
// writeJSON.
//
// Akan kip: delta* → answer → done. Başlık/flush disiplini
// sseEmitter'da (yukarı).
// explainLinkService — kimlik köprüsünün ORTAMI hangi servisten çıkacak.
//
// v0.10.55 — eskiden yalnız `?service=` query param'ı okunuyordu ve ✨
// Explain uçlarının HİÇBİRİ onu göndermiyor: env her zaman "prod"a
// düşüyordu (envFromServiceName("") → "" → varsayılan). Prod'da doğru
// sonuç verdiği için görünmezdi; prod-DIŞI bir trace açıklandığında link
// yanlış ortamın log sistemine gidiyordu.
//
// Handler'ın bildiği servis artık AÇIKÇA geçiyor. Query param yedek
// olarak kalıyor (sohbet yüzeyleri onu kullanıyor).
// cacheKey (v0.10.83) — boş değilse cevap explain önbelleğinden
// servis edilir / oraya yazılır (explain_cache.go: anahtar prompt'un
// tamamından türer, isabet etiketlenir, ?refresh=1 atlar).
//
// v0.10.948 — gövde deliverExplainPrepared'a taşındı; bu, hazırlığı OLMAYAN
// özel hâli (ekler önceden hesaplı) — çıktı bayt bayt aynı.
func (s *Server) deliverExplain(w http.ResponseWriter, r *http.Request, xid string, extra map[string]any, run explainRun, service, cacheKey string) {
	s.deliverExplainPrepared(w, r, xid, cacheKey,
		func() (map[string]any, string) { return extra, service },
		func(func(string, any)) (explainPrepared, error) {
			return explainPrepared{extra: extra, run: run, service: service, cacheKey: cacheKey}, nil
		})
}

// explainPrepared — v0.10.948: üretim yarısının girdileri (deliverExplain'in
// extra/run/service/cacheKey dörtlüsü) + cevap saklanınca çalışan kanca.
// cacheKey boşsa cevap SAKLANMAZ (ör. geçici kaynak arızalı trace incelemesi).
type explainPrepared struct {
	extra    map[string]any
	run      explainRun
	service  string
	cacheKey string
	onStore  func(ctx context.Context)
}

// explainPrepare — v0.10.948: önbellek ISKASINDA üretimden ÖNCE koşan kanıt
// toplama (trace incelemesi: araç çağrıları). Akan kipte emit SSE'ye yazar
// (step / step-result, withStepIDs kimlikli); buffered kipte olaylar düşer.
// İsabette HİÇ çağrılmaz — hiçbir okuma çalışmaz, adım olayı çıkmaz.
type explainPrepare func(emit func(string, any)) (explainPrepared, error)

// deliverExplainPrepared — v0.10.948: deliverExplain'in çekirdeği. Sıra:
// önbellek (isabet → onHit ekleri, hazırlık yok) → hazırlık (adım olayları
// akar) → hazırlığın FARKLI anahtarı varsa ona da bakış (Tempo yedeği) →
// üretim (delta*) → answer → done. Hazırlık ilk bayttan ÖNCE hata
// verirse gerçek HTTP statüsü döner (trace yok → bugünkü 404 gövdesi);
// sonra verirse akışta error + done{ok:false}.
func (s *Server) deliverExplainPrepared(w http.ResponseWriter, r *http.Request, xid, cacheKey string,
	onHit func() (map[string]any, string), prepare explainPrepare) {
	em, canStream := blocks.NewEmitter(w, blocks.Options{})
	stream := explainWantsStream(r) && canStream
	// v0.10.35 — KİMLİK KÖPRÜSÜ TEK NOKTADAN. answerRequestIDLinks beş
	// sohbet yüzeyinde kabloluydu (chat, drawer, guided, RAG) ama ✨ Explain
	// yüzeylerinin HİÇBİRİNDE yoktu: operatör cevapta bir request_id
	// görüyor, log arayüzüne gitmek için elle kopyalıyordu.
	//
	// Burada hesaplamak 15 explain ucunun HEPSİNİ birden kazandırıyor
	// (trace, span, problem, exception…). Servis bilinmiyorsa
	// templateForService varsayılan şablona düşüyor, yani kırılmıyor.
	// ── ÖNBELLEK İSABETİ (v0.10.83) ────────────────────────────────────
	// run HİÇ çağrılmaz: LLM turu yok, ai_calls satırı yok. exchangeId
	// SAKLANAN kimliktir ki 👍/👎 gerçek çağrıya bağlansın; isabet
	// cached/cachedAtMs ile ETİKETLENİR. Linkler yeniden hesaplanır —
	// şablon ayarı değişmiş olabilir ve link üretimi ucuz.
	var p explainPrepared
	if env, hit := s.explainCacheGet(r, cacheKey); hit {
		extra, service := onHit()
		hitExtra := map[string]any{"cached": true, "cachedAtMs": env.AtMs}
		for k, v := range extra {
			hitExtra[k] = v
		}
		xid = env.Xid
		p = explainPrepared{extra: hitExtra, service: service,
			run: explainPromptBuffered(func() (string, error) { return env.Text, nil })} // cacheKey boş: isabeti geri yazma
	} else {
		emit := func(string, any) {}
		if stream {
			emit = withStepIDs(em.Emit) // v0.10.948 — adım kimliği sohbetle aynı sayaçtan
		}
		var err error
		if p, err = prepare(emit); err != nil {
			if !em.Started() {
				writeExplainPrepareErr(w, err)
				return
			}
			em.Emit("error", map[string]string{"error": err.Error()})
			em.Emit("done", map[string]bool{"ok": false})
			return
		}
		// v0.10.948 — hazırlık farklı bir anahtar seçtiyse (Tempo yedeği: klasik
		// prompt anahtarı) üretimden ÖNCE o anahtara da bakılır; isabet etiketli,
		// exchangeId saklanan kimlik, geri yazılmaz. Ekler hazırlıktan TAZE.
		if p.cacheKey != "" && p.cacheKey != cacheKey {
			if env, hit := s.explainCacheGet(r, p.cacheKey); hit {
				hitExtra := map[string]any{"cached": true, "cachedAtMs": env.AtMs}
				for k, v := range p.extra {
					hitExtra[k] = v
				}
				xid = env.Xid
				p = explainPrepared{extra: hitExtra, service: p.service,
					run: explainPromptBuffered(func() (string, error) { return env.Text, nil })}
			}
		}
	}
	extra, service := p.extra, p.service
	store := func(out, xid string) {
		// v0.10.948 — boş cevap saklanmaz (explainCacheSet de atlar); yan kayıt
		// (onStore — incelemenin :inv meta satırı) da yetim kalmasın.
		if p.cacheKey == "" || strings.TrimSpace(out) == "" {
			return
		}
		s.explainCacheSet(r.Context(), p.cacheKey, out, xid)
		if p.onStore != nil {
			p.onStore(r.Context())
		}
	}

	withLinks := func(out string) map[string]any {
		svc := service
		if svc == "" {
			svc = r.URL.Query().Get("service")
		}
		links := s.answerRequestIDLinks(r.Context(), out, svc)
		if len(links) == 0 {
			return extra
		}
		merged := make(map[string]any, len(extra)+1)
		for k, v := range extra {
			merged[k] = v
		}
		// v0.10.948 — ekler kendi kanıt bağlantılarını taşıyorsa (trace
		// incelemesi) kimlik köprüleri onların ARKASINA eklenir; ezilmez.
		if prior, ok := extra["links"].([]guidedAnswerLink); ok {
			links = dedupLinksByHref(append(append([]guidedAnswerLink{}, prior...), links...))
		}
		merged["links"] = links
		return merged
	}
	if !stream {
		out, err := p.run(nil)
		if err != nil {
			writeErr(w, err)
			return
		}
		store(out, xid)
		writeJSON(w, explainBody(out, xid, withLinks(out)))
		return
	}

	out, err := p.run(func(d string) {
		if d == "" {
			return
		}
		em.Emit("delta", map[string]string{"text": d})
	})
	if err != nil {
		if !em.Started() {
			writeErr(w, err)
			return
		}
		em.Emit("error", map[string]string{"error": err.Error()})
		em.Emit("done", map[string]bool{"ok": false})
		return
	}
	store(out, xid)
	em.Emit("answer", explainAnswerFrame(out, xid, withLinks(out)))
	em.Emit("done", map[string]bool{"ok": true})
}

// writeExplainPrepareErr — hazırlık hatası, ilk bayttan önce. Trace yoksa
// gövde BUGÜNKÜYLE aynı (düz metin 404; FE ve testler bunu okur), gerisi writeErr.
func writeExplainPrepareErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errExplainTraceNotFound) {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}
	writeErr(w, err)
}
