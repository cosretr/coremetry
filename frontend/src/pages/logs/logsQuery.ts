// logsQuery — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1).
//
// Sorgu çubuğunun iki SAF kararı, DOM'suz test edilebilsin diye burada:
//   • isDraftDirty — taslak uygulanan süzgeçten farklı mı. Search ile ↻ TEK
//     düğmede birleşti (Kibana "Update" ⇄ "Refresh"): taslak = uygulanan iken
//     düğme "↻ HH:MM" (pencereyi şimdiye taşır), aksi hâlde "Search" (apply).
//     Oto-uygulanan alanlar (severity, hasTrace, pattern, team) taslağa da
//     yazıldığı için karşılaştırma tüm alanlar üzerinden güvenle yapılır.
//   • bareTraceIdOf — KQL kutusuna yapıştırılmış çıplak 32-hex trace id'si.
//     Sayfa "trace olarak süz" önerir: trace kutusu artık "+ Filter ▾"
//     popover'ının içinde, en sık yapıştırma yolu ona tek tıkla bağlanır.
import type { LogsUrlFilter } from '@/lib/logsUrl';

export function isDraftDirty(draft: LogsUrlFilter, applied: LogsUrlFilter): boolean {
  return draft.service !== applied.service
    || draft.cluster !== applied.cluster
    || draft.search !== applied.search
    || draft.severity !== applied.severity
    || draft.traceId !== applied.traceId
    || draft.spanId !== applied.spanId
    || draft.hasTrace !== applied.hasTrace
    || draft.pattern !== applied.pattern
    || draft.team !== applied.team;
}

const BARE_TRACE_ID = /^(?:0x)?([0-9a-f]{32})$/i;
// v0.10.1890 (Log Search 3.8, audit §4 "Yapıştırılan trace_id") — tireli UUID
// de tanınır (tiresiz 32-hex'e çevrilir). 16-hex TANINMAZ: span id ile ayırt
// edilemez, kutuya yapıştırılan 16-hex bir aramadır.
const BARE_UUID = /^([0-9a-f]{8})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{12})$/i;

/** Kutudaki metin YALNIZ bir trace id'siyse (32-hex / `0x` / tireli UUID)
 *  küçük harfli 32-hex id, aksi hâlde ''. v0.10.1890 — Enter / Search bu
 *  durumda aramayı DEĞİL birleşik görünümü açar (Logs.tsx apply). */
export function bareTraceIdOf(search: string): string {
  const s = search.trim();
  const m = BARE_TRACE_ID.exec(s);
  if (m) return m[1].toLowerCase();
  const u = BARE_UUID.exec(s);
  return u ? u.slice(1).join('').toLowerCase() : '';
}

const HEX = /^[0-9a-f]+$/;

/** v0.10.1889 (Log Search 3.6) — log'daki trace yazımı → ClickHouse'taki
 *  32-hex (küçük harf; `0x` öneki atılır; tireli UUID tiresiz; 16-hex sola
 *  sıfırla). Sunucudaki `normalizeTraceID`'nin (logs_trace_preview.go)
 *  aynası; tanınmayan biçim ''. */
export function normalizeTraceId(raw: string): string {
  let t = raw.trim().toLowerCase();
  if (t.startsWith('0x')) t = t.slice(2);
  if (t.length === 36 && t.split('-').length === 5) t = t.replaceAll('-', '');
  if (t.length === 32 && HEX.test(t)) return t;
  if (t.length === 16 && HEX.test(t)) return '0'.repeat(16) + t;
  return '';
}
