// statusLine — v0.10.1994 (operatör: "login sayfasında silüet yazı vardı; orada izlenen servis sayısı,
// ClickHouse toplam sıkıştırılmamış terabayt bilgisi ve All operational yazabilir"). SAF: sol alt satır.
// Parça yoksa atlanır; hiçbiri yoksa null (satır çizilmez).
import type { LoginSummary } from '@/lib/types';
import { fmtNum } from '@/lib/utils';

/** Ondalık (SI) birim: 12.4 TB, 980 GB — operatörün "terabayt"ı. */
export function fmtStorage(bytes: number): string {
  const units: [number, string][] = [[1e15, 'PB'], [1e12, 'TB'], [1e9, 'GB'], [1e6, 'MB']];
  for (const [d, u] of units) {
    if (bytes >= d) {
      const v = bytes / d;
      return `${v.toFixed(v < 10 ? 1 : 0)} ${u}`;
    }
  }
  return `${Math.round(bytes / 1e3)} KB`;
}

export function loginStatusLine(s: LoginSummary | undefined | null, topoServices: number): string | null {
  const parts: string[] = [];
  const n = s?.services_monitored ?? topoServices;
  if (n > 0) parts.push(`${n.toLocaleString('en-US')} services monitored`);
  // v0.10.2152 (operatör: "sol altta total span sayısı da yazsın servisler ile birlikte").
  // v0.10.2165 (operatör, 2026-11-14: "bugünkü span mı kalsın, saklama süresinin toplamı mı" → "toplamı") —
  // saklanan toplam span (`components.spans_stored`, Sistem sayfasıyla aynı sayı). Alan yoksa parça ATLANIR;
  // bugünkü sayıya geri düşülmez (iki farklı anlam aynı yerde görünmesin). Sağdaki KPI `spans_today` olarak kalır.
  const sp = s?.components?.spans_stored;
  if (sp && sp > 0) parts.push(`${fmtNum(sp)} spans stored`);
  const b = s?.components?.clickhouse_uncompressed_bytes;
  if (b && b > 0) parts.push(`ClickHouse ${fmtStorage(b)} uncompressed`);
  const st = s?.components?.state;
  if (st === 'ok') parts.push('All operational');
  else if (st) parts.push('Degraded');
  return parts.length ? parts.join(' · ') : null;
}
