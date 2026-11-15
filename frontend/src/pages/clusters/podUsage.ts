import type { ClusterPodRow } from '@/lib/types';
import { t, tf, fmtPctLang, type Lang } from '@/lib/i18n';
import { fmtBytes } from '@/lib/utils';
import { podStatusWord } from './nsDetailModel';

// v0.10.2194 (operatör, prod ekran görüntüsü) — pod tablolarında kullanım
// OpenShift Console pod listesi gibi MUTLAK: bellek ikili birimle ("248.3
// MiB"), CPU çekirdekle ("0.004 cores", 3 ondalık). Limit yüzdesi yalnız
// limit varken küçük ikincil ipucu; limitsiz pod'da hücre "—" değil.

/** "0.004 cores" / "0,004 çekirdek"; değer yoksa ''. */
export function podCpuText(cores: number | undefined, lang: Lang): string {
  if (cores == null || !Number.isFinite(cores)) return '';
  const n = new Intl.NumberFormat(lang === 'tr' ? 'tr-TR' : 'en-US', { minimumFractionDigits: 3, maximumFractionDigits: 3 }).format(cores);
  return `${n} ${t('clusters.cores', lang)}`;
}

/** "248.3 MiB", "1.2 GiB"; değer yoksa ''. */
export function podMemText(bytes: number | undefined): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) return bytes === 0 ? '0 B' : '';
  return fmtBytes(bytes, 1);
}

/** "· 45% of limit" — limit yoksa ''. */
export function podLimitHint(pctOfLimit: number | undefined, lang: Lang): string {
  if (!pctOfLimit) return '';
  return tf('clusters.pod.ofLimit', lang, { pct: fmtPctLang(Math.round(pctOfLimit), lang) });
}

/** Pods sekmesinin durum hücresi: faz sözlüğü i18n'den (EN "Running", TR "Çalışıyor"). */
export function podStatusText(r: Pick<ClusterPodRow, 'phase' | 'unschedulable'>, lang: Lang): string {
  return podStatusWord(r, undefined, lang).word;
}

// v0.10.2245 — OpenShift Console "Ready" kolonu: hazır / toplam container.
// Sunucu yalnız namespace kapsamında doldurur; containers yoksa ''.
type ReadyRow = Pick<ClusterPodRow, 'readyContainers' | 'containers'>;

/** "2/2"; bilinmiyorsa ''. */
export function podReadyText(r: ReadyRow): string {
  if (!r.containers) return '';
  return `${r.readyContainers ?? 0}/${r.containers}`;
}

/** Hazır < toplam → tablonun uyarı tonu; aksi hâlde undefined. */
export function podReadyTone(r: ReadyRow): 'warn' | undefined {
  return r.containers && (r.readyContainers ?? 0) < r.containers ? 'warn' : undefined;
}

/** Sıralama değeri: hazır oranı; bilinmiyorsa -1 (en sona). */
export function podReadySortValue(r: ReadyRow): number {
  return r.containers ? (r.readyContainers ?? 0) / r.containers : -1;
}
