// v0.10.2054 — anomali tekilleştirme D4: tek anahtar `ml.groupProblems`.
// Alan yokken AÇIK görünmeli (sunucu nil = açık); PUT tüm blobu gönderir.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('ML grup → Problem anahtarı', () => {
  const src = readFileSync(resolve(__dirname, 'MLSubsection.tsx'), 'utf8');
  it('alan yok = açık; ana anahtar kapalıyken düzenlenemez', () => {
    expect(src).toContain('checked={cfg.groupProblems !== false} disabled={!cfg.enabled}');
    expect(src).toContain('groupProblems: v');
    expect(src).toContain('Anomali grubu Problem açar');
  });
});
