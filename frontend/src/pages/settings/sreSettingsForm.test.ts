// v0.10.1286 — AI SRE ayar ekranı: form doğrulaması + PUT gövdesi, ve
// sınır/varsayılan tablosunun internal/sre/settings.go ile kaynak-pini (Go
// sınırı değişip form değişmezse form ya geçerli değeri reddeder ya da
// sunucunun sessizce kırpacağı değeri kabul eder).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { SRESettings } from '@/lib/types';
import {
  SRE_DEFAULT_PRIORITIES, SRE_NUM_BOUNDS, sreDefaultForm, sreFormToWire, sreSavedSummaryTR,
  sreToForm, togglePriority, validateSREForm, type SREForm,
} from './sreSettingsForm';

const stored: SRESettings = {
  enabled: true, autoPriorities: ['P1'],
  maxPerTick: 3, maxPerHour: 40, serviceCooldownMin: 30, maxConcurrent: 4,
  itemBudgetSec: 120, maxAttempts: 3, maxQueue: 500, maxLLMCallsPerHour: 60,
  updatedBy: 'ops@x', updatedAt: 1,
};

const withNum = (f: SREForm, k: keyof SREForm['nums'], v: string): SREForm => ({ ...f, nums: { ...f.nums, [k]: v } });

describe('sreSettingsForm — Go aynası', () => {
  const go = readFileSync(resolve(__dirname, '../../../../internal/sre/settings.go'), 'utf8');
  const goField: Record<string, string> = {
    maxPerTick: 'MaxPerTick', maxPerHour: 'MaxPerHour', serviceCooldownMin: 'ServiceCooldownMin',
    maxConcurrent: 'MaxConcurrent', itemBudgetSec: 'ItemBudgetSec', maxAttempts: 'MaxAttempts',
    maxQueue: 'MaxQueue', maxLLMCallsPerHour: 'MaxLLMCallsPerHour',
  };
  it('her alanın min/max değeri NormalizeSettings clampInt satırıyla aynı', () => {
    for (const b of SRE_NUM_BOUNDS) {
      const f = goField[b.key];
      const m = go.match(new RegExp(`c\\.${f} = clampInt\\(c\\.${f}, (\\d+), (\\d+), d\\.${f}\\)`));
      expect(m, `${f} clampInt satırı bulunamadı`).not.toBeNull();
      expect([b.min, b.max], f).toEqual([Number(m![1]), Number(m![2])]);
    }
  });
  it('varsayılanlar DefaultSettings ile aynı', () => {
    const body = go.slice(go.indexOf('func DefaultSettings()'), go.indexOf('func clampInt'));
    for (const b of SRE_NUM_BOUNDS) {
      const m = body.match(new RegExp(`\\b${goField[b.key]}: (\\d+)`));
      expect(m, goField[b.key]).not.toBeNull();
      expect(b.def, b.key).toBe(Number(m![1]));
    }
    expect(go).toContain('var DefaultAutoPriorities = []string{"P1", "P2"}');
    expect([...SRE_DEFAULT_PRIORITIES]).toEqual(['P1', 'P2']);
  });
});

describe('sreToForm', () => {
  it('saklı değerler kutulara yazılır', () => {
    const f = sreToForm(stored);
    expect(f.enabled).toBe(true);
    expect(f.priorities).toEqual(['P1']);
    expect(f.nums.maxQueue).toBe('500');
  });
  it('enabled yok = açık; autoPriorities yok = varsayılan [P1, P2]', () => {
    const { enabled: _e, autoPriorities: _a, ...rest } = stored;
    const f = sreToForm(rest);
    expect(f.enabled).toBe(true);
    expect(f.priorities).toEqual(['P1', 'P2']);
  });
  it('autoPriorities [] = hiçbiri seçili değil (nil ile karışmaz)', () => {
    expect(sreToForm({ ...stored, autoPriorities: [] }).priorities).toEqual([]);
  });
  it('enabled false korunur; 0 alan boş kutu', () => {
    const f = sreToForm({ ...stored, enabled: false, maxQueue: 0 });
    expect(f.enabled).toBe(false);
    expect(f.nums.maxQueue).toBe('');
  });
});

