// fmtEtaHours.ts — v0.10.909 (parite #6 dilim 3): DB kapasite kartının
// "kaç saat kaldı" okunuşu + ton + title. Saf; tablo testli (fmtEtaHours.test.ts).
import type { DBForecast } from './types';

/** Saat → okunuş: <1 sa dakika, <10 sa bir ondalık, üstü tam saat. */
export function fmtEtaHours(h: number): string {
  if (!Number.isFinite(h) || h < 0) return '—';
  if (h < 1) return `${Math.max(1, Math.round(h * 60))} dk`;
  if (h < 10) return `${h.toFixed(1)} saat`;
  return `${Math.round(h)} saat`;
}

export type EtaTone = 'b-err' | 'b-warn' | 'b-gray';

export interface EtaChip {
  text: string;
  tone: EtaTone;
  title: string;
  /** false → soluk "⌛ yok · sebep" satırı (rozet değil). */
  badge: boolean;
}

/** Kart alt satırı. ≤6 sa kırmızı (evaluator erken-açma eşiği), ≤24 sa sarı.
 *  Band genişse "N+ saat" (dürüst kısa hâl). null = gösterilecek bir şey yok. */
export function dbEtaChip(f: DBForecast | undefined): EtaChip | null {
  if (!f) return null;
  const src = f.source === 'vm' ? 'VictoriaMetrics' : 'ClickHouse';
  const base = `Son ${f.windowH} saatin doğrusal eğilimi (${f.points} nokta) · kaynak ${src}.`;
  if (f.status === 'at_limit') {
    return { text: '⌛ dolu (eğilim tavanda)', tone: 'b-err', badge: true, title: base };
  }
  if (f.status !== 'ok' || f.hours === undefined) {
    return { text: `⌛ yok · ${f.reason || 'tahmin kurulamadı'}`, tone: 'b-gray', badge: false,
      title: `${base} Tahmin yalnız eğim pozitif, uyum R² ≥ 0.6 ve ufuk ≤ 24 saatse gösterilir.` };
  }
  const r2 = f.r2 !== undefined ? ` · R² ${f.r2.toFixed(2)}` : '';
  const range = f.loHours !== undefined && f.loHours > 0
    ? ` Aralık ${fmtEtaHours(f.loHours)} – ${f.hiOpen ? 'üst sınır yok' : fmtEtaHours(f.hiHours ?? f.hours)}.`
    : '';
  const text = f.wide ? `⌛ ${fmtEtaHours(f.loHours ?? f.hours)}+` : `⌛ ≈ ${fmtEtaHours(f.hours)}`;
  const tone: EtaTone = f.hours <= 6 ? 'b-err' : 'b-warn';
  return { text: text + r2, tone, badge: true, title: base + range };
}
