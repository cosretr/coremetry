// lib/dashboardVars — v0.10.2001 (Dashboards Faz 3 dilim 3.8; audit §5.3
// "Değişkenler").
//
// Pano değişkenlerinin SAF çekirdeği:
//   • URL aynası router üzerinden ve YALNIZ panoda tanımlı değişken adlarına
//     dokunur (eski ayna rezerve olmayan HER paramı siliyordu; yabancı
//     paramlar — ör. ?ai= — bu yüzden düşüyordu). Çoklu değer tekrarlı param:
//     ?env=a&env=b.
//   • Çoklu seçim MetricsQL eşleyicisinde `=~"a|b"` olarak açılır (değerler
//     regex-kaçışlı). Değer bir NÖBETÇİ önekle taşınır (MULTI); promql
//     genişletmesi (PanelRenderer expandPromqlVars) onu tanır, diğer alanlar
//     (span DSL, servis, metrik adı) plainVars ile İLK değeri görür — span
//     DSL'de çoklu eşleme yok, sessizce yanlış eşlemek yerine ilk değer.
import type { DashboardVariable } from './types';

/** Çoklu değer nöbetçisi; değerin geri kalanı regex alternasyonu. */
export const MULTI = '\u0001re:';

/** RE2 / MetricsQL regex kaçışı. */
export function regexEscapeValue(v: string): string {
  return v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** SAF: değişken başına URL değerleri → panellere giden harita. */
export function resolveVarValues(defs: readonly DashboardVariable[], sp: URLSearchParams): Record<string, string> {
  const out: Record<string, string> = {};
  for (const d of defs) {
    const vals = sp.getAll(d.name).map(v => v.trim()).filter(Boolean);
    const picked = vals.length > 0 ? vals : d.defaultValue ? [d.defaultValue] : [];
    if (picked.length === 0) continue;
    out[d.name] = picked.length > 1 && d.multi
      ? MULTI + picked.map(regexEscapeValue).join('|')
      : picked[0];
  }
  return out;
}

/** SAF: değişkenin seçili değerleri (URL'den; varsayılan dahil değil). */
export function selectedValues(sp: URLSearchParams, name: string): string[] {
  return sp.getAll(name).map(v => v.trim()).filter(Boolean);
}

/** SAF: tek değişkenin URL yazımı (yabancı paramlar korunur). */
export function writeVarValues(prev: URLSearchParams, name: string, values: readonly string[]): URLSearchParams {
  const next = new URLSearchParams(prev);
  next.delete(name);
  for (const v of values) if (v.trim()) next.append(name, v.trim());
  return next;
}

/** SAF: promql DIŞI alanlar için harita — çoklu değerin İLK öğesi. */
export function plainVars(vars?: Record<string, string>): Record<string, string> | undefined {
  if (!vars) return vars;
  let changed = false;
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(vars)) {
    if (v.startsWith(MULTI)) {
      changed = true;
      const first = v.slice(MULTI.length).split(/(?<!\\)\|/)[0] ?? '';
      out[k] = first.replace(/\\(.)/g, '$1');
    } else {
      out[k] = v;
    }
  }
  return changed ? out : vars;
}
