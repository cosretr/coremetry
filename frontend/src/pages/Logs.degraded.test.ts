// v0.10.415 — log arama denetimi B1: üç yüzey de sunucunun 200 {degraded}
// sözleşmesini OKUR. Kaynak pini (Logs.prefs.test deseni): bayrağı okuyan
// satır kaybolursa yavaş backend gene "No logs found" gibi görünür.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('degraded log backend surfaces (B1)', () => {
  it('Logs.tsx: rozet + degraded boş durum + normal boş durum kapısı', () => {
    const src = read('./Logs.tsx');
    expect(src).toContain("!live && !!staticQ.data?.degraded && (");
    // v0.10.967 — tablo standardı T12: degraded boş durumu artık tablonun
    // içinde hata satırı (⚠ + ↻ Retry); kapı aynı, metin logsState'te.
    expect(src).toContain('message: `Log backend yavaş — bu liste eksik:');
    expect(src).toContain("(live || !staticQ.data?.degraded) ? (");
  });
  it('ProblemLogEvidence: degraded → partial → boş sırası (v0.10.452)', () => {
    const src = read('../features/anomalies/ProblemLogEvidence.tsx');
    // v0.10.967 — tablo standardı T12 (dilim 5): durumlar tablonun İÇİNDE.
    // Sıra aynı: degraded (hata satırı, "hiçbir sayı gerçek değil") → partial
    // notu (degraded değilken) → boş (en son dal); satırlar degraded'da çizilmez.
    expect(src).toMatch(/d\?\.degraded \? \{\s*kind: 'error'/);
    expect(src).toContain('hiçbir sayı gerçek değil');
    expect(src).toContain('d?.partial && !d.degraded && !q.isError &&');
    expect(src).toContain('!d.degraded && rows.length > 0');
    expect(src).toMatch(/: \{ kind: 'empty', message: `Bu pencerede/);
  });
  it('LogFieldsPanel: degraded ve partial "No values" ile karışmaz', () => {
    const src = read('../components/LogFieldsPanel.tsx');
    expect(src).toContain('d?.degraded &&');
    expect(src).toContain('d?.partial &&');
    expect(src).toContain('d && !d.degraded && d.values.length === 0');
  });
  it('LogContextModal: degraded state okunur, sıfırlanır, çizilir', () => {
    const src = read('../components/LogContextModal.tsx');
    expect(src).toContain("setDegraded(r?.degraded ?");
    expect((src.match(/setDegraded\(null\)/g) ?? []).length).toBeGreaterThanOrEqual(3);
    expect(src).toContain('{degraded && (');
  });
});
