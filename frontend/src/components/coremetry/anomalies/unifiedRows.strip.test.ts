// v0.10.2030 — Operator-reported: listede "Ne oldu" servis adını tekrar
// ediyordu (servis kendi sütununda). Önek kırpılır, ilk harf büyür.
import { describe, it, expect } from 'vitest';
import { listHeadline, stripServicePrefix } from './unifiedRows';

describe('stripServicePrefix', () => {
  it.each([
    ['orders-api: consumer lag normalde hiç görülmez', 'orders-api', 'Consumer lag normalde hiç görülmez'],
    ['orders-api servisinin POST /pay operasyonunda hata sayısı 40\'a çıktı', 'orders-api', 'POST /pay operasyonunda hata sayısı 40\'a çıktı'],
    ['orders-api servisi, prod-east cluster\'ında heap arttı', 'orders-api', 'Prod-east cluster\'ında heap arttı'],
    ['orders-api POST /pay hata vermeye başladı', 'orders-api', 'POST /pay hata vermeye başladı'],
    ['orders-api: ilk kez görülen kalıp', 'orders-api', 'İlk kez görülen kalıp'],
    ['başka servis: bir şey', 'orders-api', 'Başka servis: bir şey'],
  ])('%s', (text, svc, want) => {
    expect(stripServicePrefix(text, svc)).toBe(want);
  });
  it('listHeadline satırın servisini kullanır', () => {
    expect(listHeadline({ headline: 'orders-batch: yeni bir log kalıbı belirdi, 25 satır', service: 'orders-batch' }))
      .toBe('Yeni bir log kalıbı belirdi, 25 satır');
  });
});
