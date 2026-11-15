import { describe, it, expect } from 'vitest';
import { podStatusWord, podSentence, siblingSentence } from './podSentence';

// Clusters S11 (plan §10.2, §10.3) — saf tablo testi: alan yoksa cümle sadeleşir.
const ctr = (o: Record<string, unknown>) => ({ name: 'app', ready: true, readyKnown: true, restarts: 0, ...o });

describe('pod durum sözcüğü', () => {
  it.each([
    [{ phase: 'Running', restarts: 0 }, undefined, 'Sağlıklı'],
    [{ phase: 'Running', restarts: 3 }, undefined, 'Dikkat'],
    [{ phase: 'Running', restarts: 0, memPct: 92 }, undefined, 'Dikkat'],
    [{ phase: 'Pending' }, undefined, 'Sorunlu'],
    [{ phase: 'Failed' }, undefined, 'Sorunlu'],
    [{ phase: 'Running', restarts: 7, lastTermReason: 'OOMKilled' }, undefined, 'Sorunlu'],
    [{ phase: 'Running' }, [ctr({ waitingReason: 'CrashLoopBackOff' })], 'Sorunlu'],
    [undefined, undefined, null],
  ] as const)('%j + %j → %s', (row, ctrs, want) => {
    expect(podStatusWord(row as never, ctrs as never)).toBe(want);
  });
});

describe('pod düz cümlesi', () => {
  it('tüm parçalar', () => {
    expect(podSentence({ phase: 'Running', restarts: 7, lastTermReason: 'OOMKilled', memPct: 99 }))
      .toBe("Çalışıyor, toplam 7 yeniden başlama, bellek limitini aştı, bellek limitinin %99'unda.");
  });
  it('bekleme sebebi fazı ezer; Completed düşer; alan yoksa yalnız faz', () => {
    expect(podSentence({ phase: 'Pending' }, [ctr({ waitingReason: 'ImagePullBackOff' })])).toBe('Bekliyor (imaj çekilemedi).');
    expect(podSentence({ phase: 'Succeeded', lastTermReason: 'Completed' })).toBe('Sonlandı.');
    expect(podSentence({ phase: 'Running', restarts: 0 })).toBe('Çalışıyor.');
    expect(podSentence(undefined)).toBe('');
  });
  it('satır yokken konteynerlerden restart ve neden', () => {
    expect(podSentence({ phase: 'Running', restartsUnknown: true }, [ctr({ restarts: 2, lastTermReason: 'Error' })]))
      .toBe('Çalışıyor, toplam 2 yeniden başlama, hata ile sonlandı.');
  });
});

describe('kardeş kıyası', () => {
  it('yüzde varsa yüzdeyle; en yüksek / ortalamanın içinde', () => {
    expect(siblingSentence({ memPct: 99 }, [{ memPct: 60 }, { memPct: 72 }, { memPct: 86 }]))
      .toBe("Aynı iş yükündeki 3 pod'un bellek ortancası %72, bu pod %99 — en yüksek.");
    expect(siblingSentence({ memPct: 70 }, [{ memPct: 60 }, { memPct: 86 }]))
      .toBe("Aynı iş yükündeki 2 pod'un bellek ortancası %73, bu pod %70 — ortalamanın içinde.");
  });
  it('yüzde yoksa bayt; kardeş yoksa cümle yok', () => {
    expect(siblingSentence({ memBytes: 2 * 1024 ** 3 }, [{ memBytes: 1024 ** 3 }]))
      .toBe("Aynı iş yükündeki 1 pod'un bellek ortancası 1.00 GiB, bu pod 2.00 GiB — en yüksek.");
    expect(siblingSentence({ memPct: 50 }, [])).toBe('');
    expect(siblingSentence(undefined, [{ memPct: 5 }])).toBe('');
  });
});
