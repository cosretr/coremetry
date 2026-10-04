// kafkaTab.test.ts — v0.10.1097 ("Kafka istemcileri" sekmesi). SAF yardımcılar:
//   • etiket varlığı → denetim görünürlüğü (etiket yoksa denetim YOK);
//   • pod görünümü: ≤ 12 çizgi + "diğer N" (sunucu katlaması ya da savunma);
//   • URL gidiş-dönüş (ktopic / kclient / kview), yabancı param korunur;
//   • kısa kaynak satırı + adım biçimi; bağlantı paneli durumları.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import type { KafkaConnections, SpanMetricSeries } from '@/lib/types';
import {
  fmtStepTR, kafkaConnState, kafkaPodItems, kafkaSourceLineTR, kafkaTabControls,
  KAFKA_POD_CAP, parseKafkaTabState, writeKafkaTabState,
} from './kafkaTab';

const pod = (i: number, v = i): SpanMetricSeries => ({
  groupKey: ['svc-a', `svc-a-7d9f-${i}`],
  points: [{ time: 1e9, value: 1 }, { time: 2e9, value: v }],
});

describe('kafkaTabControls — etiket varlığı → denetim', () => {
  it.each([
    ['hepsi', { detected: true, topic: true, clientId: true, pod: 'k8s_pod_name' }, { topic: true, client: true, podToggle: true }],
    ['topic yok (prod tipik)', { detected: true, topic: false, clientId: true, pod: 'k8s_pod_name' }, { topic: false, client: true, podToggle: true }],
    ['pod yok', { detected: true, topic: false, clientId: true }, { topic: false, client: true, podToggle: false }],
    ['keşif yapılamadı (CH / hata) → hiçbiri', { detected: false, topic: true, clientId: true, pod: 'pod' }, { topic: false, client: false, podToggle: false }],
  ] as const)('%s', (_n, labels, want) => {
    expect(kafkaTabControls(labels)).toEqual(want);
  });
  it('eski sunucu (alan yok) → hiçbiri', () => {
    expect(kafkaTabControls(undefined)).toEqual({ topic: false, client: false, podToggle: false });
    expect(kafkaTabControls(null)).toEqual({ topic: false, client: false, podToggle: false });
  });
});

describe('kafkaPodItems — 12 çizgi + "diğer N"', () => {
  it('sunucu katlaması: son seri "diğer N", soluk + kesikli', () => {
    const series = [...Array.from({ length: 12 }, (_, i) => pod(i)), { groupKey: ['diğer 7'], points: [{ time: 2e9, value: 9 }] }];
    const { items, folded } = kafkaPodItems(series, 7);
    expect(items).toHaveLength(13);
    expect(folded).toBe(7);
    const last = items[12];
    expect(last.name).toBe('diğer 7');
    expect(last.role).toBe('muted');
    expect(last.dashed).toBe(true);
    // Ortak servis sütunu düşer, pod adı kalır.
    expect(items[0].name).toBe('svc-a-7d9f-0');
  });
  it('savunma katlaması: katlanmamış 20 seri → 12 + "diğer 8" (toplam)', () => {
    const { items, folded } = kafkaPodItems(Array.from({ length: 20 }, (_, i) => pod(i)));
    expect(items).toHaveLength(KAFKA_POD_CAP + 1);
    expect(folded).toBe(8);
    expect(items[12].name).toBe('diğer 8');
    // pod 12..19 son adım toplamı = 12+…+19 = 124; ilk adım 8×1.
    expect(items[12].series[0].points).toEqual([{ time: 1e9, value: 8 }, { time: 2e9, value: 124 }]);
  });
  it('≤ 12 seri aynen, "diğer" yok', () => {
    const { items, folded } = kafkaPodItems(Array.from({ length: 5 }, (_, i) => pod(i)));
    expect(items).toHaveLength(5);
    expect(folded).toBe(0);
    expect(items.some(i => i.name.startsWith('diğer'))).toBe(false);
  });
  it('boş / null güvenli', () => {
    expect(kafkaPodItems(null).items).toEqual([]);
    expect(kafkaPodItems([], 3).items).toEqual([]);
  });
});

