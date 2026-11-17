// rolloutRow.ts — v0.10.201 (ROLLOUTS Faz 4): satır kimliği, upsert
// (updated_at monotonluğu — ReplacingMergeTree'nin istemci ikizi), durum
// tonu, süre, kısa revizyon. Saf; rolloutRow.test.ts pinler.
//
// v0.10.984 (Rollouts v2 P2.3; docs/rollouts/v2-audit.md §2.4, karar 14):
// rollouts.source="v2" iken satırlar rollout_events'ten gelir ve kimlikleri
// 6 parçalıdır (cluster|namespace|kind|workload|incarnationAtMs|generation).
// Satır v2 mi → generation + incarnationAt dolu mu (isV2Rollout). ?rollout=
// kodeki parça sayısıyla ayırır: 5 → workload_rollouts (eski bağlantılar
// TTL'e dek çalışır), 6 → rollout_events.
import type { WorkloadRollout, RolloutOrigin } from './types';
import { fmtDateTime } from './utils';
import { t, tf, fmtPctLang, type Lang } from './i18n'; // v0.10.2304 — ipuçları isteğe bağlı lang (varsayılan 'tr')

/** v0.10.984 — satır rollout_events'ten mi (6 parçalı kimlik taşıyor mu). */
export function isV2Rollout(r: { generation?: number; incarnationAt?: number }): boolean {
  return (r.generation ?? 0) > 0 && (r.incarnationAt ?? 0) > 0;
}

type RolloutKeyInput = Pick<WorkloadRollout, 'clusterId' | 'namespace' | 'workload' | 'revision' | 'startedAt'>
  & Partial<Pick<WorkloadRollout, 'kind' | 'generation' | 'incarnationAt'>>;

export function rolloutKey(r: RolloutKeyInput): string {
  if (isV2Rollout(r)) return `${r.clusterId}|${r.namespace}|${r.kind ?? ''}|${r.workload}|${r.incarnationAt}|${r.generation}`;
  return `${r.clusterId}|${r.namespace}|${r.workload}|${r.revision}|${r.startedAt}`;
}

/** Gelen satır mevcut olanı yalnız updatedAt DAHA BÜYÜKSE ezer; yeni kimlik eklenir. Sıra korunmaz (çağıran sıralar). */
export function upsertRollouts(rows: WorkloadRollout[], incoming: WorkloadRollout[]): WorkloadRollout[] {
  if (incoming.length === 0) return rows;
  const idx = new Map<string, number>();
  rows.forEach((r, i) => idx.set(rolloutKey(r), i));
  const out = rows.slice();
  for (const inc of incoming) {
    const k = rolloutKey(inc);
    const i = idx.get(k);
    if (i === undefined) { idx.set(k, out.length); out.push(inc); continue; }
    if ((inc.updatedAt ?? 0) > (out[i].updatedAt ?? 0)) out[i] = inc;
  }
  return out;
}

export type RolloutTone = 'info' | 'success' | 'danger' | 'warning' | 'neutral';

export function statusTone(status: string): RolloutTone {
  switch (status) {
    case 'in_progress': return 'info';
    case 'completed': return 'success';
    case 'rolled_back': return 'danger';
    case 'superseded': return 'neutral';
    case 'stalled': return 'warning';
    default: return 'neutral';
  }
}

/** v0.10.1421 — Operator-reported: "Türkçe 'ilk gözlem' ve 'tamamlandı' vs
 * yerine İngilizce yazsın." Durum rozeti İngilizce (tablo, çekmece, Servis
 * GitOps sekmesi, kök neden paneli, olay işaretçisi aynı sözlükten okur);
 * açıklayıcı ipuçları (statusTitle) v0.10.2304'ten beri arayüz dilinde. */
export function statusLabel(status: string): string {
  switch (status) {
    case 'in_progress': return 'In progress';
    case 'completed': return 'Completed';
    case 'rolled_back': return 'Rolled back';
    case 'superseded': return 'Superseded';
    case 'stalled': return 'Stalled';
    default: return status || '—';
  }
}

/** v0.10.234 — Operator-reported: durum rozeti ("devralındı") olayın NE olduğunu
 * söylemiyordu. Durumun anlamı (reconcile.go §DURUM) ipucu olarak.
 * v0.10.984 — v2 satırında (KSM dedektörü) aynı v1 durumunun anlamı farklı
 * kanıttan gelir: v2 = true KSM ipucunu verir. */
