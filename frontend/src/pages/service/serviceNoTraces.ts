// v0.10.2451 — ServiceNoTracesNote'un SAF koşulu (ayrı dosya: react-refresh).
import type { Service } from '@/lib/types';

export interface NoTracesInput {
  loading: boolean;
  bundleErr: string | null;
  info: Service | null;
  operations: readonly unknown[];
  endpoints: readonly unknown[];
}

/** SAF: bundle okundu, hata yok ve penceredeki span özeti / operasyon / giriş YOK. */
export function serviceHasNoTraces(i: NoTracesInput): boolean {
  return !i.loading && !i.bundleErr && !i.info && i.operations.length === 0 && i.endpoints.length === 0;
}
