// Metrics — v0.10.1865 (Metrics Explorer Faz 2 dilim 2.5): /metrics =
// Metrics Explorer (pages/metrics/MetricsShell — Sorgu + Keşif sekmeleri).
// Eski katalog Keşif sekmesine (pages/metrics/DiscoverTab) dönüştü; satır
// tıklaması artık Explore'a değil Sorgu sekmesine gider (S3, Explore v2 P5
// aşıldı). Eski `?editor=`, `?metric=`, `?mq=`, `?tab=promql` linkleri
// lib/metricsExplorer/legacy.ts'te bir kez içe okunur.
import { MetricsShell } from './metrics/MetricsShell';

export default function MetricsPage() {
  return <MetricsShell />;
}
