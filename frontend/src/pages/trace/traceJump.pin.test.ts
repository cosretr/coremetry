// v0.10.1953 (operatör: tekil trace mockup'ından "hızlı atlama") — süzgeç çubuğunda
// "İlk hata" ve "En yavaş çağrı" çipleri; tıklama span'i seçer ve şelalenin mevcut
// reveal mekanizmasıyla (ataları aç + kaydır) satıra gider.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { t } from '@/lib/i18n';

const src = readFileSync(resolve(__dirname, '../Trace.tsx'), 'utf8');

describe('trace hızlı atlama', () => {
  it('çipler özetin kök hatası ve en uzun dış çağrısından', () => {
    expect(src).toContain('jumps={jumpTargets} onJump={jumpTo}');
    // v0.10.1954 — düz cümle özeti kaldırıldı (operatör: açıklama CoSRE'nin işi).
    expect(src).not.toMatch(/TraceSummaryLine|trace-sum\b/);
    expect(src).toMatch(/data-testid="trace-jump-error"[\s\S]{0,200}onJump\(jumps\.firstError!\.spanId\)/);
    expect(src).toMatch(/data-testid="trace-jump-slow"[\s\S]{0,120}onJump\(jumps\.hot!\.spanId\)/);
  });
  it('atlama seçer ve şelalenin reveal hedefi olur; servis odağı atlamayı sıfırlar', () => {
    expect(src).toMatch(/const jumpTo = \(id: string\) => \{\s*setSelectedId\(id\);/);
    expect(src).toContain('revealSpanId={jumpId ?? svcFocusInfo.first');
    expect(src).toMatch(/pickSvcFocus = [^\n]*setJumpId\(null\)/);
  });
  it('metinler iki dilde', () => {
    expect(t('trace.jump.err', 'tr')).toBe('İlk hata · {svc}');
    expect(t('trace.jump.slow', 'en')).toBe('Slowest call · {d}');
  });
});
