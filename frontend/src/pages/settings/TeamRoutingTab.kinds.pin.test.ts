// v0.10.814 — Team routing: olay türü süzgeci (ekip maili kanal süzgecinden bağımsızdı).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('v0.10.814 — Team routing olay türleri', () => {
  it('sekme NOTIFY_KIND_OPTIONS ile kinds alanını düzenler; exception seçeneği yok', () => {
    const src = readFileSync(resolve(__dirname, './TeamRoutingTab.tsx'), 'utf8');
    expect(src).toContain('>Olay türleri (boş = hepsi)</div>'); // R9: etiketli rol=group (Field yalnız tek denetim)
    expect(src).toContain("NOTIFY_KIND_OPTIONS.filter(o => o.value !== 'exception')");
    expect(src).toContain('kinds: toggleKind(normalizeKinds(tc.kinds), o.value)');
    const t = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(t).toContain('kinds?: NotifyKind[];');
  });
});
