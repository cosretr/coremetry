// v0.10.802 — incident zaman çizelgesinde "severity_raised" olayı etiketli ve kırmızı.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('v0.10.802 — severity_raised timeline event', () => {
  const src = readFileSync(resolve(__dirname, './Incident.tsx'), 'utf8');
  it('has a label and an error-toned icon', () => {
    expect(src).toContain("case 'severity_raised':  return 'Severity raised';");
    expect(src).toContain("case 'severity_raised':  return { icon: <AlertTriangle size={16} />, token: '--err' };");
  });
});
