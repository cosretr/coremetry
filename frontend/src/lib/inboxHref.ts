// inboxHref — bir Inbox satırının "kaynağına git" hedefi, TEK yerde
// (v0.10.784). Inbox.tsx'in dört dalı ve servis sayfasının dikkat şeridi
// aynı adresi üretir; iki kopya ayrışınca "aynı satır iki yerde iki yere
// gider" sınıfı doğar.
//
// Ayrıca attentionRows: servis sayfasının üst şeridi için Inbox listesinin
// süzülmüş hâli — SLO burn-rate problemleri satır DEĞİL dipnot (operatör
// kararı 2026-09-18: "SLO üstte yazmasın, varsa problem veya exception
// gözüksün"), anomali satırları varsayılan dışarıda ("∿ Anomalies" düğmesi
// zaten var). Sıra sunucunun sırası (P1 → P2 → P3, sonra son görülme).
import type { InboxItem } from './types';

export const isInboxExcFamily = (it: InboxItem): boolean =>
  (it.kind === 'exception' || it.kind === 'httperror') && !!it.exception;

// SLO burn-rate kural kimliği "slo:<id>:<severity>" (evaluator/slo_burn.go).
export const isSloProblem = (it: InboxItem): boolean =>
  it.kind === 'problem' && !!it.problem && it.problem.ruleId.startsWith('slo:');

// anomalyDetailHref — v0.10.1032 (operatör: "Anomali ve alert rule'lara
// girdiğimde drawer çıkıyor. Exception gibi detay gözükmüyor."). Anomali
// olayının TAM SAYFA detayı Problems kuyruğunun içinde, ?anomaly=<id> ile
// açılır (Inbox.tsx AnomalyEventHost). Eskiden "kaynak" /anomalies?event=
// çekmecesiydi; servis dikkat şeridi, "Open source" ve /anomalies
// çekmecesinin "Tam detay →" bağlantısı artık hep bu adrese iner.
export function anomalyDetailHref(id: string): string {
  return `/anomalies/${encodeURIComponent(id)}`; // C1 (v0.10.1770): tam sayfa
}

// serviceInboxHref — v0.10.1508 (operator-reported, prod): servis sayfası
// şeridi "4 açık konu" (P1 ×2, P2, …) derken "Inbox'ta gör →" "No open
// alerts" + boş kuyruk açıyordu. Şerit /api/inbox'u servis + open ile, ENV'SİZ
// ve TÜM ÖNCELİKLERLE okuyor; link ise `?service=&status=open` taşıyordu ve
// Inbox geri kalanını kendi varsayılanlarından aldı: yalnız P1 (v0.10.1081)
// ve localStorage'daki yapışkan ?env= (useUrlEnv; sunucu "son 1 saatte o
// env'de koşan servis" kümesine daraltır). Link artık şeridin kapsamını AÇIK
// yazar: tüm öncelikler + açık-boş `env=` ("tüm ortamlar", resolveEnv
// yapışkan seçimi DİRİLTMEZ). status varsayılanı zaten open.
// v0.10.1796 — Inbox tür varsayılanı artık problem+anomaly+incident (exception
// kalabalığı); şerit exception'ları da sayar, link türleri de AÇIK yazar.
// Gidiş-dönüş testi: inboxHref.serviceLink.test.ts.
export const SERVICE_INBOX_PRIO = 'P1,P2,P3';
export const SERVICE_INBOX_KIND = 'problem,exception,httperror,anomaly,incident';
export function serviceInboxHref(service: string): string {
  return `/inbox?service=${encodeURIComponent(service)}&prio=${SERVICE_INBOX_PRIO}&kind=${SERVICE_INBOX_KIND}&env=`;
}

export function inboxItemHref(it: InboxItem): string | null {
  if (it.kind === 'problem' && it.problem) {
    return `/problems?problem=${encodeURIComponent(it.problem.id)}`;
  }
  if (isInboxExcFamily(it)) {
    return `/problems?exc=${encodeURIComponent(it.exception!.fingerprint)}`;
  }
  if (it.kind === 'anomaly' && it.anomaly) {
    return anomalyDetailHref(it.anomaly.id);
  }
  if (it.kind === 'incident' && it.incident) {
    return `/incident?id=${encodeURIComponent(it.incident.id)}`;
  }
  return null;
}

