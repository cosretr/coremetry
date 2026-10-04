// kafkaLabelScope.test.ts — v0.10.1102 (operatör-onaylı): Kafka istemcileri
// sekmesinin topic / client_id seçici önerileri SAYFA kapsamında. Filo
// geneli öneri (v0.10.1097) bu topic'e hiç dokunmayan bir client_id'yi
// sunup boş panel veriyordu. Çivilenen:
//   • kapsam = sayfa anahtarları (system / cluster / destination) + UYGULANMIŞ
//     süzgeç; servis listesi istemciden GİTMEZ (sunucu türetir — 200+200 adlık
//     sorgu dizesi başlık tamponunu aşar, virgüllü ad bölünür);
//   • api.kafkaLabelValues'un GERÇEKTEN çıkan URL'i sayfa anahtarlarını ve
//     karşı süzgeci taşır, aranan etiketin kendi süzgecini taşımaz;
//   • arama imzası yalnız etkili girdilerle değişir.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/lib/api';
import { kafkaLabelScopeOf, kafkaLabelScopeSig } from './kafkaTab';

const PAGE = { system: 'kafka', cluster: 'prod-eu', destination: 'orders.v1' };
const DATA = { filter: { topic: 'orders.v1', clientId: 'consumer-7' } };

function captureFetch(): string[] {
  const urls: string[] = [];
  vi.stubGlobal('fetch', (url: unknown) => {
    urls.push(String(url));
    return Promise.resolve(new Response('{"label":"x","source":"vm","values":[]}', {
      status: 200, headers: { 'content-type': 'application/json' },
    }));
  });
  return urls;
}

describe('kafkaLabelScopeOf', () => {
  it('sayfa anahtarları + uygulanmış süzgeç', () => {
    expect(kafkaLabelScopeOf(PAGE, DATA)).toEqual({ ...PAGE, topic: 'orders.v1', clientId: 'consumer-7' });
  });
  it('veri / süzgeç yok → yalnız sayfa anahtarları', () => {
    expect(kafkaLabelScopeOf(PAGE, null)).toEqual({ ...PAGE, topic: undefined, clientId: undefined });
  });
});

describe('kafkaLabelScopeSig', () => {
  const sc = kafkaLabelScopeOf(PAGE, DATA);
  it('aranan etiketin kendi süzgeci imzaya girmez', () => {
    expect(kafkaLabelScopeSig('client_id', sc)).toBe(kafkaLabelScopeSig('client_id', { ...sc, clientId: 'other' }));
    expect(kafkaLabelScopeSig('topic', sc)).toBe(kafkaLabelScopeSig('topic', { ...sc, topic: 'other' }));
  });
  it('karşı süzgeç ve sayfa anahtarları imzayı değiştirir', () => {
    expect(kafkaLabelScopeSig('client_id', sc)).not.toBe(kafkaLabelScopeSig('client_id', { ...sc, topic: 'payments' }));
    expect(kafkaLabelScopeSig('topic', sc)).not.toBe(kafkaLabelScopeSig('topic', { ...sc, destination: 'payments' }));
    expect(kafkaLabelScopeSig('topic', sc)).not.toBe(kafkaLabelScopeSig('topic', { ...sc, cluster: 'c2' }));
  });
});

describe('api.kafkaLabelValues — giden URL sayfa kapsamını taşır', () => {
  afterEach(() => vi.unstubAllGlobals());
  const sc = kafkaLabelScopeOf(PAGE, DATA);

  it('client_id: sayfa anahtarları + seçili topic; client süzgeci ve servis listesi YOK', async () => {
    const urls = captureFetch();
    await api.kafkaLabelValues('client_id', 'cons', 1, 2, sc);
    const p = new URL(urls[0], 'http://x').searchParams;
    expect(p.get('label')).toBe('client_id');
    expect(p.get('system')).toBe('kafka');
    expect(p.get('cluster')).toBe('prod-eu');
    expect(p.get('destination')).toBe('orders.v1');
    expect(p.get('topicFilter')).toBe('orders.v1');
    expect(p.has('clientFilter')).toBe(false);
    expect(p.has('producers')).toBe(false);
    expect(p.has('consumers')).toBe(false);
  });

  it('topic: sayfa anahtarları + seçili client_id; topic süzgeci YOK', async () => {
    const urls = captureFetch();
    await api.kafkaLabelValues('topic', '', 1, 2, sc);
    const p = new URL(urls[0], 'http://x').searchParams;
    expect(p.get('clientFilter')).toBe('consumer-7');
    expect(p.has('topicFilter')).toBe(false);
    expect(p.get('destination')).toBe('orders.v1');
  });
});
