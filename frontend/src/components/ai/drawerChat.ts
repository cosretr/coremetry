import { aiSubjectTopic, type AIKind, type AISubject } from '@/lib/aiSubject';

// drawerChat — AI çekmecesi içindeki sohbetin BAĞLAM DEVRİ kodeği
// (v0.9.479, operatör raporu: "Chat'te devam et" global CoSRE
// penceresini çekmecenin üstüne açıyordu ve sohbet ekrandaki
// açıklamayı bilmiyordu — ekranda exception dururken CoSRE "bu trace
// ID'ye ait veri yok" deyip filo geneli JVM GC uyarılarını anlattı).
//
// v0.10.1407 (Tek CoSRE adım 4) — çekmecenin özne kipi (AIDrawerBody: ikinci
// thread + çizilmeyen "seed" turu) SİLİNDİ; açıklama artık sohbetin sıradan
// bir turu (explainTurn.ts). Burada kalanlar o turun takip bağlamı ve çipleri:
//
//   • explain BAĞLAMI (buildExplainContext) — chat isteğinin
//      `context.explain` alanı. Sunucu narration bloğuna katar
//      (internal/api/copilot_drawer.go). Konuşma geçmişine sentetik
//      bir "asistan cevabı" turu koymak yetmezdi: guided narration
//      çağrısı konuşma geçmişini HİÇ görmez, yalnız SORU+VERİ görür.
//
// Saf modül: React'ten bağımsız vitest (node ortamı) — emsal
// lib/aiSubject.ts / lib/inboxUrl.ts.

// EXPLAIN_CONTEXT_MAX — tele giden açıklamanın rune tavanı. Sunucu da
// aynı sınırla yeniden kırpar (drawerExplainMaxRunes); buradaki kesme
// 50 KB'lık bir POST'u baştan engeller. İki katman bilerek: sunucu
// kendi sözleşmesini frontend'e emanet etmez.
export const EXPLAIN_CONTEXT_MAX = 3000;

const TRUNC_SUFFIX = '\n…(kısaltıldı)';

// Kanıt id'lerinden bloğa girecek üst sınır — küçük modelde 40 tane
// trace id bağlamı boğar; ilk N zaten "örnek" niteliğinde.
const EVIDENCE_MAX = 10;

export function clampExplain(s: string): string {
  const t = (s ?? '').trim();
  const runes = [...t];
  if (runes.length <= EXPLAIN_CONTEXT_MAX) return t;
  return runes.slice(0, EXPLAIN_CONTEXT_MAX).join('').trim() + TRUNC_SUFFIX;
}

// subjectRef — açıklamanın hangi nesneye ait olduğunun tam (kısaltmasız)
// künyesi (kısaltmasız): model fingerprint/trace id'yi cevabında kullanabilsin.
function subjectRef(s: AISubject): string {
  switch (s.kind) {
    case 'trace':          return `trace ${s.id}`;
    case 'span':           return `trace ${s.id} · span ${s.spanId}`;
    case 'service-health': return `servis ${s.id}`;
    case 'charts':         return `servis ${s.id} grafikleri (${s.scope})`;
    case 'log-spike':      return `log artışı (${s.id || 'tüm servisler'}${s.env ? ' · ' + s.env : ''}${s.q ? ' · arama: ' + s.q : ''})`;
    case 'exception':      return `exception grubu ${s.id}`;
    case 'problem':        return `problem ${s.id}`;
    case 'incident':       return `incident ${s.id}`;
    case 'anomaly':        return `anomali ${s.id}`;
    case 'runbook':        return `runbook (problem ${s.id})`;
    case 'log':            return `log satırı (servis ${s.id}${s.pod ? ' · pod ' + s.pod : ''} · zaman ${s.tsNs} ns${s.traceId ? ' · trace ' + s.traceId : ''})`; // v0.10.1291
    case 'endpoint':       return `endpoint ${s.method ? s.method + ' ' : ''}${s.route} · servis ${s.id}`; // v0.10.1367
    case 'metric_panel':   return `grafik paneli ${s.id}${s.series.length ? ' · seçili seri ' + s.series.join(', ') : ''}`; // v0.10.1435
  }
}

// v0.10.1367 — endpoint öznesinin GÖRÜNÜR ilk sorusu. Sunucu sözleşmesi
// (internal/api/chat_endpoint_prefetch.go): endpointChipShape normalize
// edilmiş mesajın bu ekle BİTTİĞİNE bakar (liste/sor vetosundan muaf);
// endpointMention ilk `/`-belirtecini yol, önündeki alanı fiil sayar;
// endpointServiceHint servisi `[a-z0-9._-]` belirteçlerinden bulur. Bu yüzden
// ek EN SONDA, servis + fiil + rota hemen önünde. Go pini:
// TestEndpointAskQuestionResolves (bu dosyadan ENDPOINT_ASK_SUFFIX'i okur).
export const ENDPOINT_ASK_SUFFIX = 'endpointine bak';

export function endpointAskQuestion(s: Extract<AISubject, { kind: 'endpoint' }>): string {
  const target = [s.id, s.method, s.route].filter(Boolean).join(' ');
  return `Ne oluyor, ne zamandan beri, en yavaş trace'lerde süre nereye gidiyor, hangi hatalar var? ${target} ${ENDPOINT_ASK_SUFFIX}`;
}

