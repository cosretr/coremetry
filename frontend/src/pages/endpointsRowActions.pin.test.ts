import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.946 (operatör, 2026-09-26: "en sağdaki çarpı ve şimşek ikonlarına
// gerek yok, direkt Traces diyebilir") — Endpoints listesinde satırın tek
// açılış hedefi Traces bağlantısı; ⚡ (en yavaş) / ✖ (en yavaş hatalı)
// exemplar kısayolları geri gelmesin. Editör alarm kuralı düğmesi (⚠) kalır.
describe('Endpoints satır eylemleri', () => {
  const src = readFileSync(resolve(__dirname, 'Endpoints.tsx'), 'utf8');
  it('⚡/✖ exemplar kısayolları yok', () => {
    expect(src).not.toMatch(/r\.slowTraceId\s*&&/);
    expect(src).not.toMatch(/r\.errorTraceId\s*&&/);
  });
  it('Traces bağlantısı ve alarm kuralı düğmesi duruyor', () => {
    // v0.10.1117 — kimlik çipi satırın kuruluşundan (şekil / RPC), endpoints/links.ts.
    expect(src).toContain('<Link to={tracesLink({ service: r.service, path: r.path, sig: bySignature, entry }, range, env, cluster)} className="accent"');
    expect(src).toContain('aria-label="Bu route için alarm kuralı"');
  });
});
