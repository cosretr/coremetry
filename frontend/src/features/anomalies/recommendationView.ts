import type { Tone } from '@/components/ui';
import type { SRERecommendation, SREInvestigation } from '@/lib/types';
import { appLinkKind } from '@/lib/safeAppPath';
import { t, type Lang } from '@/lib/i18n'; // v0.10.2288 — etiketler lang alır (varsayılan 'tr')

// recommendationView — v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook
// eşleme + geri alma metni). ProblemInvestigationRecommendations'ın saf
// yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın — react-refresh).
// Go internal/sre/recommend.go sabitlerinin aynası. Öneri YALNIZ METİNDİR:
// hiçbir düğme sistemde bir şeyi uygulamaz.

/** Ajanın her kartta taşıdığı durum belirteci (Go RecPending). Değer sunucu
 *  belirtecidir, değişmez; rozet metni v0.10.2288'den beri 'rec.pending'
 *  (TR değeri bu belirteçle birebir aynı). */
export const REC_PENDING = 'öneri — uygulanmadı' as const;

/** Kartta gösterilen öneriler (sunucu sırası: hipotez önerileri → runbook'lar). */
export function shownRecommendations(inv: SREInvestigation): SRERecommendation[] {
  return inv.recommendations ?? [];
}

/** Operatör işaretinin etiketi (yoksa null). */
export function recHumanLabel(r: SRERecommendation, lang: Lang = 'tr'): { text: string; tone: Tone } | null {
  const who = r.ackBy ? ` · ${r.ackBy}` : '';
  if (r.state === 'ack') return { text: t('rec.human.ack', lang) + who, tone: 'success' };
  if (r.state === 'dismissed') return { text: t('rec.human.dismissed', lang) + who, tone: 'neutral' };
  return null;
}

/**
 * Runbook bağlantısı uygulama içi mi (react-router ile git) yoksa dış mı.
 * v0.10.1314 — url editörün elle yazdığı runbook alanı ve navigate()'e
 * gidiyor: iç yalnız isSafeAppPath ("/\evil.com" react-router v6'da
 * window.location.assign ile dışarı açılırdı), dış yalnız http(s)
 * (javascript:/ftp: vb. asla); ikisi de değilse null (düğme gizli).
 */
export function runbookTarget(r: SRERecommendation): { kind: 'internal' | 'external'; href: string } | null {
  const u = r.runbook?.url;
  if (!u) return null;
  if (u.startsWith('/api/')) return null; // hiçbir zaman bir uca bağlanma (Go zaten üretmez)
  const kind = appLinkKind(u);
  return kind ? { kind, href: u } : null;
}

/** Uygulandı / Ele yalnız bitmiş kayıtta ve işaretlenmemiş öneride. */
export function canAckRecommendation(inv: SREInvestigation, r: SRERecommendation): boolean {
  return (inv.state === 'done' || inv.state === 'partial') && r.state === REC_PENDING;
}