export function statusTitle(status: string, v2 = false, lang: Lang = 'tr'): string {
  const known = ['in_progress', 'completed', 'rolled_back', 'superseded', 'stalled'];
  if (!known.includes(status)) return '';
  return t(`ro.st${v2 ? '.v2' : ''}.${status}`, lang);
}

/** v0.10.234 — Operator-reported: "imaj değiştiyse deployment, değişmediyse
 * rollout (config change)". Değişikliğin TÜRÜ imaj kimliğinden (repo:tag)
 * türetilir; önceki revizyon bilinmiyorsa kıyas yoktur (ilk gözlem). */
export type RolloutChangeKind = 'deployment' | 'config' | 'initial' | 'rollback' | 'unknown';
/** v0.10.984 — v2 satırında tür dedektörün change_type'ından (imaj kıyası
 * orada yapıldı; rollback'i yalnız dedektör bilir). */
export function rolloutChangeKind(r: Pick<WorkloadRollout, 'image' | 'imageTag' | 'prevImage' | 'prevImageTag' | 'prevRevision'> & Partial<Pick<WorkloadRollout, 'changeType'>>): RolloutChangeKind {
  switch (r.changeType) {
    case 'rollout': return 'deployment';
    case 'config': return 'config';
    case 'rollback': return 'rollback';
    case 'initial': return 'initial';
  }
  if (!r.prevRevision) return 'initial';
  const cur = `${r.image || ''}:${r.imageTag || ''}`;
  const prev = `${r.prevImage || ''}:${r.prevImageTag || ''}`;
  if (cur === ':' && prev === ':') return 'unknown';
  if (cur === ':' || prev === ':') return 'unknown';
  return cur === prev ? 'config' : 'deployment';
}
/** v0.10.1421 — tür rozeti de İngilizce ("ilk gözlem" → "First seen"). */
export function changeKindLabel(k: RolloutChangeKind): string {
  switch (k) {
    case 'deployment': return 'Deployment';
    case 'config': return 'Rollout (config)';
    case 'initial': return 'First seen';
    case 'rollback': return 'Rollback';
    default: return 'Unknown';
  }
}
export function changeKindTitle(k: RolloutChangeKind, lang: Lang = 'tr'): string {
  return t(`ro.ck.${k === 'deployment' || k === 'config' || k === 'initial' || k === 'rollback' ? k : 'unknown'}`, lang);
}
export function changeKindTone(k: RolloutChangeKind): RolloutTone {
  switch (k) {
    case 'deployment': return 'info';
    case 'config': return 'neutral';
    case 'rollback': return 'warning'; // renk yalnız sapmada: geri dönüş sapmadır
    default: return 'neutral';
  }
}
/** v0.10.1445 — operatör: "KSM dedektörü doğru mu". KSM `Rollback` türü gerçek
 * geri almayı (git eski revizyona döndü) eski bir yapının ileri commit'le
 * yeniden deploy'undan ayıramaz; ayrım Argo CD senkron kaydından gelir
 * (rollouts_origin.go). İkinci rozet YALNIZ sapmada: forward / unknown → null
 * (renk yalnız sapan değerde); çekmece hepsini gösterir (originSummary). */
export interface OriginBadge { label: string; tone: RolloutTone; title: string }

/** git revizyonunu kısalt (sha → 7). */
export function shortGitRevision(rev?: string): string {
  const v = (rev ?? '').trim();
  return /^[0-9a-f]{8,}$/i.test(v) ? v.slice(0, 7) : v || '?';
}

export function originLabel(kind: string): string {
  switch (kind) {
    case 'rollback': return 'Rollback (git)';
    case 'redeploy_older': return 'Redeploy (older)';
    case 'forward': return 'Forward';
    default: return 'Unknown';
  }
}

