// copilotLauncherProp.test.ts — v0.10.732 (operatör: "Kiosk modunda da
// Explain trace yapılabilsin"). CopilotChat `launcher={false}`: FAB, nudge
// ve kritik-problem poll'u YOK; yalnız ?ai= öznesiyle açılan çekmece.
// Varsayılan (kabuk) davranış aynen.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const cc = readFileSync(resolve(__dirname, 'CopilotChat.tsx'), 'utf8');

describe('CopilotChat launcher kipi (v0.10.732)', () => {
  it('prop varsayılanı true; FAB ve nudge launcher\'a bağlı; poll launcher kapalıyken atılmaz', () => {
    expect(cc).toContain('export function CopilotChat({ launcher = true }: { launcher?: boolean } = {})');
    expect(cc).toContain('{launcher && !drawerOpen && <TraceExplainNudge />}');
    expect(cc).toContain('{launcher && !drawerOpen && (');
    expect(cc).toContain("useOpenCriticalCount({ enabled: enabled === true && launcher })");
  });
  it('çekmece launcher\'dan bağımsız: özne varken açılır', () => {
    expect(cc).toContain('const drawerOpen = open || subject !== null;');
    expect(cc).toContain('{drawerOpen && (');
  });
});
