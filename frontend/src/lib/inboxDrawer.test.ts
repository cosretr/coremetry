import { describe, it, expect } from 'vitest';
import { anomalyEventSilenceBody } from './inboxDrawer';

// v0.10.1032 (operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor.
// Exception gibi detay gözükmüyor.") — Mute… tam sayfa anomali detayında;
// sayfanın elinde AnomalyEvent var. Gövde lib/actions.ts'in silence-anomaly
// eylemiyle aynı şekil: fingerprint = olayın doğal kimliği.
// v0.10.1081 — çekmece kalktı; satırdan kurulan ikiz kurucu (ve eylem
// matrisi, kök-neden çıpası, ?item= çözümü) onunla birlikte silindi.
describe('anomalyEventSilenceBody', () => {
  it('olaydan susturma gövdesi', () => {
    expect(anomalyEventSilenceBody(
      { id: 'anom-9', kind: 'log_pattern', pattern: 'timeout x', service: 'checkout' }, 900,
    )).toEqual({
      fingerprint: 'anom-9', kind: 'log_pattern', pattern: 'timeout x', service: 'checkout', durationSec: 900,
    });
  });
  it('boş kimlik → null (Mute düğmesini korur)', () => {
    expect(anomalyEventSilenceBody({ id: '', kind: 'k', pattern: 'p', service: 's' }, 60)).toBeNull();
  });
});
