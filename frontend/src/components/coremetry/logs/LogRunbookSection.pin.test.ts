// Log Search Faz 6 dilim 6.4 — "Runbook ile açıkla": ES yük kuralı (istek yalnız
// tıkla) + iki giriş noktası (satır içi açılım ve ?doc= çekmecesi) + mevcut
// "CoSRE'ye sor" (explain-log) yerinde.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');
const src = read('LogRunbookSection.tsx');

describe('LogRunbookSection', () => {
  it('istek yalnız run() içinde (mount isteği yok)', () => {
    expect(src).not.toContain('useQuery');
    expect(src.match(/api\.explainLogRunbook\(/g)).toHaveLength(1);
    expect(src).toMatch(/const run = \(\) => \{[\s\S]*api\.explainLogRunbook\(/);
  });
  it('span-event / Oracle satırında ve copilot kapalıyken çizilmez', () => {
    expect(src).toContain('row.origin ? null');
    expect(src).toContain("enabled !== true");
  });
  it('Logs ve servis sekmesi: satır içi + çekmece', () => {
    for (const f of ['../../../pages/Logs.tsx', 'CompactLogSearch.tsx']) {
      const s = read(f);
      expect(s.match(/<LogRunbookSection /g)?.length).toBe(2);
      expect(s).toContain('expandedSection=');
    }
  });
  it('mevcut CoSRE’ye sor yerinde (LogTable LogLineExplain)', () => {
    expect(read('../../LogTable.tsx')).toContain('<LogLineExplain l={l} />');
  });
});
