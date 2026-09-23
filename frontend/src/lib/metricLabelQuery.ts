// metricLabelQuery.ts — v0.10.875 (inceleme): /api/metrics/labels ?q= rungu.
// Sunucu anahtarı q'yu taşıyor; her tuş vuruşu ayrı 60 s cache girdisi ve
// ayrı bir 24 saatlik DISTINCT metric_points taraması demek ("abc" = 3 tarama).
// Kural: q yalnız ≥3 karakterde gider; altında top-200 listesi (q'suz, tek
// cache girdisi) gelir ve istemci süzer. ES-maliyet disiplini: "cache-key
// paramları sınırlı rungalara oturur".
export const METRIC_LABEL_Q_MIN = 3;

export function metricLabelQ(typed: string): string {
  const t = typed.trim();
  return t.length >= METRIC_LABEL_Q_MIN ? t : '';
}
