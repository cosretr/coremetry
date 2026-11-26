// sreReplayView.test.ts — replay panelinin saf biçim yardımcıları.
import { describe, it, expect } from 'vitest';
import type { SREReplayListItem, SREReplayRun } from '@/lib/types';
import { buildText, defaultBase, deltaText, pathCell, pathTitle, pctText, reachCell, reportMode, runLabel, runTitle, scoreText, scoreTrend, versionTrend } from './sreReplayView';

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
    expect(reportMode({ variant: { plain: true }, trigger: 'manual' })).toBe('B kapalı (plain)');
  });

  it('yol hücresi: okuma / dalga / boşa + tavan', () => {
    const runs: SREReplayRun[] = [
      { rep: 1, state: 'done', path: { steps: 7, toolCalls: 5, rounds: 2, wasted: 2, wastedSteps: ['errors', 'log_spike'] } },
      { rep: 2, state: 'done', path: { steps: 8, toolCalls: 6, rounds: 3, wasted: 3, capHit: true, cap: 'budget' } },
      { rep: 3, state: 'queued' },
    ];
    expect(pathCell(runs)).toBe('5 okuma · 2 dalga · 2 boşa | 6 okuma · 3 dalga · 3 boşa · bütçe tavanı');
    expect(pathCell([{ rep: 1, state: 'queued' }])).toBeNull();
    expect(pathTitle(runs)).toContain('boşa: errors, log_spike');
  });

  it('işaretli fark ve sürüm metni', () => {
    expect(deltaText(12.5)).toBe('+12,5');
    expect(deltaText(-3)).toBe('−3');
    expect(deltaText(0)).toBe('0');
    expect(deltaText(null)).toBe('—');
    expect(deltaText(0.25, true)).toBe('+25 puan');
    expect(buildText({ build: 'v0.10.1' })).toBe('v0.10.1');
    expect(buildText({ build: 'v0.10.1', gradeBuild: 'v0.10.2' })).toBe('v0.10.1 → v0.10.2');
    expect(buildText({})).toBe('—');
  });

  it('sürüm eğilimi: sürüm başına ortalama, sürümsüz / puansız rapor yok', () => {
    const mk = (id: string, build: string | undefined, score: number | null, at: number) =>
      ({ reportId: id, build, createdAt: at, summary: { score } }) as unknown as SREReplayListItem;
    const pts = versionTrend([mk('a', 'v1', 40, 1), mk('b', 'v1', 60, 2), mk('c', 'v2', 80, 3), mk('d', undefined, 90, 4), mk('e', 'v3', null, 5)]);
    expect(pts.map(p => [p.build, p.score, p.reports])).toEqual([['v1', 50, 2], ['v2', 80, 1]]);
  });

  it('varsayılan taban: seçiliden önceki en yeni rapor', () => {
    const reps = [{ reportId: 'c', createdAt: 3 }, { reportId: 'a', createdAt: 1 }, { reportId: 'b', createdAt: 2 }];
    expect(defaultBase(reps, 'c')).toBe('b');
    expect(defaultBase(reps, 'a')).toBe('');
    expect(defaultBase(reps, 'zz')).toBe('');
  });
});
