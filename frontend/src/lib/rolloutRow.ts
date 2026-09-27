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
import type { WorkloadRollout } from './types';

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

export function statusLabel(status: string): string {
  switch (status) {
    case 'in_progress': return 'sürüyor';
    case 'completed': return 'tamamlandı';
    case 'rolled_back': return 'geri alındı';
    case 'superseded': return 'devralındı';
    case 'stalled': return 'takıldı';
    default: return status || '—';
  }
}

/** v0.10.234 — Operator-reported: durum rozeti ("devralındı") olayın NE olduğunu
 * söylemiyordu. Durumun anlamı (reconcile.go §DURUM) ipucu olarak.
 * v0.10.984 — v2 satırında (KSM dedektörü) aynı v1 durumunun anlamı farklı
 * kanıttan gelir: v2 = true KSM ipucunu verir. */
export function statusTitle(status: string, v2 = false): string {
  if (v2) {
    switch (status) {
      case 'in_progress': return 'Yeni nesil yayılıyor: güncel şablondaki replikalar henüz hepsi hazır değil (KSM)';
      case 'completed': return 'Tüm replikalar yeni şablonda ve hazır (KSM: gözlenen nesil yetişti, güncel = hazır = istenen)';
      case 'rolled_back': return 'Sonraki rollout bu revizyondan önceki bir revizyona geri döndü';
      case 'superseded': return 'Yerini daha yeni bir nesil aldı (bitmeden ya da iş yükü silindi)';
      case 'stalled': return 'İlerlemiyor: progress deadline aşıldı ya da takılma süresi (stuckAfter) doldu';
      default: return '';
    }
  }
  switch (status) {
    case 'in_progress': return 'Yeni revizyon trafik alıyor; eski revizyon henüz tamamen çekilmedi';
    case 'completed': return 'Yeni revizyon tek aktif revizyon; eskileri çekildi';
    case 'rolled_back': return 'Bu revizyon çekildi ve yerine ÖNCEKİ revizyon geri döndü';
    case 'superseded': return 'Bu revizyon çekildi; yerini önceki değil BAŞKA bir revizyon aldı (devralındı)';
    case 'stalled': return 'Yeni revizyon ilerlemiyor: pod\'lar hazır değil ya da trafik teyit edilmedi';
    default: return '';
  }
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
export function changeKindLabel(k: RolloutChangeKind): string {
  switch (k) {
    case 'deployment': return 'Deployment';
    case 'config': return 'Rollout (config)';
    case 'initial': return 'ilk gözlem';
    case 'rollback': return 'Rollback';
    default: return 'bilinmiyor';
  }
}
export function changeKindTitle(k: RolloutChangeKind): string {
  switch (k) {
    case 'deployment': return 'İmaj değişti (eski → yeni tag): yeni sürüm yayını';
    case 'config': return 'İmaj aynı, yalnız revizyon değişti: config/env/secret/replica değişikliği ya da yeniden başlatma';
    case 'initial': return 'Önceki revizyon bilinmiyor — Coremetry bu iş yükünü ilk kez gördü, kıyas yok';
    case 'rollback': return 'Daha önce görülmüş bir revizyona dönüldü (KSM dedektörü)';
    default: return 'İmaj bilgisi eksik (span\'ler container.image taşımıyor) — tür belirlenemedi';
  }
}
export function changeKindTone(k: RolloutChangeKind): RolloutTone {
  switch (k) {
    case 'deployment': return 'info';
    case 'config': return 'neutral';
    case 'rollback': return 'warning'; // renk yalnız sapmada: geri dönüş sapmadır
    default: return 'neutral';
  }
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
