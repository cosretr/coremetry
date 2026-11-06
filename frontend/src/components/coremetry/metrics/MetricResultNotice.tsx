// coremetry/metrics/MetricResultNotice — v0.10.1836 (Metrics Explorer Faz 1
// dilim 1.2; audit §6 "kesilen her sonuçta bildirim").
//
// Metrik cevabının `meta` zarfından (lib/metricMeta.ts metricNoticeLines)
// kısa, akışta duran bildirim: "12 seriden 8'i gösteriliyor", "Kısmi sonuç…",
// sunucu notu. Söylenecek bir şey yoksa HİÇBİR şey çizmez (boş kutu yok).
// role="status": hata değil, sonucun nasıl okunacağına dair bilgi.
// Yerleşim yeniden tasarımı Faz 2'de (durum satırı); bu bileşen yalnız
// bugünkü editör + pano panellerine bilgi taşır.
import { metricNoticeLines } from '@/lib/metricMeta';
import type { MetricResultMeta } from '@/lib/types';

export interface MetricResultNoticeProps {
  meta?: MetricResultMeta | null;
  /** Üst düzey `note` (meta.note yoksa ya da ayrı geldiyse). */
  note?: string | null;
  /** Çok sorgulu editörde satır öneki ("A: …"). */
  prefix?: string;
  className?: string;
}

export function MetricResultNotice({ meta, note, prefix, className }: MetricResultNoticeProps) {
  const lines = metricNoticeLines(meta, note);
  if (lines.length === 0) return null;
  return (
    <div role="status" data-testid="metric-result-notice"
      className={`mb-2 rounded-md border border-status-warn-border bg-status-warn-bg px-2 py-1 text-xs ${className ?? ''}`}>
      {lines.map(l => (
        <div key={l}>{prefix ? `${prefix}: ` : ''}{l}</div>
      ))}
    </div>
  );
}
