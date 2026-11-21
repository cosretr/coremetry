import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

// v0.10.2416 — operator-reported: prod showed raw keys because both catalog
// chunks failed at boot and nothing retried. Pin the recovery paths.
describe('i18n catalog load recovery', () => {
  const src = readFileSync(new URL('./i18n.ts', import.meta.url), 'utf8');
  it('logs the failure and schedules a retry instead of swallowing it', () => {
    expect(src).toMatch(/console\.error\(`\[i18n\] catalog/);
    expect(src).toMatch(/scheduleRetry\(lang\)/);
  });
  it('retries only while no catalog is loaded, then reloads once per tab', () => {
    expect(src).toMatch(/if \(catalogs\.tr \|\| catalogs\.en \|\| retryTimer/);
    expect(src).toMatch(/RELOAD_GUARD_KEY/);
    expect(src).toMatch(/if \(getSessionRaw\(RELOAD_GUARD_KEY\)\) window\.location\.reload\(\)/);
  });
  it('useLang asks for a catalog when none is loaded, even before branding resolves', () => {
    expect(src).toMatch(/\(!catalogs\.tr && !catalogs\.en\)\);/);
  });
});

// v0.10.2421 — the real prod cause: English UI loads only EN, CoSRE text is
// fixed Turkish (tCosre). A TR lookup without the TR catalog must not return
// the raw key: it starts the TR load and shows the English text meanwhile.
describe('TR lookup without the TR catalog', () => {
  const src = readFileSync(new URL('./i18n.ts', import.meta.url), 'utf8');
  it('falls back to English and starts loading TR', () => {
    expect(src).toMatch(/void loadCatalog\('tr'\);\n\s*return catalogs\.en\?\.\[key\] \?\? EN_EAGER\[key\] \?\? key;/);
  });
  it('CoSRE roots subscribe to catalog loads', () => {
    for (const f of ['../components/CopilotChatGate.tsx', '../components/cosre/conversation/CosreChatV2.tsx', '../components/CopilotChat.tsx']) {
      expect(readFileSync(new URL(f, import.meta.url), 'utf8'), f).toContain('useCatalogs();');
    }
  });
});
