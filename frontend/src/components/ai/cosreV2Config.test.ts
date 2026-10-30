// cosreV2Config.test — v0.10.1647 (CoSRE v2 Faz 2b B1). Sunucu
// /api/copilot/config'te `cosreV2:true` gönderiyordu ama normalizeCopilotConfig
// bilinmeyen alanı DÜŞÜRÜYORDU → bayrak FE'ye hiç ulaşmıyor, kapı hep v1
// seçiyordu (v0.10.1138 profiles / v0.10.1461 codeRead düşüşünün ikizi).
import { describe, it, expect } from 'vitest';
import { normalizeCopilotConfig } from './useCopilotEnabled';

describe('normalizeCopilotConfig: cosreV2', () => {
  it.each([
    [{ enabled: true, cosreV2: true }],
    [{ enabled: false, cosreV2: true }],
  ])('%j → cosreV2 taşınır', (raw) => {
    expect(normalizeCopilotConfig(raw).cosreV2).toBe(true);
  });

  it.each([
    [{ enabled: true }],
    [{ enabled: true, cosreV2: false }],
    // Tip dışı değer (bozuk yanıt) açık sayılmaz.
    [{ enabled: true, cosreV2: 'true' as unknown as boolean }],
  ])('%j → alan yok', (raw) => {
    expect('cosreV2' in normalizeCopilotConfig(raw)).toBe(false);
  });

  it('null / undefined → kapalı, alan yok', () => {
    expect('cosreV2' in normalizeCopilotConfig(null)).toBe(false);
    expect('cosreV2' in normalizeCopilotConfig(undefined)).toBe(false);
  });
});
