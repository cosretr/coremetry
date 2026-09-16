import type { CHRootCoverageRow } from '@/lib/types';

// rootCoverage — v0.10.733 (kök tanımı). "Giriş kökü" tanımıyla köklü sayı,
// SAF: giriş servisli satırın tamamı giriş span'lidir (giriş = en erken
// server/consumer span), giriş servisi olmayan satırda yalnız tam köklüler.
// Sunucu (rootCoverageEntryRoot) aynı kuralı toplam için uygular.
export function entryRootOf(r: CHRootCoverageRow): number {
  return r.entryService ? r.traces : r.withRoot;
}
