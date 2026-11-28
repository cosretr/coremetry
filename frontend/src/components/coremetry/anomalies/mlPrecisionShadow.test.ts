// v0.10.2486 — precision B.2: otomatik gölge kuralının metin yardımcıları.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { MLPrecisionView } from '@/lib/types';
import { fedLine, precisionModeText, precisionOnTransition, precisionStatusOf, recordNumbersText, recordOutcomeText, voteNoteKey } from './mlPrecisionShadow';

const view: MLPrecisionView = {
  mode: 'shadow',
  settings: { mode: 'shadow', minLabels: 20, demoteUpper: 0.5, restoreLower: 0.6, windowDays: 30, maxPerDay: 1, maxShare: 0.25, pins: { template_spike: 'on' } },
  detectors: {
    op_latency: { status: 'would_demote', since: 1795000000, reason: "30 günde 24 etiket, 6'sı doğru; üst sınır %45" },
    error_rate: { status: 'on' },
    template_spike: { status: 'pinned', pin: 'on' },
  },
  records: [], fed: { op_latency: { fed: 7, real: 2 } }, wouldDemote: ['op_latency'],
};

describe('mlPrecisionShadow', () => {
  it('durum rozeti: düşerdi gerekçe + tarih ipucunda; açık; sabitlenmiş; blok yoksa null', () => {
    const w = precisionStatusOf(view, 'op_latency', 'tr');
    expect(w?.text).toBe('düşerdi');
    expect(w?.title).toContain("6'sı doğru; üst sınır %45");
    expect(w?.title).toContain('tarihinden beri');
    expect(precisionStatusOf(view, 'error_rate', 'tr')?.text).toBe('açık');
    expect(precisionStatusOf(view, 'template_spike', 'tr')?.text).toBe('sabitlenmiş');
    expect(precisionStatusOf(undefined, 'op_latency', 'tr')).toBeNull();
  });
  it('kayıt metinleri: sonuç, fren nedeni, sayılar lib/format ile', () => {
    expect(recordOutcomeText('would_demote', 'tr')).toBe('düşerdi');
    expect(recordOutcomeText('blocked:daily_cap', 'tr')).toBe('düşmezdi: günlük sınır');
    const r = { day: '2026-11-28', at: 1, detector: 'op_latency', outcome: 'would_demote', reason: '', n: 24, correct: 6, wrong: 18, lower: 0.12, upper: 0.449, window: 30, mode: 'shadow', problemsFed: 7, problemsReal: 2 };
    expect(recordNumbersText(r, 'tr')).toBe('24 etiket, 6 doğru · üst %45');
    expect(recordNumbersText({ ...r, outcome: 'would_restore', n: 12, correct: 11, lower: 0.65 }, 'tr')).toBe('12 yeni etiket, 11 doğru · alt %65');
  });
  it('on öncesi sayı: beslenen ve gerçek Problem', () => {
    expect(fedLine('Endpoint gecikmesi', view.fed.op_latency, 'tr')).toBe('Endpoint gecikmesi: 7 Problem besledi, 2 tanesi onaylı vakayla gerçek (gürültü değil)');
    expect(precisionModeText('shadow', 'tr')).toContain('yalnız rapor');
  });
  it('on geçişi onay ister; etiket notu yalnız on kipinde değişir', () => {
    expect(precisionOnTransition('shadow', 'on')).toBe(true);
    expect(precisionOnTransition(undefined, 'on')).toBe(true);
    expect(precisionOnTransition('on', 'on')).toBe(false);
    expect(precisionOnTransition('shadow', 'off')).toBe(false);
    expect(voteNoteKey('on')).toBe('mlRecord.vote.noteShadowOn');
    expect(voteNoteKey('shadow')).toBe('mlRecord.vote.note');
    expect(voteNoteKey('off')).toBe('mlRecord.vote.note');
    expect(voteNoteKey(undefined)).toBe('mlRecord.vote.note');
  });
  it('Settings: on onay adımı ve rozet bağlı', () => {
    const src = readFileSync(resolve(__dirname, '../../../pages/settings/MLSubsection.tsx'), 'utf8');
    expect(src).toContain('precisionOnTransition(savedMode, cfg.precisionShadow?.mode)');
    expect(src).toContain('confirmPrecisionOn: goingOn');
    expect(src).toContain('<PrecisionBadge view={pview} detector={d.id} />');
  });
});