// ── Satır tıkı (v0.10.1032; v0.10.1081 çekmece kalktı) ───────────────────
//
// Operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception
// gibi detay gözükmüyor." Problems kuyruğunda (Inbox.tsx) satır tıkının
// kararı burada, SAF: hangi tür nereye açılır. Sayfa yalnız bu kararı uygular;
// test markup'ı değil davranışı çiviler (inboxHref.test.ts).
//
//   exception / httperror → tam sayfa exception detayı (/problems?exc=, gezinme)
//   problem (alarm kuralı) → ?problem=<id>, YERİNDE tam sayfa (AlertProblemHost)
//   anomaly               → ?anomaly=<id>, YERİNDE tam sayfa (AnomalyEventHost)
//   incident              → /incident?id=<id> tam sayfa (gezinme)
//
// v0.10.1081 (operatör: "Drawer çıkmasın, problem sayfasında direkt içeriğine
// girebileyim, Exceptions sayfası gibi.") — triyaj çekmecesi KALDIRILDI.
// Yükü eksik satır kimliğinden (`<tür>:<doğal kimlik>`) aynı hedefe gider
// (inboxItemIdTarget); o da çözülemezse satır hiçbir şey açmaz ('none').
//
// ?problem= ile ?anomaly= KARŞILIKLI DIŞLAYICI: biri açılınca öteki ve eski
// ?item= silinir (withInboxDetail). Elle ikisi birden yazılmışsa okuma sırası
// INBOX_DETAIL_PARAMS'tır (problem önce) — iki tam sayfa aynı anda çizilmez.
export type InboxDetailParam = 'problem' | 'anomaly';
export const INBOX_DETAIL_PARAMS: readonly InboxDetailParam[] = ['problem', 'anomaly'];

export type InboxRowOpen =
  | { to: 'page'; href: string }
  | { to: 'detail'; param: InboxDetailParam; id: string }
  | { to: 'none' };

export function inboxRowOpen(it: InboxItem): InboxRowOpen {
  const href = inboxItemHref(it);
  if (isInboxExcFamily(it) && href) return { to: 'page', href };
  if (it.kind === 'problem' && it.problem?.id) return { to: 'detail', param: 'problem', id: it.problem.id };
  if (it.kind === 'anomaly' && it.anomaly?.id) return { to: 'page', href: anomalyDetailHref(it.anomaly.id) }; // C1: tam sayfa
  if (it.kind === 'incident' && it.incident?.id && href) return { to: 'page', href };
  return inboxItemIdTarget(it.id);
}

/** Eski `?item=<tür>:<doğal kimlik>` linki (ve yükü eksik satır) → tam sayfa
 *  hedefi. Doğal kimlik ilk ':'dan sonrası (kimliğin kendisi ':' taşıyabilir —
 *  `runtime:<check>:<svc>:<pod>`). Tanınmayan tür / boş kimlik → 'none'. */
export function inboxItemIdTarget(itemId: string | null | undefined): InboxRowOpen {
  const raw = itemId ?? '';
  const i = raw.indexOf(':');
  if (i <= 0) return { to: 'none' };
  const kind = raw.slice(0, i);
  const id = raw.slice(i + 1);
  if (!id) return { to: 'none' };
  switch (kind) {
    case 'problem': return { to: 'detail', param: 'problem', id };
    case 'anomaly': return { to: 'page', href: anomalyDetailHref(id) }; // C1
    case 'incident': return { to: 'page', href: `/incident?id=${encodeURIComponent(id)}` };
    case 'exception':
    case 'httperror': return { to: 'page', href: `/problems?exc=${encodeURIComponent(id)}` };
    default: return { to: 'none' };
  }
}

// v0.10.1081 — incident tam sayfası Problems kuyruğundan açılınca geri
// bağlantısı kuyruğa (aynı süzgeçlerle) dönsün: kuyruk adresini gezinme
// durumuyla taşır. Yalnız /inbox adresleri kabul edilir — durum serbest bir
// nesne, başka bir adres geri bağlantıya sızmasın.
export interface IncidentBackState { backTo: string }

