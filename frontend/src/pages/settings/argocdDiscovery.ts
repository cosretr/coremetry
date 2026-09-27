// argocdDiscovery — v0.10.974 — Ayarlar › Argo CD keşif panelinin SAF yarısı
// (mockup Main "Keşif sonucu" + States (b); sunucu internal/argocd/discover.go
// BuildCandidates/matchConfigured/suggestID/uniqueID, argocd_settings_routes.go).
//
// Neden istemci YENİDEN işaretliyor: sunucu adayları keşif anındaki KAYITLI
// blobla eşler (`configuredId`). Sekmede ise operatör Kaydet'ten önce satır
// ekleyip kaldırıyor ve iki hub'ı ayrı ayrı arıyor; "kayıtlı / eklendi / yeni"
// ve kimlik önerisi TASLAĞA (kayıtlı + kaydedilmemiş satırlar) ve öteki hub'ın
// adaylarına karşı hesaplanmazsa aynı kimlik iki kez önerilir ya da az önce
// eklenen aday hâlâ "Ekle" gösterir. Kural sunucununkiyle birebir: aynı hub,
// (hubNamespace, metricsJob); kayıtta iş boşsa yalnız namespace; adayın ns'i
// yoksa yalnız iş. "kayıtlı" yalnız KAYITLI satır ve aynı hub.
//
// Hub durumu (States sözlüğü): hata > kısmi > limitli > boş > ok; renk yalnız
// sapmada (ok rozetsiz). 401/403 sunucuda 502 "unavailable" — ayrı "yetki yok"
// durumu YOK (operatör kararı ertelendi); sabit metin regex'le tanınır ve
// "erişilemedi" rozetine kimlik iletisi eklenir.
import type { ArgoCDCandidate, ArgoCDDiscoverResult } from '@/lib/types';
import { fmtTr, hubName, trLocative, type ArgoHttpError, type InstanceDraft, type RemoteCluster } from './argocdForm';

// ── Hub koşusu ─────────────────────────────────────────────────────────────

export type HubDone = { kind: 'done'; result: ArgoCDDiscoverResult; ms: number; oneShot: boolean };

export type HubRun =
  // v0.10.974 — `prev`: hub yeniden aranırken son BİTEN sonuç (mockup
  // buildDisc: meşgul hub satırlarını tutar; yalnız özet "aranıyor…" olur,
  // rozet ve ayrıntı gizlenir). Tablo 60 sn boyunca kaybolmaz.
  | { kind: 'running'; oneShot: boolean; prev?: HubDone }
  | { kind: 'skipped'; reason: 'missing' | 'disabled' }
  | HubDone
  | { kind: 'failed'; err: ArgoHttpError; ms: number; oneShot: boolean };

/** Ekranda gösterilecek aday sonucu: bitmişse kendisi, yeniden aranıyorsa önceki. */
export function shownResult(r: HubRun | undefined): HubDone | undefined {
  if (!r) return undefined;
  if (r.kind === 'done') return r;
  return r.kind === 'running' ? r.prev : undefined;
}

export type HubState = 'running' | 'ok' | 'empty' | 'partial' | 'truncated' | 'unreachable' | 'timeout' | 'not_configured' | 'busy';

export interface HubBadge { text: string; tone: 'neutral' | 'warning' | 'danger' }

/** Sunucunun sabit 401/403 metni (argocd_settings_routes_test pinli). */
export const CRED_RE = /rejected the cluster credentials \(HTTP (401|403)\)/;

/** Keşfi başlatmadan hub'ı eler: kayıt yok / devre dışı → istek YOK. */
export function preflight(clusterId: string, clusters: RemoteCluster[]): 'missing' | 'disabled' | null {
  const rc = clusters.find(c => c.id === clusterId);
  if (!rc) return 'missing';
  return rc.enabled ? null : 'disabled';
}

/** Hata yanıtının durumu: 400 guardrail → yapılandırılmamış, 504 → zaman aşımı, 429 → meşgul, kalan → erişilemedi. */
export function failureState(err: ArgoHttpError): HubState {
  if (err.status === 400 && (!err.errorType || err.errorType === 'guardrail')) return 'not_configured';
  if (err.status === 504 || err.errorType === 'timeout' || (err.status === 0 && /timed out/i.test(err.error))) return 'timeout';
  if (err.status === 429) return 'busy';
  return 'unreachable';
}

