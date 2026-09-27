// serviceGitOps.ts — v0.10.981 — servis GitOps sekmesinin saf yardımcıları
// (pages/service/ServiceGitOpsTab.tsx). Argo durum tonları, eşleme etiketi,
// 24 saatlik senkron özeti ve rollout ↔ uygulama eşlemesi. serviceGitOps.test.ts pinler.
import type { ArgoServiceApp } from './types';
import type { RolloutTone } from './rolloutRow';

type WorkloadRef = { clusterId: string; namespace: string; workload: string };

/** Bir iş yüküne (rollout satırı) eşlenmiş uygulamalar; manual önce (sunucu sırası korunur). */
export function appsForWorkload(apps: ArgoServiceApp[], w: WorkloadRef): ArgoServiceApp[] {
  return apps.filter(a => a.workloads.some(x => x.clusterId === w.clusterId && x.namespace === w.namespace && x.workload === w.workload));
}

/** Argo sync_status → rozet tonu (renk yalnız sapan değerde: Synced nötr değil başarı, OutOfSync uyarı). */
export function syncTone(s?: string): RolloutTone {
  switch (s) {
    case 'Synced': return 'success';
    case 'OutOfSync': return 'warning';
    default: return 'neutral';
  }
}

/** Argo health_status → rozet tonu. */
export function healthTone(h?: string): RolloutTone {
  switch (h) {
    case 'Healthy': return 'success';
    case 'Progressing': return 'info';
    case 'Degraded': case 'Missing': return 'danger';
    default: return 'neutral';
  }
}

/** Eşleme etiketi: pin (elle, kesin) ya da ad tahmini (güven %). */
export function matchLabel(a: Pick<ArgoServiceApp, 'match' | 'confidence'>): string {
  return a.match === 'manual' ? 'pin' : `tahmini %${a.confidence}`;
}
export function matchTitle(a: Pick<ArgoServiceApp, 'match'>): string {
  return a.match === 'manual'
    ? 'Ayarlar › Argo CD pinlerinden: bu iş yükü elle bu uygulamaya bağlandı'
    : 'Ad tahmini: uygulama adı iş yükü adını içeriyor ve aynı namespace\'e (çözülebildiyse aynı kümeye) deploy ediyor. Kesinleştirmek için Ayarlar › Argo CD\'de pin ekleyin.';
}

const PHASE_ORDER = ['Succeeded', 'Failed', 'Error', 'Running', 'Terminating'];
const PHASE_LABEL: Record<string, string> = { Succeeded: 'başarılı', Failed: 'başarısız', Error: 'hata', Running: 'sürüyor', Terminating: 'sonlanıyor' };

/** 24 saatlik senkron özeti: "3 başarılı · 1 başarısız"; boş harita "yok"; okunmadıysa "—". */
export function syncsSummary(m?: Record<string, number> | null): string {
  if (!m) return '—';
  const keys = Object.keys(m).filter(k => m[k] > 0)
    .sort((a, b) => {
      const ia = PHASE_ORDER.indexOf(a), ib = PHASE_ORDER.indexOf(b);
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
    });
  if (keys.length === 0) return 'yok';
  return keys.map(k => `${m[k]} ${PHASE_LABEL[k] ?? k}`).join(' · ');
}

/** Toplam senkron (sıralama için); okunmadıysa -1. */
export function syncsTotal(m?: Record<string, number> | null): number {
  if (!m) return -1;
  return Object.values(m).reduce((s, n) => s + n, 0);
}

/** Senkronlardan biri başarısız/hata mı (hücre tonu). */
export function syncsFailed(m?: Record<string, number> | null): boolean {
  return !!m && ((m.Failed ?? 0) > 0 || (m.Error ?? 0) > 0);
}

/** Repo URL'sini kısalt: şema, kullanıcı ve `.git` düşer ("github.com/org/repo"). */
export function repoShort(repo?: string): string {
  if (!repo) return '—';
  return repo.replace(/^[a-z+]+:\/\//i, '').replace(/^[^@/]+@/, '').replace(/\.git$/, '').replace(/\/$/, '');
}

/** Autosync etiketi; etiket yoksa (Argo < 2.9) "—". */
export function autoSyncLabel(v?: boolean): string {
  return v === undefined ? '—' : v ? 'açık' : 'kapalı';
}

/** v0.10.985 — Argo bölümü başlığındaki kaynak rozeti: eşleyici tablosu ya da canlı Thanos. */
export function argoSourceBadge(source?: string): string {
  return source === 'mapper' ? 'eşleyici · argocd_app_mapping' : 'Thanos · argocd_app_info';
}

/** v0.10.985 — bölüm meta'sındaki sessiz kaynak notu; kaynak yoksa (Argo aranmadı) boş.
 *  İnceleme: sabit "≤10 dk" ayarlanmış aralıklarda yanlıştı; sunucu tabloyu yalnız son
 *  argocd-metrics koşusu taze ve 'ok' iken kullanır (gecikme = metricsS / mapperMin). */
export function argoSourceMeta(source?: string): string {
  switch (source) {
    case 'mapper': return 'eşleyici tablosundan (işçi aralıkları kadar gecikmeli)';
    case 'live': return 'canlı sorgu';
    default: return '';
  }
}