/** Satır ipucu / çekmece açıklaması: prev → new revizyon + güven + sunucunun (EN) gerekçesi. */
export function originTitle(o: RolloutOrigin, lang: Lang = 'tr'): string {
  const revs = `${shortGitRevision(o.prevRevision)} → ${shortGitRevision(o.newRevision)}`;
  const conf = o.confidence > 0 ? tf('ro.or.conf', lang, { p: fmtPctLang(o.confidence, lang) }) : '';
  let head: string;
  switch (o.kind) {
    case 'rollback':
      head = tf('ro.or.rollback', lang, { revs, first: o.firstSyncedAt ? tf('ro.or.firstSync', lang, { t: fmtDateTime(new Date(o.firstSyncedAt)) }) : '' });
      break;
    case 'redeploy_older':
      head = tf('ro.or.redeploy', lang, { revs, img: o.prevTag || o.newTag ? tf('ro.or.img', lang, { a: o.prevTag || '?', b: o.newTag || '?' }) : '' });
      break;
    case 'forward':
      head = tf('ro.or.forward', lang, { revs });
      break;
    default:
      head = o.basis === 'argocd_webhook' ? tf('ro.or.unclear', lang, { revs }) : t('ro.or.unknown', lang);
  }
  return [head, conf, o.reason].filter(Boolean).join(' · ');
}

/** Tablodaki ikinci rozet: yalnız rollback / redeploy_older. */
export function originBadge(r: Pick<WorkloadRollout, 'origin'>, lang: Lang = 'tr'): OriginBadge | null {
  const o = r.origin;
  if (!o) return null;
  if (o.kind === 'rollback') return { label: originLabel(o.kind), tone: 'danger', title: originTitle(o, lang) };
  if (o.kind === 'redeploy_older') return { label: originLabel(o.kind), tone: 'warning', title: originTitle(o, lang) };
  return null;
}

/** Çekmece "kaynak" satırının ek bilgisi: app · başlatan. */
export function originAppLine(o: RolloutOrigin, lang: Lang = 'tr'): string {
  const parts: string[] = [];
  if (o.app) parts.push(`${o.app.appNamespace}/${o.app.appName}${o.app.matchClass ? ` (${o.app.matchClass})` : ''}`);
  if (o.automated !== undefined) parts.push(o.automated ? 'auto-sync' : `${t('ro.or.manualSync', lang)}${o.initiator ? ` · ${o.initiator}` : ''}`);
  if (o.historyDepth) parts.push(tf('ro.or.history', lang, { n: o.historyDepth }));
  return parts.join(' · ');
}

/** Tam imaj kimliği `repo:tag`; ikisi de boşsa '—'. */
export function imageRef(image: string, tag: string): string {
  if (!image && !tag) return '—';
  if (!tag) return image;
  if (!image) return `:${tag}`;
  return `${image}:${tag}`;
}

/** Süre (sn): tamamlandıysa completedAt − startedAt, değilse now − startedAt. */
export function rolloutDurationSec(r: Pick<WorkloadRollout, 'startedAt' | 'completedAt'>, nowMs: number): number {
  const end = r.completedAt && r.completedAt > 0 ? r.completedAt : nowMs;
  return Math.max(0, Math.round((end - r.startedAt) / 1000));
}

/** v0.10.211 — Traces pivot filtreleri TÜRE göre: Deployment'ta revizyon bir
 * ReplicaSet adıdır; STS/DS'de revizyon imaj TAG'idir (MV vekili), replicaset
 * filtresi hiçbir span'e denk gelmezdi — workload adı + imaj tag'iyle süz.
 * v0.10.984 (İnceleme) — v2 satırında STS/DS revizyonu controller revizyonu
 * (update_revision / controller_revision_hash), imaj tag'i DEĞİL: tag
 * imageTag'den (birincil imaj); tag da boşsa yalnız iş yükü + namespace.
 * Revizyon boşsa (v2 Deployment'ta RS henüz okunamadı) replicaset.name=''
 * hiçbir span'e denk gelmez — deployment adı + namespace. */
export function rolloutTracesFilters(r: Pick<WorkloadRollout, 'kind' | 'namespace' | 'workload' | 'revision'>
  & Partial<Pick<WorkloadRollout, 'imageTag' | 'generation' | 'incarnationAt'>>): { k: string; op: string; v: string[] }[] {
  const ns = { k: 'resource.k8s.namespace.name', op: '=', v: [r.namespace] };
  if (r.kind === 'StatefulSet' || r.kind === 'DaemonSet') {
    const kindKey = r.kind === 'StatefulSet' ? 'resource.k8s.statefulset.name' : 'resource.k8s.daemonset.name';
    const tag = isV2Rollout(r) ? (r.imageTag ?? '') : r.revision;
    const out = [{ k: kindKey, op: '=', v: [r.workload] }];
    if (tag) out.push({ k: 'resource.container.image.tag', op: '=', v: [tag] });
    return [...out, ns];
  }
  if (!r.revision) return [{ k: 'resource.k8s.deployment.name', op: '=', v: [r.workload] }, ns];
  return [{ k: 'resource.k8s.replicaset.name', op: '=', v: [r.revision] }, ns];
}