// buildExplainContext — `context.explain` alanının gövdesi: künye +
// açıklama metni + kanıt id'leri. Metin boşsa BOŞ döner (henüz cevap
// gelmemiş çekmecede sohbet açılmaz).
export function buildExplainContext(i: {
  subject: AISubject;
  text: string;
  spanIds?: string[];
  traceIds?: string[];
}): string {
  const body = (i.text ?? '').trim();
  if (!body) return '';
  // v0.10.948 — model bağlamı arayüz dilinden BAĞIMSIZ (trace başlığı artık i18n'li).
  const parts = [`KONU: ${aiSubjectTopic(i.subject)} — ${subjectRef(i.subject)}`, body]; // v0.10.1326
  if (i.spanIds?.length) parts.push(`Kanıt span'leri: ${i.spanIds.slice(0, EVIDENCE_MAX).join(', ')}`);
  if (i.traceIds?.length) parts.push(`Kanıt trace'leri: ${i.traceIds.slice(0, EVIDENCE_MAX).join(', ')}`);
  return clampExplain(parts.join('\n'));
}

// aiSubjectQuestion — özneye karşılık gelen doğal soru (v0.9.165 `chatSeed`).
// v0.10.1407 — çağıranlar: sohbet başlangıç çipleri (lib/chatContext.ts) ve
// insight kartı (lib/insightCard.ts); çekmece seed'i ve ✨ kartı silindi.
export function aiSubjectQuestion(kind: AIKind, id: string): string {
  switch (kind) {
    case 'service-health': return `${id} servisinin sağlığı nasıl?`;
    case 'charts':         return `${id} grafiklerinde bu pencerede ne oldu?`;
    case 'log-spike':      return `${id || 'Tüm servislerin'} log artışını ne açıklıyor?`;
    case 'log':            return `${id} servisinin bu log satırı ne anlama geliyor?`; // v0.10.1291
    case 'endpoint':       return `${id} servisinin bu endpoint'i ne durumda?`; // v0.10.1367 — çekmece seed'i boş; yalnız tür bütünlüğü
    case 'problem':        return `Bu problemin kök nedeni ne? (problem ${id})`;
    case 'runbook':        return `${id} runbook'unun adımlarını özetle`;
    case 'anomaly':        return `Bu anomaliyi açıkla (${id})`;
    case 'incident':       return `Bu incident'i açıkla (${id})`;
    case 'exception':      return `Bu exception grubunun kök nedeni ne? (${id})`;
    case 'span':           return `Bu span'i açıkla (${id})`;
    default:               return `Bu trace'i açıkla (${id})`;
  }
}

// drawerFollowups — çekmece sohbetinin çip önerileri. Global chat'in
// statik FOLLOWUPS listesi ("Takımımın servisleri nasıl?") çekmecede
// konu dışıdır; çipin KENDİSİ yeni bir affordance değil, yalnız
// içeriği özneye göre üretilir.
//
// service-health öznesinde öneriler bilerek servis ADINI taşır: bu
// şekiller guided router'da tek başına yönlenir (copilot_followup.go
// guidedSuggestions ile aynı sözleşme) → çip tıklaması gerçek
// telemetriye gider, açıklama özetine değil.
// v0.10.1405 — null (künyesi çözülemeyen arşiv turu) → genel liste.
export function drawerFollowups(s: AISubject | null): string[] {
  if (!s) return ['Bu neden oluyor?', 'Nasıl düzeltirim?', 'Hangi kanıta dayanıyorsun?'];
  if (s.kind === 'service-health' || s.kind === 'charts') {
    return [
      `${s.id} en yavaş trace'ler?`,
      `${s.id} hata logları?`,
      `${s.id} son deploy etkisi?`,
    ];
  }
  if (s.kind === 'log-spike' && s.id) {
    return [`${s.id} hata logları?`, `${s.id} son deploy etkisi?`, 'Hangi kanıta dayanıyorsun?'];
  }
  // v0.10.1367 — endpoint: servis adını taşıyan şekiller guided router'da yönlenir.
  if (s.kind === 'endpoint') {
    return [`${s.id} hata logları?`, `${s.id} en yavaş trace'ler?`, 'Hangi kanıta dayanıyorsun?'];
  }
  // v0.10.1291 — log satırı: servis adını taşıyan şekiller guided router'da yönlenir.
  if (s.kind === 'log') {
    return [`${s.id} hata logları?`, ...(s.traceId ? [`${s.traceId} trace'ini açıkla`] : []), 'Nasıl düzeltirim?'];
  }
  return ['Bu neden oluyor?', 'Nasıl düzeltirim?', 'Hangi kanıta dayanıyorsun?'];
}

// Follow-up önerileri — cevaptan sonra sıradaki faydalı drill-down'lar.
// v0.10.1660 — CopilotChat.tsx'ten taşındı (v1 çekmecesi + v2 MessageList aynı liste).
export const FOLLOWUPS = [
  'Açık problemlerin kök nedeni?',
  'Takımımın servisleri nasıl?',
  'En yavaş servisler?',
  // v0.9.652 (operatör: "son deploy etkisine gerek yok") — deploy
  // sorusu STATİK listeden çıktı. Rotaya bağlı follow-up'larda duruyor:
  // orada bir CEVABIN devamı, burada ise bağlamsız bir menü maddesiydi.
];
