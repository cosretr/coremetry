// logsKeys — v0.10.1857 (Log Search Faz 2 dilim 2.6; mockup görünüm 1 tablo
// kartının alt satırı "f filtrele · e hariç tut · t trace'e git"). Seçili satır
// (j/k, useTableNav) üzerinde üç kısayol; yalnız /logs bağlıyken kayıtlı
// (useShortcuts sayfa ömrü). SAF çekirdek; logsKeys.test.ts.
//
//   f — seçili satırın servisine süz: `service:"<ad>"` hapı (KQL kısayol anahtarı
//       `service` sunucuda yapılandırılmış servis alan(lar)ına açılır — es.go)
//   e — aynı servisi hariç tut: `NOT service:"<ad>"`
//   t — seçili satırın trace'ine git (trace_id yoksa no-op)
// f/e "sağla" anlamında: aynı polaritede hap varsa değişmez, ters polaritede
// çevrilir, yoksa eklenir (iki kez basmak süzgeci kaldırmaz — tuş tekrarı güvenli).
import type { LogFilter } from '@/lib/logFilters';
import type { LogRow } from '@/lib/types';

export const LOG_ROW_SERVICE_KEY = 'service';

export function ensureFilter(filters: LogFilter[], key: string, value: string, negated: boolean): LogFilter[] {
  const i = filters.findIndex(f => !f.exists && !f.op && !(f.values && f.values.length > 1) && f.key === key && f.value === value);
  if (i === -1) return [...filters, { key, value, negated, disabled: false }];
  const cur = filters[i];
  if (cur.negated === negated && !cur.disabled) return filters;
  return filters.map((f, j) => (j === i ? { ...f, negated, disabled: false } : f));
}

export type LogRowKeyAction =
  | { kind: 'pills'; next: LogFilter[] }
  | { kind: 'trace'; traceId: string }
  | { kind: 'none' };

export function logRowKeyAction(key: 'f' | 'e' | 't', row: LogRow | null, filters: LogFilter[]): LogRowKeyAction {
  if (!row) return { kind: 'none' };
  if (key === 't') return row.traceId ? { kind: 'trace', traceId: row.traceId } : { kind: 'none' };
  const svc = (row.serviceName ?? '').trim();
  if (!svc) return { kind: 'none' };
  const next = ensureFilter(filters, LOG_ROW_SERVICE_KEY, svc, key === 'e');
  return next === filters ? { kind: 'none' } : { kind: 'pills', next };
}