describe('validateSREForm', () => {
  it('boş form geçerli (hepsi varsayılan)', () => {
    expect(validateSREForm(sreDefaultForm())).toEqual({});
  });
  it('alt/üst sınır dahil geçerli, dışı hata', () => {
    const f = sreToForm(stored);
    expect(validateSREForm(withNum(f, 'itemBudgetSec', '30'))).toEqual({});
    expect(validateSREForm(withNum(f, 'itemBudgetSec', '600'))).toEqual({});
    expect(validateSREForm(withNum(f, 'itemBudgetSec', '29')).itemBudgetSec).toBe('30–600 aralığında olmalı');
    expect(validateSREForm(withNum(f, 'maxQueue', '1001')).maxQueue).toBe('10–1000 aralığında olmalı');
    expect(validateSREForm(withNum(f, 'maxPerTick', '0')).maxPerTick).toBe('1–10 aralığında olmalı');
  });
  it('tam sayı olmayan hata — sessizce yuvarlanmaz', () => {
    const f = sreToForm(stored);
    for (const v of ['2.5', '-1', 'abc', '1e2']) {
      expect(validateSREForm(withNum(f, 'maxConcurrent', v)).maxConcurrent, v).toBe('Tam sayı olmalı');
    }
  });
});

describe('sreFormToWire — PUT gövdesi', () => {
  it('saklı ayar gidip dönünce aynı gövde', () => {
    const r = sreFormToWire(sreToForm(stored));
    expect(r).toEqual({
      ok: true,
      wire: {
        enabled: true, autoPriorities: ['P1'],
        maxPerTick: 3, maxPerHour: 40, serviceCooldownMin: 30, maxConcurrent: 4,
        itemBudgetSec: 120, maxAttempts: 3, maxQueue: 500, maxLLMCallsPerHour: 60,
      },
    });
  });
  it('boş kutu varsayılanı yollar; updatedBy/updatedAt gönderilmez', () => {
    const r = sreFormToWire(sreDefaultForm());
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.wire).toEqual({
      enabled: true, autoPriorities: ['P1', 'P2'],
      maxPerTick: 2, maxPerHour: 20, serviceCooldownMin: 15, maxConcurrent: 2,
      itemBudgetSec: 90, maxAttempts: 2, maxQueue: 200, maxLLMCallsPerHour: 30,
    });
    expect('updatedBy' in r.wire).toBe(false);
  });
  it('hiç öncelik seçili değil → [] (otomatik kuyruk kapalı), alan atlanmaz', () => {
    const r = sreFormToWire({ ...sreDefaultForm(), priorities: [] });
    expect(r.ok && r.wire.autoPriorities).toEqual([]);
  });
  it('kapalı → enabled:false açıkça gider', () => {
    const r = sreFormToWire({ ...sreDefaultForm(), enabled: false });
    expect(r.ok && r.wire.enabled).toBe(false);
  });
  it('geçersiz alan → gövde yok, hata haritası', () => {
    const r = sreFormToWire(withNum(sreDefaultForm(), 'maxLLMCallsPerHour', '501'));
    expect(r).toEqual({ ok: false, errors: { maxLLMCallsPerHour: '1–500 aralığında olmalı' } });
  });
});

describe('togglePriority + özet', () => {
  it('P1→P3 sırası korunur', () => {
    expect(togglePriority(['P1'], 'P3')).toEqual(['P1', 'P3']);
    expect(togglePriority(['P3', 'P1'], 'P2')).toEqual(['P1', 'P2', 'P3']);
    expect(togglePriority(['P1', 'P2'], 'P1')).toEqual(['P2']);
  });
  it('özet cümlesi üç hâl', () => {
    expect(sreSavedSummaryTR({ ...stored, enabled: false })).toContain('AI SRE kapalı');
    expect(sreSavedSummaryTR({ ...stored, autoPriorities: [] })).toContain('otomatik kuyruk kapalı');
    expect(sreSavedSummaryTR(stored)).toBe('Kaydedildi — P1 problemleri otomatik araştırılır (saatte en çok 40).');
  });
});

describe('kanıt kapısı kipi (CoSRE Faz B3)', () => {
  it('show / off gidip döner; shadow (boş) alan yollamaz; bilinmeyen → shadow', () => {
    const show = sreFormToWire(sreToForm({ ...stored, evidenceGate: 'show' }));
    expect(show.ok && show.wire.evidenceGate).toBe('show');
    const def = sreFormToWire(sreToForm(stored));
    expect(def.ok && 'evidenceGate' in def.wire).toBe(false);
    expect(sreToForm({ ...stored, evidenceGate: 'weird' as never }).evidenceGate).toBe('');
  });
});

describe('bildirim özeti (CoSRE Faz C1)', () => {
  it('varsayılan açık ve alan yollamaz; kapalı false gider ve geri okunur', () => {
    expect(sreDefaultForm().notifySummary).toBe(true);
    const def = sreFormToWire(sreToForm(stored));
    expect(def.ok && 'notifySummary' in def.wire).toBe(false);
    const off = sreFormToWire({ ...sreToForm(stored), notifySummary: false });
    expect(off.ok && off.wire.notifySummary).toBe(false);
    expect(sreToForm({ ...stored, notifySummary: false }).notifySummary).toBe(false);
  });
});
