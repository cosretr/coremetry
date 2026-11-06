// logLevelBand — v0.10.1900 (Log Search 3.10): servis Logs sekmesinin seviye
// bantları kompakt Log Search'e taşınırken SAF kurallar buraya çıktı
// (pages/service/ServiceSignalTabs.tsx v0.9.358'den; davranış aynı).
import type { LogRow } from '@/lib/types';

export type LogLevelBand = 'error' | 'warn' | 'info' | 'debug';
export const LOG_LEVEL_BANDS: readonly LogLevelBand[] = ['error', 'warn', 'info', 'debug'];

// Sunucu severity paramı MINIMUM'dur (OTel numarası). error bandı için kesin
// (>=17 zaten bandın kendisi); warn/info için taban — sayfa o taban ÜSTÜNDEN
// gelir, istemci bant kesimi görünümü tamamlar.
export const LOG_LEVEL_MIN_SEV: Record<LogLevelBand, number> = { error: 17, warn: 13, info: 9, debug: 0 };

/** Satır → bant: önce severityText, yoksa OTel numarası (ERROR≥17, WARN≥13, INFO≥9). */
export function levelBandOf(r: LogRow): LogLevelBand {
  const t = (r.severityText || '').toUpperCase();
  if (t.startsWith('ERR') || t.startsWith('FATAL') || t.startsWith('CRIT')) return 'error';
  if (t.startsWith('WARN')) return 'warn';
  if (t.startsWith('INFO')) return 'info';
  if (t.startsWith('DEBUG') || t.startsWith('TRACE')) return 'debug';
  const s = r.severity;
  if (s >= 17) return 'error';
  if (s >= 13) return 'warn';
  if (s >= 9) return 'info';
  return 'debug';
}

/** Histogram serisi adı → bant (levelBandOf ile AYNI kural; v0.9.358). */
export function levelBandOfName(name: string): LogLevelBand {
  const t = name.toUpperCase();
  if (t.startsWith('ERR') || t.startsWith('FATAL') || t.startsWith('CRIT')) return 'error';
  if (t.startsWith('WARN')) return 'warn';
  if (t.startsWith('INFO')) return 'info';
  return 'debug';
}

/** URL `llvl` → bant ya da 'all' (bilinmeyen = all). */
export function parseLevelBand(v: string | null | undefined): 'all' | LogLevelBand {
  return v === 'error' || v === 'warn' || v === 'info' || v === 'debug' ? v : 'all';
}
