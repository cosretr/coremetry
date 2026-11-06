// lib/metricsExplorer/snippets.ts — v0.10.1864 (Metrics Explorer Faz 2 dilim
// 2.4; audit §3.1 satır 8 "kod kipinde parçacık menüsü: WITH, default,
// topk_avg, rollup_rate, keep_metric_names"). Saf: metin → metin.
// Hepsi MetricsQL'e özgü; ClickHouse PromQL alt kümesinde menü kapalı.

export type SnippetId = 'with' | 'default' | 'topk_avg' | 'rollup_rate' | 'keep_metric_names';

export const SNIPPETS: { id: SnippetId; label: string; hint: string }[] = [
  { id: 'with', label: 'WITH (…)', hint: 'ortak süzgeci adlandır, ifadede tekrar kullan' },
  { id: 'default', label: '… default 0', hint: 'boş noktaları 0 ile doldur' },
  { id: 'topk_avg', label: 'topk_avg(5, …)', hint: 'ortalamaya göre en büyük 5 seri' },
  { id: 'rollup_rate', label: 'rollup_rate(…[5m])', hint: 'pencere içi min / maks / ort hız' },
  { id: 'keep_metric_names', label: '… keep_metric_names', hint: 'fonksiyondan sonra metrik adını koru' },
];

/** Parçacığı metne uygular: WITH başa, sarmalayıcılar ifadeyi sarar, ekler sona. */
export function applySnippet(id: SnippetId, text: string): string {
  const t = text.trim();
  const body = t || 'metrik_adi';
  switch (id) {
    case 'with': return `WITH (\n  f = {label="değer"}\n)\n${t || 'sum(rate(metrik_adi{f}[$__rate_interval]))'}`;
    case 'default': return `${body} default 0`;
    case 'topk_avg': return `topk_avg(5, ${body})`;
    case 'rollup_rate': return t ? `rollup_rate(${t}[$__rate_interval])` : 'rollup_rate(metrik_adi[$__rate_interval])';
    case 'keep_metric_names': return `${body} keep_metric_names`;
  }
}
