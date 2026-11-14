import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1233 — servis sekmelerinin elle yazılmış trace pivotları ekrandaki
// env (+ cluster) bağlamını ortak taşıyıcıdan alır; Overview → metrics
// sayfa aralığını URL'de olmasa da taşır.
const read = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');

describe('servis sekmesi pivotları', () => {
  // v0.10.1491 — üçüncü taşıyıcı: Operasyon kartı satır eylemi (/endpoint#opcard).
  it('OperationsTable iki traces linki + operasyon kartı eylemi env+cluster taşır', () => {
    expect(read('OperationsTable.tsx').match(/pctx, \{ carry: \['env', 'cluster'\] \}\)/g)?.length).toBe(3);
  });
  // v0.10.2145 (S6) — grup başlığı (ikinci traces linki) kalktı; satır eylemi kaldı.
  it('ServicePodsTable traces linki env taşır', () => {
    expect(read('ServicePodsTable.tsx').match(/pctx, \{ carry: \['env'\] \}\)/g)?.length).toBe(1);
  });
  it('Overview metrics linki aralığı hep taşır', () => {
    expect(read('Overview.tsx')).toContain('`&range=${encodeURIComponent(r || encodeRange(range))}`');
  });
});