/** v0.10.338 — Operator-reported: aynı workload birden çok cluster'a çıkıyor
 * ama çekmece cluster'ı söylemiyordu. Yer satırı: `<küme> · <namespace> · <tür>`;
 * ad çözülemezse cluster ID (boş bırakılmaz — iki cluster'daki iki rollout
 * ayırt edilebilmeli). */
export function rolloutPlaceLabel(r: Pick<WorkloadRollout, 'clusterId' | 'namespace' | 'kind'>, clusterName?: string): string {
  const name = (clusterName ?? '').trim();
  const cluster = name || (r.clusterId ?? '').trim() || '?';
  return [cluster, r.namespace || '?', r.kind || 'Deployment'].join(' · ');
}

/** `<workload>-<hash>` → `<hash>`; önek yoksa olduğu gibi. */
export function shortRevision(revision: string, workload: string): string {
  if (workload && revision.startsWith(workload + '-')) return revision.slice(workload.length + 1);
  return revision;
}

/** İmaj diff etiketi: tag değiştiyse `eski → yeni`, aynıysa tek; imaj yoksa '—'. */
export function imageDiff(r: Pick<WorkloadRollout, 'imageTag' | 'prevImageTag'>): string {
  const cur = r.imageTag || '';
  const prev = r.prevImageTag || '';
  if (!cur && !prev) return '—';
  if (prev && prev !== cur) return `${prev} → ${cur || '?'}`;
  return cur || prev;
}

// ── Çekmece URL codec'i (v0.10.203) — rolloutHref.test.ts pinler ──────────
export interface RolloutIdParamV1 { clusterId: string; namespace: string; workload: string; revision: string; startedAt: number }
/** v0.10.984 — rollout_events anahtarı (incarnationAt ms). */
export interface RolloutIdParamV2 { clusterId: string; namespace: string; kind: string; workload: string; incarnationAt: number; generation: number }
export type RolloutIdParam = RolloutIdParamV1 | RolloutIdParamV2;

export function isV2Id(p: RolloutIdParam): p is RolloutIdParamV2 {
  return 'generation' in p;
}

/** Satır da (WorkloadRollout) kimlik de (RolloutIdParam) kodlanabilir. */
interface RolloutEncodable {
  clusterId: string; namespace: string; workload: string;
  revision?: string; startedAt?: number; kind?: string; generation?: number; incarnationAt?: number;
}

// Ayraç '|': encodeURIComponent onu %7C'ye kaçırır ('~' KAÇMAZ — unreserved;
// '~' ayraçlı ilk sürüm workload'daki '~' ile bölünüyordu, rolloutHref.test).
// v0.10.984 — v2 satırı (generation + incarnationAt + kind) 6 parça, aksi 5.
export function encodeRolloutParam(r: RolloutEncodable): string {
  if (isV2Rollout(r) && r.kind) {
    return [r.clusterId, r.namespace, r.kind, r.workload, String(r.incarnationAt), String(r.generation)].map(encodeURIComponent).join('|');
  }
  return [r.clusterId, r.namespace, r.workload, r.revision ?? '', String(r.startedAt ?? 0)].map(encodeURIComponent).join('|');
}

const positiveInt = (s: string) => /^[1-9][0-9]*$/.test(s) && Number.isSafeInteger(Number(s));

/** Bozuk/eksik token → null (çekmece açılmaz; sayfa kırılmaz). Parça sayısı
 * kaynağı seçer: 5 → workload_rollouts, 6 → rollout_events (karar 14). */
