// sreReplayView.test.ts — replay panelinin saf biçim yardımcıları.
import { describe, it, expect } from 'vitest';
import type { SREReplayListItem, SREReplayRun } from '@/lib/types';
import { pctText, reachCell, reportMode, runLabel, runTitle, scoreText, scoreTrend } from './sreReplayView';

describe('sreReplayView', () => {
  it('puan ve oran biçimi', () => {
    expect(scoreText(67.5)).toBe('67,5');
    expect(scoreText(100)).toBe('100');
    expect(scoreText(null)).toBe('—');
    expect(pctText(0.75)).toBe('%75');
    expect(pctText(undefined)).toBe('—');
  });

  it('koşu etiketi: not > not hatası > durum', () => {
    expect(runLabel({ rep: 1, state: 'done', grade: 'miss' })).toBe('Iska');
    expect(runLabel({ rep: 1, state: 'done', gradeErr: 'hakem çalışmadı' })).toBe('notsuz');
    expect(runLabel({ rep: 1, state: 'queued' })).toBe('kuyrukta');
    const t = runTitle({ rep: 2, state: 'done', grade: 'bullseye', why: 'aynı neden', reach: false, reachBy: 'evidence', conclusion: '1. Deploy' });
    expect(t).toContain('koşu 2');
    expect(t).toContain('suçluya varmadı (kanıt)');
    expect(t).toContain('sonuç: 1. Deploy');
  });

  it('erişim hücresi yalnız bilinen koşuları sayar', () => {
    const runs: SREReplayRun[] = [{ rep: 1, state: 'done', reach: true }, { rep: 2, state: 'done', reach: false }, { rep: 3, state: 'done' }];
    expect(reachCell(runs)).toBe('1/2');
    expect(reachCell([{ rep: 1, state: 'done' }])).toBeNull();
  });

  it('eğilim eskiden yeniye, notsuz rapor atlanır', () => {
    const r = (id: string, at: number, score: number | null) => ({ reportId: id, createdAt: at, summary: { score } }) as unknown as SREReplayListItem;
    expect(scoreTrend([r('b', 2, 70), r('a', 1, 40), r('c', 3, null)])).toBe('40 → 70');
    expect(scoreTrend([])).toBe('—');
  });

  it('kip: varyant + tetik', () => {
    expect(reportMode({ variant: { contextPack: true }, trigger: 'schedule' })).toBe('bağlam paketi · haftalık');
    expect(reportMode({ variant: {}, trigger: 'manual' })).toBe('varsayılan');
  });
});
