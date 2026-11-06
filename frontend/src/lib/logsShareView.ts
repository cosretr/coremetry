import type { LogsDisplay, TimeRange } from './types';
import { writeLogsDisplay } from './logsDisplay';
import { encodeRange } from './urlState';
import { absoluteShareHref } from './shareUrl';

// logsShareView — v0.10.1899 (Log Search Faz 5 dilim 5.2). /logs "Share ▾ →
// Copy link": link başka kullanıcıda AYNI tabloyu açmalı.
//
// URL görünümün kaynağıdır ama her şeyi taşımaz: URL'de olmayan anahtarlarda
// sayfa göndericinin YEREL tercihini kullanır — kolonlar (tarayıcı tercihi
// `dt.logs.columns` / sunucu kolon modeli / akıllı iş alanı kolonları), satır
// yüksekliği (eski `logs.wrap`), görünüm tercihi (rh / hh / den / pp / smp) ve
// Topbar'ın yapışkan zaman aralığı. Alıcının tercihi farklıysa aynı link farklı
// tablo açıyordu. Düzeltme: kopyalanan linke yalnız URL'de OLMAYAN anahtarlar,
// göndericinin o an gördüğü değerle eklenir (URL > tercih kuralı alıcıda da
// aynı sonucu verir); URL'de olan anahtarların baytları değişmez. Göreli aralık
// sonra mutlak zamana sabitlenir (absoluteShareHref, v0.9.1280).
//
// Adres çubuğuna DOKUNULMAZ (yalnız kopyalanan link): URL'yi her görünümde
// doldurmak kayıtlı görünüm eşleşmesini ("● modified") bozardı.

export interface LogsShareView {
  display: LogsDisplay;
  sample: number;
  /** Ekranda görünen kolonlar (akıllı kolonlar dahil), sırasıyla. */
  cols: string[];
  /** Sayfanın etkin aralığı (URL'de `range` yoksa yapışkan aralık). */
  range?: TimeRange;
}

/** SAF: göndericinin etkin görünümü → URL parametreleri. */
export function logsShareViewParams(v: LogsShareView): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, val] of writeLogsDisplay(new URLSearchParams(), v.display)) out[k] = val;
  if (v.sample > 0) out.smp = String(v.sample);
  if (v.cols.length) out.cols = v.cols.join(',');
  if (v.range?.preset) out.range = encodeRange(v.range);
  return out;
}

/**
 * SAF: paylaşılacak link. URL'de olmayan görünüm anahtarları eklenir (olanlar
 * aynen), sonra göreli aralık `nowMs`'e göre mutlak zamana sabitlenir.
 */
export function logsShareHref(href: string, nowMs: number, view: LogsShareView): string {
  const hashAt = href.indexOf('#');
  const base = hashAt >= 0 ? href.slice(0, hashAt) : href;
  const hash = hashAt >= 0 ? href.slice(hashAt) : '';
  const qAt = base.indexOf('?');
  const have = new URLSearchParams(qAt >= 0 ? base.slice(qAt + 1) : '');
  const add = new URLSearchParams();
  for (const [k, val] of Object.entries(logsShareViewParams(view))) if (!have.has(k)) add.set(k, val);
  const extra = add.toString();
  const filled = extra ? `${base}${qAt >= 0 ? (base.endsWith('?') || base.endsWith('&') ? '' : '&') : '?'}${extra}${hash}` : href;
  return absoluteShareHref(filled, nowMs);
}
