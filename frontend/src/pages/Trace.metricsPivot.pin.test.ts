import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1240 — trace → metrics pivotu vardı. v0.10.1969 (operatör: "Logs, Metrics düğmelerine gerek
// yok; Share en sağda, Export ve Kiosk menünün içinde") — eylem satırı yalnız Compare + Share.
const src = readFileSync(resolve(__dirname, 'Trace.tsx'), 'utf8');

describe('Trace eylem satırı', () => {
  it('Logs / Metrics düğmesi yok; Export JSON + Kiosk Share menüsünde', () => {
    expect(src).not.toContain('<DrillButton to="/metrics"');
    expect(src).not.toContain('<DrillButton to="/logs"');
    expect(src).toContain('<SharePopover traceId={id}\n                  onExport={() => exportTraceJSON(id, spans)}');
    expect(src).toContain("onKiosk={() => window.open(kioskHref, '_blank', 'noopener,noreferrer')} />");
  });
});