export function incidentBackState(inboxPathAndSearch: string): IncidentBackState {
  return { backTo: inboxPathAndSearch };
}

export function readIncidentBackTo(state: unknown): string | null {
  if (!state || typeof state !== 'object') return null;
  const v = (state as Partial<IncidentBackState>).backTo;
  return typeof v === 'string' && (v === '/inbox' || v.startsWith('/inbox?')) ? v : null;
}

/** Tam sayfa detayı aç (id) ya da kapat (null). Açarken diğer detay paramı ve
 *  eski ?item= (v0.10.1081'te çekmece kalktı; yönlendirme bunu da kullanır)
 *  silinir; yabancı parametreler (süzgeçler, ?env=) kalır.
 *  v0.10.1032 (inceleme) — KAPATMAK da hepsini siler (problem, anomaly,
 *  item): elle yazılmış bir link "← Problems"ten sonra öteki detaya ya da
 *  çekmeceye sıçramasın; geri dönüş her zaman kuyruğun kendisidir. */
export function withInboxDetail(prev: URLSearchParams, param: InboxDetailParam, id: string | null): URLSearchParams {
  const p = new URLSearchParams(prev);
  for (const k of INBOX_DETAIL_PARAMS) p.delete(k);
  p.delete('item');
  if (id) p.set(param, id);
  return p;
}

/** v0.10.1414 (tablo satırları gerçek link) — satır kararının (inboxRowOpen)
 *  GERÇEK bağlantı hâli: satırın başlık hücresi `<Link to replace state>`,
 *  satırın boş yeri `rowClickHandlers(to, () => navigate(to, { replace, state }))`
 *  — Ctrl/⌘/orta tık yeni sekme, "bağlantıyı kopyala" aynı hedef. Adresler
 *  bugünküyle AYNI: tam sayfa `o.href` (push; incident'te kuyruğa geri dönüş
 *  durumu), yerinde detay `withInboxDetail`in yazdığı kuyruk adresi
 *  (`replace`). 'none' → null: satır açılmaz, link / işaret / el imleci yok.
 *  `pathname` + `search` = şu anki kuyruk adresi (useLocation). */
export interface InboxRowLink { to: string; replace: boolean; state?: IncidentBackState }
export function inboxRowLink(o: InboxRowOpen, pathname: string, search: string): InboxRowLink | null {
  if (o.to === 'page') {
    return {
      to: o.href,
      replace: false,
      state: o.href.startsWith('/incident?') ? incidentBackState(pathname + search) : undefined,
    };
  }
  if (o.to === 'detail') {
    const qs = withInboxDetail(new URLSearchParams(search), o.param, o.id).toString();
    return { to: qs ? `${pathname}?${qs}` : pathname, replace: true };
  }
  return null;
}

/** URL'deki açık tam sayfa detayı (yoksa null); ikisi birden varsa problem. */
export function readInboxDetail(sp: URLSearchParams): { param: InboxDetailParam; id: string } | null {
  for (const param of INBOX_DETAIL_PARAMS) {
    const id = sp.get(param);
    if (id) return { param, id };
  }
  return null;
}

export interface AttentionRows {
  rows: InboxItem[];
  // Tavanın dışında kalan satır sayısı ("+N daha → Inbox").
  hidden: number;
  // Satır olmayan SLO burn-rate problemleri (dipnot).
  sloCount: number;
}

export const ATTENTION_CAP = 5;

export function attentionRows(
  items: InboxItem[],
  opts: { includeAnomalies?: boolean; cap?: number } = {},
): AttentionRows {
  const cap = opts.cap ?? ATTENTION_CAP;
  let sloCount = 0;
  const kept: InboxItem[] = [];
  for (const it of items) {
    if (isSloProblem(it)) { sloCount++; continue; }
    if (it.kind === 'anomaly' && !opts.includeAnomalies) continue;
    if (!inboxItemHref(it)) continue;
    kept.push(it);
  }
  return { rows: kept.slice(0, cap), hidden: Math.max(0, kept.length - cap), sloCount };
}
