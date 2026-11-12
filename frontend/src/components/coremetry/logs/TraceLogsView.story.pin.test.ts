// Log Search Faz 6 dilim 6.5 — birleşik trace + log görünümünde "Bu trace’te ne
// oldu?": MEVCUT trace açıklaması (`trace` öznesi, CoSRE sohbet turu). Yeni uç
// ya da yeni ES isteği eklenmez.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'TraceLogsView.tsx'), 'utf8');

describe('TraceLogsView trace hikâyesi', () => {
  it('mevcut trace öznesiyle AIExplainButton', () => {
    expect(src).toContain("<AIExplainButton subject={{ kind: 'trace', id: traceId }}");
    expect(src).toContain('Bu trace’te ne oldu?');
  });
  it('yeni istek yok (api / fetch çağrısı eklenmedi)', () => {
    expect(src).not.toMatch(/\bapi\.\w+\(/);
    expect(src).not.toMatch(/(^|[^\w.])fetch\(/m);
  });
});
