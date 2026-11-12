// v0.10.2031 — Operator-reported: başlık rozeti "İzleniyor · 4 sa 59 dk"
// derken cümle "5 dakika sürdü (11:15–11:20)" diyordu; listede consumer lag
// olayları saatlerce "İzleniyor" kalıyordu. Rozet yalnız `stage`e bakıyordu;
// kart (sunucu cardOpen) stage + son görülme tazeliğine bakıyor. Epizot
// kapanış satırı süreç içi koşu durumuna bağlı (yeniden başlatma / lider
// değişiminde yazılmaz) olduğundan kapanmamış satırlar sonsuza dek "açık"
// görünüyordu. Kural tek: kapalı damgası YA DA 4 kova boyunca görülmemek = bitti.
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { groupRows, groupStatus, groupStatusText, memberOpen } from './mlGroupRows';

const min = 60e9;
const T = 1_800_000_000e9;
const ev = (o: Partial<MLEvent>): MLEvent => ({
  id: 'e', detector: 'metric_kafka', signal: 'metric', stage: 'shadow', service: 'orders-consumer',
  startedAt: T, lastSeen: T + 5 * min, windowSec: 300, baseline: 0, observed: 162, score: 9, direction: 'up', ...o,
});

describe('durum = kart kuralı (stage + tazelik)', () => {
  it.each<[string, Partial<MLEvent>, number, boolean]>([
    ['kapalı damgası', { stage: 'closed' }, T + 6 * min, false],
    ['açık, son görülme taze (≤ 4 kova)', {}, T + 5 * min + 20 * min, true],
    ['açık damgalı ama 5 sa görülmemiş → bitti', {}, T + 5 * 60 * min, false],
    ['pencere 60 sn: 4 kova = 4 dk', { windowSec: 60 }, T + 5 * min + 5 * min, false],
  ])('%s', (_n, over, now, want) => {
    expect(memberOpen(ev(over), now)).toBe(want);
  });
  it('saatlerce görülmeyen olay "Bitti · 5 dk sürdü" ve gerçek bitişle', () => {
    const now = T + 5 * 60 * min;
    expect(groupStatus([ev({})], now)).toBe('done');
    const g = groupRows([ev({})], now)[0];
    expect(g.status).toBe('done');
    expect(groupStatusText(g, now)).toBe('Bitti · 5 dk sürdü');
  });
});
