// lib/metricMeta.ts — v0.10.1836 (Metrics Explorer Faz 1 dilim 1.2).
//
// Saf: bir metrik cevabının `meta` zarfından (types.ts MetricResultMeta)
// operatöre gösterilecek kısa bildirim satırları. Mockup
// (docs/frontend/mockups/metrics-explore.html) "12 seriden 8'i gösteriliyor".
//
// Neden yalnız üç durum:
//   - truncated + seriesTotal biliniyor → "N seriden M'i gösteriliyor".
//     Toplam BİLİNMİYORSA (ClickHouse 50k satır tavanı) satır yazılmaz:
//     o durumu panel köşesindeki "⚠ kesik" (rowsCapped) zaten söylüyor; iki
//     ayrı cümle aynı şeyi iki kez söylerdi.
//   - partial → VictoriaMetrics `isPartial` (bazı vmstorage düğümleri cevap
//     vermedi): sayı EKSİK olabilir, tam gibi okunmamalı.
//   - note → sunucunun boş sonuç açıklaması (VM yüzdelik/ad tahmini).
// Kırpma yoksa ve not yoksa liste boş → bileşen hiçbir şey çizmez.

import type { MetricResultMeta } from './types';

// trPossessive — sayıya Türkçe 3. tekil iyelik eki ("8'i", "12'si", "40'ı").
// Ek, sayının OKUNUŞUNDAKİ son sözcüğün ünlüsüne göre seçilir.
export function trPossessive(n: number): string {
  const v = Math.abs(Math.trunc(n));
  const ones = ["'ı", "'i", "'si", "'ü", "'ü", "'i", "'sı", "'si", "'i", "'u"]; // sıfır, bir … dokuz
  const tens = ['', "'u", "'si", "'u", "'ı", "'si", "'ı", "'i", "'i", "'ı"];    // on, yirmi … doksan
  let suffix: string;
  if (v === 0) suffix = ones[0];
  else if (v % 10 !== 0) suffix = ones[v % 10];
  else if (v % 100 !== 0) suffix = tens[(v % 100) / 10];
  else if (v % 1000 !== 0) suffix = "'ü"; // yüz
  else if (v % 1_000_000 !== 0) suffix = "'i"; // bin
  else suffix = "'u"; // milyon
  return `${v}${suffix}`;
}

export function metricNoticeLines(meta?: MetricResultMeta | null, note?: string | null): string[] {
  const out: string[] = [];
  if (meta?.truncated && meta.seriesTotal != null && meta.seriesTotal > meta.seriesShown) {
    out.push(`${meta.seriesTotal} seriden ${trPossessive(meta.seriesShown)} gösteriliyor`);
  }
  if (meta?.partial) {
    out.push('Kısmi sonuç: bazı depolama düğümleri cevap vermedi, sayılar eksik olabilir');
  }
  const n = (note ?? meta?.note ?? '').trim();
  if (n) out.push(n);
  return out;
}
