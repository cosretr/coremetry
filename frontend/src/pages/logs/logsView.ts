// logsView — v0.10.1499 (/logs Kibana-Discover yeniden tasarımı, dilim 2).
//
// Sayfanın GÖRÜNÜMLERİ: Logs (satır tablosu) | Desenler | Şablonlar | Trace.
// Görünüm v0.10.448'den beri var olan `?panel=` ipucunda yaşar (yok = Logs;
// `patterns` / `templates` = desen görünümleri), yani eski derin linkler
// (anomali/problem → `panel=patterns`) doğrudan desen görünümüne iner.
// `panel` süzgeç kimliğine (logsUrlSig) GİRMEZ — görünüm değiştirmek
// sayfalamayı sıfırlamaz. SAF; Logs.tsx yalnız bunları çağırır.
//
//   • Canlı kuyruk Logs görünümünü ZORLAR (akış satır tablosuna akar; desen
//     görünümünde akan satırı göstermek anlamsız, ES'e de iki okuma olurdu).
//   • Statik liste sorgusu YALNIZ Logs görünümünde koşar: desen görünümünde
//     tablo çizilmez, 100 satırlık ES _search'ü boşa gitmesin (ES-maliyet
//     disiplini; Logs.firstLoad.test `panel=patterns → liste çağrısı yok`).
//
// v0.10.1889 (Log Search 3.6, audit §4) — `panel=trace` birleşik görünüm
// (trace şelalesi + o trace'in logları; tek GET /api/traces/{id}/bundle).
// Kimlik `trace=` (32-hex, normalize), log zamanı ipucu `th=` (unix saniye).
// SÜZGEÇ DEĞİL: `traceId=` süzgecine yazılmaz — histogram / alan istatistiği /
// liste anahtarları değişmez, görünüm açılınca o istekler yeniden koşmaz.
// Açılış geçmişe GİRER (push): Geri önceki aramaya döner.
import { parseLogsPanel } from '@/lib/logsUrl';
import { normalizeTraceId } from './logsQuery';

export type LogsView = 'logs' | 'patterns' | 'templates' | 'trace';

/** `?panel=` (+ `trace=`) → görünüm; `panel=trace` geçerli kimliksiz = logs. */
export function parseLogsView(panel: string | null | undefined, trace?: string | null): LogsView {
  if (panel === 'trace') return normalizeTraceId(trace ?? '') ? 'trace' : 'logs';
  return parseLogsPanel(panel) ?? 'logs';
}

/** Canlı kuyruk açıkken görünüm her zaman Logs. */
export function effectiveLogsView(view: LogsView, live: boolean): LogsView {
  return live ? 'logs' : view;
}

/** Görünümü `?panel=`'e yazar (Logs = param silinir); yabancı paramlar korunur.
 *  Trace dışı görünüm trace hedefini (`trace`, `th`) düşürür. */
export function writeLogsView(prev: URLSearchParams, view: LogsView): URLSearchParams {
  const p = new URLSearchParams(prev);
  if (view === 'logs') p.delete('panel'); else p.set('panel', view);
  if (view !== 'trace') { p.delete('trace'); p.delete('th'); }
  return p;
}

export interface TraceViewTarget { id: string; hintSec: number }

/** URL → birleşik görünüm hedefi (yalnız `panel=trace` + tanınan kimlik). */
export function readTraceViewTarget(p: URLSearchParams): TraceViewTarget | null {
  if (p.get('panel') !== 'trace') return null;
  const id = normalizeTraceId(p.get('trace') ?? '');
  if (!id) return null;
  const h = Number(p.get('th'));
  return { id, hintSec: Number.isFinite(h) && h > 0 ? Math.floor(h) : 0 };
}

/** Birleşik görünümü açan URL: `panel=trace&trace=<id>[&th=<sn>]`; satır
 *  detayı çekmecesi (`doc`, `docsvc`) kapanır. Kimlik tanınmazsa null.
 *  `tsNs` log zamanı (ns) → saniye ipucu. */
export function writeTraceView(prev: URLSearchParams, rawId: string, tsNs?: number): URLSearchParams | null {
  const id = normalizeTraceId(rawId);
  if (!id) return null;
  const p = new URLSearchParams(prev);
  p.set('panel', 'trace');
  p.set('trace', id);
  if (tsNs && tsNs > 0) p.set('th', String(Math.floor(tsNs / 1e9))); else p.delete('th');
  p.delete('doc'); p.delete('docsvc');
  return p;
}

/** Statik liste sorgusunun kapısı (canlı kuyrukta ve takım kararı beklenirken de kapalı). */
export function logsListEnabled(view: LogsView, live: boolean, gate: boolean): boolean {
  return view === 'logs' && !live && !gate;
}