export function decodeRolloutParam(s: string | null): RolloutIdParam | null {
  if (!s) return null;
  const parts = s.split('|');
  if (parts.length !== 5 && parts.length !== 6) return null;
  try {
    const dec = parts.map(decodeURIComponent);
    if (dec.length === 6) {
      const [clusterId, namespace, kind, workload, inc, gen] = dec;
      if (!clusterId || !namespace || !kind || !workload || !positiveInt(inc) || !positiveInt(gen)) return null;
      return { clusterId, namespace, kind, workload, incarnationAt: Number(inc), generation: Number(gen) };
    }
    const [clusterId, namespace, workload, revision, ts] = dec;
    const startedAt = Number(ts);
    if (!clusterId || !workload || !revision || !Number.isFinite(startedAt) || startedAt <= 0) return null;
    return { clusterId, namespace, workload, revision, startedAt };
  } catch {
    return null;
  }
}

/**
 * rolloutEvidenceHref — v0.10.243 Problem↔Rollout D3: DeepEvidence.rollouts
 * satırından /rollouts çekmece linki. startedAtNs NANOSANİYE (Go
 * RolloutEvidence), çekmece parametresi MİLİSANİYE (rollout.startedAt) —
 * birim sınırı burada, testli.
 * v0.10.984 — kanıt v2 anahtarını taşıyorsa (workloadKind + generation +
 * incarnationAtNs; P2.4 ekler) 6 parçalı bağlantı; yoksa eski 5 parça.
 */
export function rolloutEvidenceHref(ev: { clusterId: string; namespace: string; workload: string; revision: string; startedAtNs: number; workloadKind?: string; generation?: number; incarnationAtNs?: number }): string {
  if (ev.workloadKind && (ev.generation ?? 0) > 0 && (ev.incarnationAtNs ?? 0) > 0) {
    return '/rollouts?rollout=' + encodeRolloutParam({
      clusterId: ev.clusterId, namespace: ev.namespace, kind: ev.workloadKind, workload: ev.workload,
      generation: ev.generation, incarnationAt: Math.round((ev.incarnationAtNs ?? 0) / 1e6),
    });
  }
  return '/rollouts?rollout=' + encodeRolloutParam({
    clusterId: ev.clusterId, namespace: ev.namespace, workload: ev.workload, revision: ev.revision,
    startedAt: Math.round(ev.startedAtNs / 1e6),
  });
}

/** v0.10.984 — v2 satırının replika özeti `güncel/hazır/istenen`; v1 satırında null. */
export function replicaSummary(r: Pick<WorkloadRollout, 'updatedReplicas' | 'availableReplicas' | 'specReplicas'> & { generation?: number; incarnationAt?: number }): string | null {
  if (!isV2Rollout(r)) return null;
  return `${r.updatedReplicas ?? 0}/${r.availableReplicas ?? 0}/${r.specReplicas ?? 0}`;
}

// v0.10.1223 — çekmece zaman çizelgesi: başladı → pod'lar hazır → ilk span
// (trafik) → bitiş (tamamlandı / takıldı / geri alındı). Boş ya da başlangıçtan
// 60 sn'den eski değerler atlanır (bayat alan); zaman sırasıyla.
// v0.10.1421 — Operator-reported: "Başladı / Pod'lar hazır / Geri alındı" üçü
// de +0s idi — başarıdan SONRA geri alınan v2 olayında bitiş succeededAt'ti
// ve "Geri alındı" diye basılıyordu (pod'ların hazır olduğu AN). Artık
// succeededAt "Completed" adımıdır; "Rolled back" adımı yalnız ayrı bir geri
// alma zamanı varsa (finishedAt > succeededAt; açıkken geri alınan olayda
// finishedAt) — yoksa uydurulmaz. Devralınan olayın bitişi "Superseded".
// Bitiş etiketleri durum rozetiyle aynı sözlük (statusLabel).
export interface RolloutTimelineStep { key: string; label: string; at: number; offsetSec: number }

