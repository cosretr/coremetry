import { describe, expect, it } from 'vitest';
import { evidenceBlocks, evidenceDeltaClass, evidenceHasHypothesis, fmtDelta, fmtRangeTR, isChatEvidence, redRows } from './chatEvidence';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { ChatEvidence, ChatTypedBlock } from './types';

// v0.10.558 — kanıt kartı saf çekirdeği.
const ev: ChatEvidence = {
  question: 'root_cause', service: 'checkout', rangeS: 3600,
  red: { current: { spans: 10, rate: 41, errorRate: 12.5, p95Ms: 900, p99Ms: 1200 }, baseline: { spans: 9, rate: 39, errorRate: 1, p95Ms: 120, p99Ms: 200 } },
  problems: [{ id: 'p1', ruleName: 'r', severity: 'critical', status: 'open', startedAt: 1, metric: 'error_rate', value: 12, threshold: 5, topSuspect: 'ledger', confidence: 0.82 }],
  changes: [], logPatterns: [], verdict: 'kök-neden şüphelisi ledger (güven 0.82)',
};

describe('chatEvidence', () => {
  it('blok süzgeci tip + şekil ister', () => {
    const blocks: ChatTypedBlock[] = [
      { id: 'b1', type: 'chart', seq: 1, final: true, payload: { service: 'x', agg: 'p95' } },
      { id: 'b2', type: 'evidence', seq: 2, final: true, payload: ev },
      { id: 'b3', type: 'evidence', seq: 3, final: true, payload: { service: 'x' } },
    ];
    expect(evidenceBlocks(blocks)).toEqual([ev]);
    expect(isChatEvidence(null)).toBe(false);
  });
  it('delta: yeni / ~ / ×N', () => {
    expect(fmtDelta(5, 0)).toEqual({ dir: 'new', text: '▲ yeni' });
    expect(fmtDelta(0, 0).dir).toBe('flat');
    expect(fmtDelta(12.5, 1)).toEqual({ dir: 'up', text: '▲ ×12.5' });
    expect(fmtDelta(60, 120)).toEqual({ dir: 'down', text: '▼ ×2.0' });
    expect(fmtDelta(41, 39).text).toBe('~');
  });
  it('RED satırları biçimli', () => {
    const rows = redRows(ev.red);
    expect(rows.map(r => r.key)).toEqual(['errorRate', 'p95', 'rate']);
    expect(rows[0]).toMatchObject({ now: '%13', base: '%1.0', delta: { dir: 'up' } });
    expect(rows[1]).toMatchObject({ now: '900 ms', base: '120 ms' });
    expect(redRows(undefined)).toEqual([]);
    expect(rows[2].label).toBe('span/sn'); // v0.10.944 — tüm span türleri, istek hızı değil
  });
  // v0.10.944 — okunamayan pencere 0 diye biçimlenmez.
  it('eksik taban: "▲ yeni" yok, taban "—"; eksik şimdi: satır yok', () => {
    const noBase = redRows({ current: ev.red!.current, baselineUnavailable: 'timeout' });
    expect(noBase).toHaveLength(3);
    for (const r of noBase) {
      expect(r.base).toBe('—');
      expect(r.delta).toEqual({ dir: 'flat', text: 'taban okunamadı' });
      expect(r.delta.text).not.toContain('yeni');
      expect(evidenceDeltaClass(r)).toBe('ev-delta ev-delta--flat');
    }
    expect(redRows({ baseline: ev.red!.baseline, currentUnavailable: 'unreachable' })).toEqual([]);
  });
  // v0.10.929 (K5) — yükselişin iyi olduğu satır (throughput) kırmızı basılmaz.
  it('yön bayrağı: hata oranı / p95 yükselişi kötü, span/sn yükselişi nötr', () => {
    const rows = redRows(ev.red);
    expect(rows.map(r => [r.key, r.upIsBad])).toEqual([['errorRate', true], ['p95', true], ['rate', false]]);
    const up = fmtDelta(80, 40), fresh = fmtDelta(5, 0), down = fmtDelta(20, 40), flat = fmtDelta(41, 39);
    // Yükseliş kötüyse sapma tonu.
    expect(evidenceDeltaClass({ delta: up, upIsBad: true })).toBe('ev-delta ev-delta--up');
    expect(evidenceDeltaClass({ delta: fresh, upIsBad: true })).toBe('ev-delta ev-delta--new');
    // Yükseliş iyiyse (span/sn) nötr: kırmızı/amber değiştirici yok.
    expect(evidenceDeltaClass({ delta: up, upIsBad: false })).toBe('ev-delta');
    expect(evidenceDeltaClass({ delta: fresh, upIsBad: false })).toBe('ev-delta');
    // Düşüş / ~ her iki yönde kendi nötr sınıfı.
    expect(evidenceDeltaClass({ delta: down, upIsBad: false })).toBe('ev-delta ev-delta--down');
    expect(evidenceDeltaClass({ delta: flat, upIsBad: true })).toBe('ev-delta ev-delta--flat');
    // Uçtan uca: span/sn ×2 artışı .ev-delta--up almaz.
    const rate = redRows({ current: { ...ev.red!.current!, rate: 80 }, baseline: { ...ev.red!.baseline!, rate: 40 } })
      .find(r => r.key === 'rate')!;
    expect(rate.delta.dir).toBe('up');
    expect(evidenceDeltaClass(rate)).not.toContain('ev-delta--up');
  });

  it('EvidenceCard Δ hücresi sınıfı yön bayrağından alır (dir\'den doğrudan değil)', () => {
    const src = readFileSync(resolve(__dirname, '../components/ai/EvidenceCard.tsx'), 'utf8');
    expect(src).toContain('<td className={evidenceDeltaClass(r)}>{r.delta.text}</td>');
    expect(src).not.toContain('ev-delta--${r.delta.dir}');
  });

  it('aralık + hipotez', () => {
    expect(fmtRangeTR(3600)).toBe('son 1 sa');
    expect(fmtRangeTR(1800)).toBe('son 30 dk');
    expect(fmtRangeTR(172800)).toBe('son 2 gün');
    expect(evidenceHasHypothesis(ev)).toBe(true);
    expect(evidenceHasHypothesis({ ...ev, problems: [] })).toBe(false);
  });
});