function isTruncatedNote(c: ArgoCDCandidate): boolean {
  return !!c.note && c.note.includes('truncated');
}

/** Öncelik: hata > kısmi > limitli > boş > ok (States sözlüğü). */
export function hubState(run: HubRun): HubState {
  switch (run.kind) {
    case 'running': return 'running';
    case 'skipped': return 'not_configured';
    case 'failed': return failureState(run.err);
    case 'done': {
      const r = run.result;
      if (r.incomplete || r.candidates.some(c => !!c.error)) return 'partial';
      if (r.jobsTruncated || r.candidates.some(isTruncatedNote)) return 'truncated';
      if (r.candidates.length === 0) return 'empty';
      return 'ok';
    }
  }
}

export function hubBadge(s: HubState): HubBadge | null {
  switch (s) {
    case 'empty': return { text: 'boş', tone: 'neutral' };
    case 'partial': return { text: 'kısmi', tone: 'warning' };
    case 'truncated': return { text: 'limitli', tone: 'warning' };
    case 'unreachable': return { text: 'erişilemedi', tone: 'danger' };
    case 'timeout': return { text: 'zaman aşımı', tone: 'danger' };
    case 'not_configured': return { text: 'yapılandırılmamış', tone: 'danger' };
    default: return null; // ok / running / busy — renk yalnız sapmada
  }
}

// ── Aday satırları ─────────────────────────────────────────────────────────

/** Sunucunun `error` önekleri (ConsoleError.Type) → Türkçe. */
const ERR_TR: Record<string, string> = {
  timeout: 'zaman aşımı (15 sn)',
  unavailable: 'erişilemedi',
  internal: 'iç hata',
  bad_data: 'geçersiz sorgu',
  execution: 'değerlendirme hatası',
  response_too_large: 'yanıt çok büyük',
};

/** Hatalı adayın "Not" hücresi: "okunamadı: zaman aşımı (15 sn)" / "atlandı: keşif bütçesi doldu". */
export function candidateErrorNote(error: string): string {
  const type = /^([a-z_]+):/.exec(error)?.[1] ?? '';
  if (type === 'skipped') return 'atlandı: keşif bütçesi doldu';
  const tr = ERR_TR[type];
  return tr ? `okunamadı: ${tr}` : 'okunamadı';
}

/** Sayı hücresi: 1.184 · eksik "—" · kesik "≥100". */
export function fmtCount(n: number | undefined, truncated?: boolean): string {
  if (truncated) return '≥100';
  return n === undefined || n === null ? '—' : fmtTr(n);
}

export type CandStatus = 'new' | 'added' | 'saved' | 'error';

export interface CandRow {
  /** Sonuç içinde kararlı anahtar (hub + sunucu sırası). */
  key: string;
  cand: ArgoCDCandidate;
  status: CandStatus;
  /** Önerilen (yeni) ya da eşleşen taslak satırın kimliği; hatalıda ''. */
  id: string;
  /** Eşleşen taslak satırın anahtarı (eklendi / kayıtlı). */
  matchKey?: string;
  note: string;
  noteTone: 'warn' | 'muted' | 'err';
  noteTitle: string;
  apps: string;
  shards: string;
  countTitle: string;
}

