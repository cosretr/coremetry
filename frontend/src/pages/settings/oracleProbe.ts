// oracleProbe.ts — v0.10.768: Oracle bağlantı testinin saf yardımcıları.
//
// Operatör: "full scan / kilitli sorgu atmasın, önce test edelim, sorgu
// görünür olsun; test ederken hangi servis/operasyon/trace geldiğini
// göreyim". Hüküm metinleri burada, kablolama OracleTab'da (pinli).
import type { OracleLongCheck, OracleScanCheck, OracleWindowSummary } from '@/lib/types';

export const ORACLE_TEST_WINDOWS = [5, 15, 60] as const;
export type OracleTestWindow = (typeof ORACLE_TEST_WINDOWS)[number];

export type ScanTone = 'b-ok' | 'b-warn' | 'b-err' | 'b-gray';

/** Tam-tarama hükmü: indeks ya da partition varsa yeşil; ikisi de yoksa
 *  kırmızı; sözlük okunamadıysa / tablo bulunamadıysa gri (hüküm yok). */
export function scanVerdict(s: OracleScanCheck | undefined): { tone: ScanTone; text: string; detail: string } {
  if (!s || !s.checked) {
    return { tone: 'b-gray', text: 'tam tarama kontrolü yapılamadı', detail: s?.error ?? 'sözlük okunmadı' };
  }
  if (!s.found) {
    return { tone: 'b-gray', text: 'tablo sözlükte yok', detail: 'view ya da synonym olabilir — altındaki tabloyu kontrol et' };
  }
  const rows = s.numRows > 0 ? `${s.numRows.toLocaleString('tr-TR')} satır` : 'satır sayısı bilinmiyor';
  const analyzed = s.lastAnalyzed ? ` (istatistik ${s.lastAnalyzed})` : '';
  if (s.indexed) {
    return { tone: 'b-ok', text: `${s.tsColumn} indeksli`, detail: `${s.indexName ?? 'indeks'} · ${rows}${analyzed}` };
  }
  if (s.partitioned) {
    return { tone: 'b-ok', text: `${s.tsColumn} partition anahtarı`, detail: `partition budaması · ${rows}${analyzed}` };
  }
  return {
    tone: 'b-err',
    text: 'TAM TARAMA RİSKİ',
    detail: `${s.tsColumn} ne indeksli ne partition anahtarı${s.partitionKey ? ` (partition: ${s.partitionKey})` : ''} — her poll ${rows} okur${analyzed}`,
  };
}

/** Pencere özeti başlığı: satır sayısı (tavanlıysa "500+"), eşleme oranı. */
export function summaryHeadline(sm: OracleWindowSummary | undefined): string {
  if (!sm) return '';
  if (sm.error) return `özet alınamadı: ${sm.error}`;
  const rows = sm.capped ? `${sm.rows}+` : String(sm.rows);
  const trace = sm.lookupDone
    ? `${sm.tracesFound}/${sm.traceIds} trace Coremetry'de`
    : sm.lookupError ? `trace araması başarısız (${sm.lookupError})` : `${sm.traceIds} ayrık trace id (arama yok)`;
  return `son ${sm.windowMin} dk: ${rows} satır · ${sm.mapped} eşlendi · ${sm.noTimestamp} damgasız · ${sm.badTraceId} bozuk trace id · ${trace}`;
}

/** v0.10.845 — LONG kolon hükmü. null = gösterilecek bir şey yok (sözlük okundu,
 *  LONG kolon yok). Oracle FETCH FIRST + select listesinde LONG = ORA-00997;
 *  SELECT * kipinde tablodaki her LONG kolon listededir. */
export function longVerdict(l: OracleLongCheck | undefined): { tone: ScanTone; text: string; detail: string } | null {
  if (!l) return null;
  if (!l.checked) {
    return { tone: 'b-gray', text: 'LONG kolon kontrolü yapılamadı', detail: l.error ?? 'sözlük okunmadı' };
  }
  if (l.columns.length === 0) return null;
  const sel = l.selected.join(', ');
  if (l.selected.length > 0 && !l.mappedOnly) {
    return {
      tone: 'b-err',
      text: `LONG kolon sorguda: ${sel}`,
      detail: 'SELECT * + FETCH FIRST bu kolonla ORA-00997 verir. "SELECT yalnız eşlenen kolonlar" kutusunu aç ve bu kolonu eşlemeye alma.',
    };
  }
  if (l.selected.length > 0) {
    return {
      tone: 'b-err',
      text: `eşlenen kolon LONG: ${sel}`,
      detail: 'LONG kolon FETCH FIRST ile okunamaz (ORA-00997). Alanı kapat (-) ya da başka kolona eşle.',
    };
  }
  return { tone: 'b-ok', text: 'LONG kolonlar select dışında', detail: l.columns.join(', ') };
}
