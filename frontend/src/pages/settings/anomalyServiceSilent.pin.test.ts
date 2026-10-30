// anomalyServiceSilent.pin.test.ts — v0.10.543 kaynak pini: service_silent
// kutusu `=== true` ile okunur (varsayılan KAPALI; attachToIncident'ın tersi).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'AnomalyTab.tsx'), 'utf8');

describe('service_silent ayarı', () => {
  it('kutu === true okur, serviceSilent yazar', () => {
    expect(src).toContain('checked={cfg.serviceSilent === true}');
    // Faz 2 dilim 1 R17 — Radix CheckboxField: onCheckedChange(boolean).
    expect(src).toContain('onCheckedChange={v => setCfg({ ...cfg, serviceSilent: v })}');
    expect(src).not.toContain('cfg.serviceSilent !== false');
  });
});