export function rolloutTimeline(r: Pick<WorkloadRollout, 'startedAt' | 'status'> & Partial<Pick<WorkloadRollout,
  'podsReadyAt' | 'firstSpanAt' | 'completedAt' | 'succeededAt' | 'stuckAt' | 'finishedAt' | 'v2Status'>>, lang: Lang = 'tr'): RolloutTimelineStep[] {
  if (!r.startedAt) return [];
  const raw: Array<[string, string, number | undefined]> = [
    ['start', t('ro.tl.start', lang), r.startedAt],
    ['ready', t('ro.tl.ready', lang), r.podsReadyAt],
    ['traffic', t('ro.tl.traffic', lang), r.firstSpanAt],
  ];
  if (r.stuckAt) raw.push(['stuck', statusLabel('stalled'), r.stuckAt]);
  const rolledBack = r.status === 'rolled_back' || r.v2Status === 'rolled_back';
  const superseded = r.status === 'superseded' || r.v2Status === 'superseded';
  const done = tf('ro.tl.done', lang, { c: statusLabel('completed') });
  const succ = r.succeededAt ?? 0;
  if (rolledBack) {
    if (succ) raw.push(['end', done, succ]);
    const rb = succ ? ((r.finishedAt ?? 0) > succ ? r.finishedAt : 0) : (r.finishedAt || r.completedAt);
    if (rb) raw.push([succ ? 'rolledBack' : 'end', statusLabel('rolled_back'), rb]);
  } else if (superseded && !succ) {
    const end = r.finishedAt || r.completedAt;
    if (end) raw.push(['end', statusLabel('superseded'), end]);
  } else {
    const end = succ || r.completedAt;
    if (end) raw.push(['end', done, end]);
  }
  const out: RolloutTimelineStep[] = [];
  for (const [key, label, at] of raw) {
    if (!at || at < r.startedAt - 60_000) continue;
    out.push({ key, label, at, offsetSec: Math.max(0, (at - r.startedAt) / 1000) });
  }
  return out.sort((a, b) => a.at - b.at || (a.key === 'start' ? -1 : b.key === 'start' ? 1 : 0));
}

// ── v0.10.1262 — operatör 2026-10-17 ──────────────────────────────────────
// (1) Platform namespace dışlaması: sunucu (internal/rollout/nsfilter.go)
// system_settings["rollouts"].excludeNamespaces glob'larını SQL'de uygular;
// istemci yalnız düzenleme metnini ayrıştırır ve "açık süzgeç gizli bir
// namespace'e uyuyor" ipucu için AYNI anlamla eşler (`*` dizi, `?` tek
// karakter, tam eşleşme, küçük harf).

/** Düzenleme metni → desen listesi: virgül / boşluk / satır ayırır; kırp, küçült, tekille (girdi sırası korunur). */
export function parseNamespacePatterns(text: string): string[] {
  const out: string[] = [];
  for (const raw of text.split(/[\s,]+/)) {
    const p = raw.trim().toLowerCase();
    if (p && !out.includes(p)) out.push(p);
  }
  return out;
}

/** Sunucunun kabul ettiği desen alfabesi (nsfilter.go nsGlobRE ikizi). */
export function invalidNamespacePatterns(patterns: string[]): string[] {
  return patterns.filter(p => !/^[a-z0-9*?.-]{1,63}$/.test(p));
}

function globToRegExp(p: string): RegExp {
  const body = p.toLowerCase().split('').map(ch =>
    ch === '*' ? '.*' : ch === '?' ? '.' : ch.replace(/[.+^${}()|[\]\\]/g, '\\$&')).join('');
  return new RegExp(`^${body}$`);
}

/** ns desenlerden birine TAM uyuyor mu? (sunucu NamespaceExcluded ikizi) */
export function namespaceExcluded(patterns: readonly string[] | undefined, ns: string): boolean {
  if (!ns || !patterns?.length) return false;
  return patterns.some(p => p.trim() !== '' && globToRegExp(p.trim()).test(ns));
}

// (2) Çekmece "imaj" satırı: önceki / yeni AYRI satır; aynıysa açıkça "aynı".
export interface ImageLines { prev: string; next: string; same: boolean; hasPrev: boolean; hasNext: boolean }

/** Çekmece imaj satırının iki satırı (imageRef biçimi, yoksa '—'); same = ikisi de dolu ve eşit. */
export function imageLines(r: Pick<WorkloadRollout, 'image' | 'imageTag' | 'prevImage' | 'prevImageTag'>): ImageLines {
  const prev = imageRef(r.prevImage, r.prevImageTag);
  const next = imageRef(r.image, r.imageTag);
  const hasPrev = prev !== '—';
  const hasNext = next !== '—';
  return { prev, next, same: hasPrev && hasNext && prev === next, hasPrev, hasNext };
}
