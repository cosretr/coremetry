// v0.10.2477 — araştırmanın başlangıç bilgisi anahtarları (sre_agent.graphOrigin /
// impactEvents / similarCases): varsayılan açık, açıkken alan yollanmaz (nil = açık),
// kapalı false gider ve geri okunur; Go Settings alanlarıyla kaynak-pini.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { SRESettings } from '@/lib/types';
import { sreDefaultForm, sreFormToWire, sreToForm } from './sreSettingsForm';

const stored: SRESettings = {
  enabled: true, autoPriorities: ['P1'],
  maxPerTick: 3, maxPerHour: 40, serviceCooldownMin: 30, maxConcurrent: 4,
  itemBudgetSec: 120, maxAttempts: 3, maxQueue: 500, maxLLMCallsPerHour: 60,
};
const KEYS = ['graphOrigin', 'impactEvents', 'similarCases'] as const;

describe('araştırmanın başlangıç bilgisi', () => {
  it('varsayılan açık, alan yollamaz; kapalı false gider ve geri okunur', () => {
    for (const k of KEYS) {
      expect(sreDefaultForm()[k]).toBe(true);
      const def = sreFormToWire(sreToForm(stored));
      expect(def.ok && k in def.wire).toBe(false);
      const off = sreFormToWire({ ...sreToForm(stored), [k]: false });
      expect(off.ok && off.wire[k]).toBe(false);
      expect(sreToForm({ ...stored, [k]: false })[k]).toBe(false);
    }
  });
  it('Go sre.Settings JSON adlarıyla aynı', () => {
    const go = readFileSync(resolve(__dirname, '../../../../internal/sre/settings.go'), 'utf8');
    for (const k of KEYS) expect(go).toContain(`json:"${k},omitempty"`);
  });
});