/** Go suggestID aynası: küçük harf, [a-z0-9-], baş/son tiresiz, ≤63; boşsa "instance". */
export function suggestId(s: string): string {
  let out = '';
  let dash = false;
  for (const ch of s.toLowerCase()) {
    if ((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) { out += ch; dash = false; continue; }
    if (!dash && out.length > 0) { out += '-'; dash = true; }
  }
  out = out.replace(/^-+|-+$/g, '');
  if (out.length > 63) out = out.slice(0, 63).replace(/-+$/, '');
  return out || 'instance';
}

/** Go uniqueID aynası: çakışırsa -2, -3 … (63 sınırı korunur). */
export function uniqueId(base: string, taken: Set<string>): string {
  if (!taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const suffix = `-${n}`;
    let b = base;
    if (b.length + suffix.length > 63) b = b.slice(0, 63 - suffix.length).replace(/-+$/, '');
    const id = b + suffix;
    if (!taken.has(id)) return id;
  }
}

/** Go matchConfigured aynası — yalnız aynı hub'daki taslak satırlarla. */
export function matchInstance(c: ArgoCDCandidate, sameHub: InstanceDraft[]): InstanceDraft | undefined {
  if (c.error) return undefined;
  return sameHub.find(i => (c.hubNamespace !== '' && i.hubNamespace === c.hubNamespace && (i.metricsJob === '' || i.metricsJob === c.metricsJob))
    || (c.hubNamespace === '' && i.metricsJob !== '' && i.metricsJob === c.metricsJob));
}

function caseNote(c: ArgoCDCandidate): string {
  if (c.note) return c.note;
  if (c.namespaceCase === 'C') return 'apps-in-any-namespace: exported_namespace ≠ namespace';
  if (c.namespaceCase === 'B') return 'exported_namespace yok — hubNamespace tahmini, doğrulayın';
  return '';
}

/**
 * v0.10.974 — hub sonuçlarını TASLAĞA karşı yeniden işaretler. `results`
 * taslak hub sırasıyla gelir; kimlik önerisi tüm taslak kimliklerine ve
 * ÖNCEKİ hub'ların önerilerine karşı tekil (iki hub'da aynı ns → -2).
 * Sabit sıra: yeni (ve eklenen) → okunamayan → kayıtlı; grup içinde sunucu sırası.
 */
export function markCandidates(
  results: { clusterId: string; candidates: ArgoCDCandidate[] }[],
  instances: InstanceDraft[],
  clusters: RemoteCluster[],
): Map<string, CandRow[]> {
  const taken = new Set(instances.map(i => i.id.trim()).filter(Boolean));
  const out = new Map<string, CandRow[]>();
  const nsHubs = new Map<string, Set<string>>();
  const addNs = (ns: string, hub: string) => {
    if (!ns) return;
    if (!nsHubs.has(ns)) nsHubs.set(ns, new Set());
    nsHubs.get(ns)!.add(hub);
  };
  for (const r of results) for (const c of r.candidates) if (!c.error) addNs(c.hubNamespace, r.clusterId);
  for (const i of instances) addNs(i.hubNamespace, i.hubClusterId);

  for (const r of results) {
    const sameHub = instances.filter(i => i.hubClusterId === r.clusterId);
    const rows: { row: CandRow; rank: number; ix: number }[] = [];
    r.candidates.forEach((c, ix) => {
      const key = `${r.clusterId}:${ix}`;
      if (c.error) {
        rows.push({ rank: 1, ix, row: {
          key, cand: c, status: 'error', id: '', note: candidateErrorNote(c.error), noteTone: 'err', noteTitle: c.error,
          apps: '—', shards: '—', countTitle: '',
        } });
        return;
      }
      let note = caseNote(c);
      if (!note && c.hubNamespace) {
        const other = [...(nsHubs.get(c.hubNamespace) ?? [])].find(h => h !== r.clusterId);
        if (other) {
          const on = hubName(other, clusters);
          note = `aynı namespace ${on}${trLocative(on)} de var — ayrı instance`;
        }
      }
      const counts = {
        apps: fmtCount(c.appCount), shards: fmtCount(c.shardCount, c.shardCountTruncated), countTitle: c.countNote ?? '',
      };
      const base = { key, cand: c, note, noteTone: c.namespaceCase === 'B' ? 'warn' as const : 'muted' as const, noteTitle: note, ...counts };
      const m = matchInstance(c, sameHub);
      if (m && m.origin === 'saved') { rows.push({ rank: 2, ix, row: { ...base, status: 'saved', id: m.id, matchKey: m.key } }); return; }
      if (m) { rows.push({ rank: 0, ix, row: { ...base, status: 'added', id: m.id, matchKey: m.key } }); return; }
      const id = uniqueId(suggestId(c.hubNamespace || c.metricsJob), taken);
      taken.add(id);
      rows.push({ rank: 0, ix, row: { ...base, status: 'new', id } });
    });
    rows.sort((a, b) => a.rank - b.rank || a.ix - b.ix);
    out.set(r.clusterId, rows.map(x => x.row));
  }
  return out;
}

// ── Özet / ayrıntı satırları ───────────────────────────────────────────────

/** "3,1" — istemci kronometresi. */
export function fmtSec(ms: number): string {
  return (ms / 1000).toFixed(1).replace('.', ',');
}

/** Ek kutu: kimlik reddi, etiketli boş sonuç, etiketsiz boş sonuç, etiketsiz aramada aday çıktı. */
export type HubNotice =
  | { kind: 'cred' }
  | { kind: 'emptyLabel'; label: string }
  | { kind: 'emptyNoLabel'; afterOneShot: boolean }
  | { kind: 'oneShotFound' };

export interface HubView {
  state: HubState;
  badge: HubBadge | null;
  summary: string;
  detail: string[];
  notice: HubNotice | null;
  calls: number;
  candidates: number;
  errors: number;
}

const TRUNC_TEXT = "≤50 iş ya da iş başına ≤100 değer sınırı doldu; liste eksik → eksik namespace'i elle ekleyin.";

/**
 * v0.10.974 — bir hub bloğunun başlık özeti + ayrıntı satırları + ek kutusu.
 * Hata özetleri "N çağrı" taşımaz (hata gövdesinde çağrı sayısı yok);
 * countsIncomplete rozeti değiştirmez, yalnız ayrıntı satırı ekler.
 */
export function hubView(run: HubRun, rows: CandRow[], hub: { name: string; label: string; clusterId: string }): HubView {
  const state = hubState(run);
  const badge = hubBadge(state);
  const empty = { calls: 0, candidates: 0, errors: 0 };
  switch (run.kind) {
    case 'running':
      return { state, badge, summary: 'aranıyor…', detail: [], notice: null, ...empty };
    case 'skipped':
      return {
        state, badge, notice: null, ...empty,
        summary: `istek gönderilmedi · Remote Cluster kaydı ${run.reason === 'missing' ? 'yok' : 'devre dışı'}`,
        detail: [run.reason === 'missing'
          ? `“${hub.clusterId}” artık bir Remote Cluster kaydı değil → istek hiç gönderilmedi (fail-closed). Hub satırını kaldırın ya da kaydı Ayarlar › Remote clusters'ta yeniden ekleyin.`
          : `${hub.name} Remote Cluster kaydı devre dışı → istek hiç gönderilmedi (fail-closed). Kaydı Ayarlar › Remote clusters'ta etkinleştirin, sonra yeniden arayın.`],
      };
    case 'failed': {
      const e = run.err;
      const summary = `0 aday · ${fmtSec(run.ms)} sn${e.status ? ` · HTTP ${e.status}` : ''}`;
      if (state === 'busy') return { state, badge, summary, notice: null, ...empty, detail: ['Bir Argo CD keşfi zaten koşuyor — bitince yeniden arayın.'] };
      if (state === 'not_configured') return { state, badge, summary, notice: null, ...empty, detail: [`${e.error || 'Hub kaydı yok, devre dışı ya da tokenRef çözülemedi'} → istek hiç gönderilmedi (fail-closed).`] };
      if (state === 'timeout') {
        return { state, badge, summary, notice: null, ...empty, detail: [e.status
          ? 'İş listesi çağrısı 15 sn içinde yanıt vermedi (HTTP 504) → yeniden dene; sürerse hub Thanos gecikmesine bak.'
          : 'Keşif 75 sn içinde yanıt vermedi → yeniden deneyin; sürerse hub Thanos gecikmesine bakın.'] };
      }
      const cred = CRED_RE.exec(e.error);
      if (cred) return { state, badge, summary, notice: { kind: 'cred' }, ...empty, detail: [`Thanos kimlik bilgisini reddetti (HTTP ${cred[1]}).`] };
      return { state, badge, summary, notice: null, ...empty,
        detail: [`Hub Thanos'una erişilemedi${e.error ? `: ${e.error}` : ''}. DNS, bağlantı reddi ya da 5xx → Remote Cluster'daki Thanos URL'sini ve ağ yolunu kontrol edin.`] };
    }
    case 'done': {
      const r = run.result;
      const errRows = rows.filter(x => x.status === 'error');
      const nErr = errRows.length;
      const nCand = rows.length - nErr;
      const nNew = rows.filter(x => x.status === 'new' || x.status === 'added').length;
      const tag = !hub.label ? 'küme etiketi yok' : r.injectClusterLabel ? `küme etiketi eklendi (${hub.label})` : 'küme etiketi eklenmedi';
      const summary = `${nCand} aday${nCand ? ` · ${nNew} yeni` : ''}${nErr ? ` · ${nErr} iş okunamadı` : ''} · ${r.calls} çağrı · ${fmtSec(run.ms)} sn · ${tag}${run.oneShot ? ' · tek seferlik etiketsiz arama' : ''}`;
      const detail: string[] = [];
      const failed = errRows.filter(x => !(x.cand.error ?? '').startsWith('skipped:'));
      if (failed.length === 1) {
        const f = failed[0].cand;
        detail.push((f.error ?? '').startsWith('timeout:')
          ? `${f.metricsJob} işinin namespace'i 15 sn'de okunamadı; diğer adaylar tam.`
          : `${f.metricsJob} işi ${candidateErrorNote(f.error ?? '')}; diğer adaylar tam.`);
      } else if (failed.length > 1) {
        detail.push(`${failed.length} iş okunamadı (${failed.map(x => x.cand.metricsJob).join(', ')}); diğer adaylar tam.`);
      }
      if (r.incomplete || errRows.length > failed.length) {
        detail.push('Keşif bütçesi doldu (hub başına ≤150 çağrı / 60 sn): kalan işler atlandı; listelenen adaylar doğru → yeniden arayın.');
      }
      if (r.jobsTruncated || r.candidates.some(isTruncatedNote)) detail.push(TRUNC_TEXT);
      if (r.countsIncomplete) {
        const k = r.candidates.filter(c => !c.error && (c.countNote ?? '').includes('bütçesi doldu')).length;
        detail.push(`Uygulama/shard sayısı ${k} adayda eksik: keşif bütçesi doldu.`);
      }
      for (const w of r.warnings ?? []) detail.push(`Thanos uyarısı: ${w}`);
      let notice: HubNotice | null = null;
      if (r.candidates.length === 0) {
        notice = r.injectClusterLabel && hub.label && !run.oneShot
          ? { kind: 'emptyLabel', label: hub.label }
          : { kind: 'emptyNoLabel', afterOneShot: run.oneShot };
      } else if (run.oneShot && nCand > 0) {
        notice = { kind: 'oneShotFound' };
      }
      return { state, badge, summary, detail, notice, calls: r.calls, candidates: nCand, errors: nErr };
    }
  }
}

/** Panel başlığının meta satırı. */
export function panelMeta(at: string, views: HubView[]): string {
  const calls = views.reduce((a, v) => a + v.calls, 0);
  const allEmpty = views.length > 0 && views.every(v => v.state !== 'running' && v.candidates === 0);
  return `${at} · son 1 saat · ${views.length} hub · ${calls} çağrı${allEmpty ? " · hiçbir hub'da aday yok" : ''}`;
}

/** Canlı bölge: "hub-1 bitti · hub-2 aranıyor…". */
export function progressText(doneNames: string[], current: string | null): string {
  return doneNames.map(n => `${n} bitti`).concat(current ? [`${current} aranıyor…`] : []).join(' · ');
}

/** Canlı bölge: tur sonu. */
export function completionText(views: { name: string; view: HubView }[]): string {
  const cand = views.reduce((a, v) => a + v.view.candidates, 0);
  const err = views.reduce((a, v) => a + v.view.errors, 0);
  const tail = `${cand} aday${err ? `, ${err} iş okunamadı` : ''}.`;
  if (views.length === 1) return `${views[0].name} keşfi tamamlandı: ${tail}`;
  return `Keşif tamamlandı: ${views.length} hub, ${tail}`;
}