describe('URL durumu — gidiş-dönüş', () => {
  it('yazılan okunur; yabancı param korunur; varsayılan silinir', () => {
    const prev = new URLSearchParams('system=kafka&cluster=c1&destination=orders&tab=clients&range=1h');
    const p1 = writeKafkaTabState(prev, { topic: ' payments ', client: 'consumer-orders-1', view: 'pod' });
    expect(parseKafkaTabState(p1)).toEqual({ topic: 'payments', client: 'consumer-orders-1', view: 'pod' });
    for (const k of ['system', 'cluster', 'destination', 'tab', 'range']) expect(p1.get(k)).toBe(prev.get(k));
    // prev mutasyona uğramaz
    expect(prev.has('ktopic')).toBe(false);
    // Kısmi yama yalnız kendi alanına dokunur.
    const p2 = writeKafkaTabState(p1, { view: '' });
    expect(parseKafkaTabState(p2.toString())).toEqual({ topic: 'payments', client: 'consumer-orders-1', view: '' });
    expect(p2.has('kview')).toBe(false);
    const p3 = writeKafkaTabState(p2, { topic: '', client: '' });
    expect(p3.has('ktopic')).toBe(false);
    expect(p3.has('kclient')).toBe(false);
  });
  it('bilinmeyen görünüm toplama düşer', () => {
    expect(parseKafkaTabState('kview=grid').view).toBe('');
    expect(parseKafkaTabState('').view).toBe('');
  });
});

describe('kısa kaynak satırı', () => {
  it('VM + adım', () => {
    expect(kafkaSourceLineTR('vm', 15)).toBe('Kaynak: VictoriaMetrics · kafka client metrikleri · 15 sn adım');
    expect(kafkaSourceLineTR('vm', 60)).toBe('Kaynak: VictoriaMetrics · kafka client metrikleri · 1 dk adım');
  });
  it('adım yoksa (CH / eski sunucu) adım yazılmaz — uydurulmaz', () => {
    expect(kafkaSourceLineTR('ch', undefined)).toBe('Kaynak: ClickHouse · kafka client metrikleri');
  });
  it.each([[15, '15 sn'], [60, '1 dk'], [90, '90 sn'], [300, '5 dk'], [3600, '1 sa'], [5400, '90 dk'], [0, null], [undefined, null]])(
    'fmtStepTR(%s) = %s', (s, want) => { expect(fmtStepTR(s)).toBe(want); });
});

describe('bağlantı paneli durumu', () => {
  const base: KafkaConnections = { series: [pod(0), pod(1)], total: 2, podLabel: 'k8s_pod_name', activePods: 1 };
  it('hazır: aktif pod + pod kırılımı', () => {
    const st = kafkaConnState(base);
    expect(st?.kind).toBe('ready');
    if (st?.kind === 'ready') {
      expect(st.active).toBe(1);
      expect(st.total).toBe(2);
      expect(st.perPod).toBe(true);
      expect(st.items).toHaveLength(2);
    }
  });
  it('seri yok → "metrik yok" (empty), hata → error, alan yok → null', () => {
    expect(kafkaConnState({ series: [], total: 0 })?.kind).toBe('empty');
    expect(kafkaConnState({ series: [], total: 0, error: 'boom' })).toEqual({ kind: 'error', message: 'boom' });
    expect(kafkaConnState(undefined)).toBeNull();
  });
  it('pod etiketi yok → aktif pod hesaplanmaz', () => {
    const st = kafkaConnState({ series: [pod(0)], total: 1 });
    expect(st?.kind === 'ready' && st.perPod).toBe(false);
    expect(st?.kind === 'ready' && st.active).toBe(null);
  });
});

// Sekme kancası signal iletir (cancellation.test.ts ilk eşleşmeyi tarar).
describe('useKafkaClientsTab sözleşmesi', () => {
  const src = readFileSync(new URL('../../lib/queries/messaging.ts', import.meta.url), 'utf8');
  const i = src.indexOf('export function useKafkaClientsTab');
  const body = src.slice(i, src.indexOf('\n}\n', i));
  it('signal + tüm girdiler anahtarda + 30 s yoklama', () => {
    expect(body).toContain('queryFn: ({ signal }) => api.messagingClients(');
    expect(body).toContain("signal, 'clients',");
    expect(body).toContain('p.topicFilter, p.clientFilter, p.view]');
    expect(body).toContain('refetchInterval: 30_000');
  });
});
