// kafkaTab.ts — v0.10.1097 (operatör: "9'u yap"). SAF: /messaging/topic
// "Kafka istemcileri" sekmesinin URL durumu, denetim görünürlüğü, pod
// görünümü öğeleri ve kısa kaynak satırı. Bileşen (KafkaClientsTab.tsx) yalnız
// bunları çağırır; davranış burada tablo testli (kafkaTab.test.ts).
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type {
  KafkaConnections, KafkaLabelScope, KafkaMetricBlock, KafkaTabLabels, MessagingClients, SpanMetricSeries,
} from '@/lib/types';
import { kafkaShortLabels } from './kafkaClients';

// ── URL durumu ──────────────────────────────────────────────────────────────
// Sayfanın kendi parametreleri (tab, system, cluster, destination, range)
// ile çakışmasın diye `k` önekli. Varsayılan (boş / toplam) param SİLİNİR:
// "kview yok" ile "kview=toplam" tek adres olur.
export const KTAB_PARAMS = { topic: 'ktopic', client: 'kclient', view: 'kview' } as const;

export type KafkaTabView = '' | 'pod';

export interface KafkaTabState {
  topic: string;
  client: string;
  view: KafkaTabView;
}

export function parseKafkaTabState(search: string | URLSearchParams): KafkaTabState {
  const p = typeof search === 'string' ? new URLSearchParams(search) : search;
  return {
    topic: (p.get(KTAB_PARAMS.topic) ?? '').trim(),
    client: (p.get(KTAB_PARAMS.client) ?? '').trim(),
    view: p.get(KTAB_PARAMS.view) === 'pod' ? 'pod' : '',
  };
}

/** prev'den TÜRETİR (yabancı param korunur); boş/varsayılan değer param'ı siler. */
export function writeKafkaTabState(prev: URLSearchParams, patch: Partial<KafkaTabState>): URLSearchParams {
  const p = new URLSearchParams(prev);
  const set = (k: string, v: string | undefined) => {
    if (v === undefined) return;
    const t = v.trim();
    if (t) p.set(k, t); else p.delete(k);
  };
  set(KTAB_PARAMS.topic, patch.topic);
  set(KTAB_PARAMS.client, patch.client);
  if (patch.view !== undefined) set(KTAB_PARAMS.view, patch.view === 'pod' ? 'pod' : '');
  return p;
}

// ── Denetim görünürlüğü ─────────────────────────────────────────────────────
export interface KafkaTabControls {
  topic: boolean;
  client: boolean;
  podToggle: boolean;
}

/**
 * Etiket yoksa denetim YOK (ölü denetim yasak) ve hakkında hiçbir şey
 * söylenmez. Keşif yapılamadıysa (detected=false, CH ya da hata) hepsi kapalı.
 */
export function kafkaTabControls(labels: KafkaTabLabels | null | undefined): KafkaTabControls {
  if (!labels?.detected) return { topic: false, client: false, podToggle: false };
  return { topic: !!labels.topic, client: !!labels.clientId, podToggle: !!labels.pod };
}

// ── Seçici kapsamı (v0.10.1102) ─────────────────────────────────────────────
/**
 * kafkaLabelScopeOf — seçici önerilerinin kapsamı: sayfanın anahtarları
 * (sunucu servis kümelerini panellerle aynı yoldan türetir) + sunucunun
 * UYGULADIĞI süzgeç (istenen değil — etiketsiz süzgeç uygulanmaz, öneriyi de
 * daraltmamalı).
 */
export function kafkaLabelScopeOf(
  page: { system: string; cluster: string; destination: string },
  data: Pick<MessagingClients, 'filter'> | null | undefined,
): KafkaLabelScope {
  return {
    system: page.system, cluster: page.cluster, destination: page.destination,
    topic: data?.filter?.topic || undefined,
    clientId: data?.filter?.clientId || undefined,
  };
}

/** Aramayı tazeleyen imza: yalnız etkili girdiler (aranan etiketin kendi süzgeci girmez). */
export function kafkaLabelScopeSig(label: 'topic' | 'client_id', sc: KafkaLabelScope): string {
  const other = label === 'client_id' ? (sc.topic ?? '') : (sc.clientId ?? '');
  return JSON.stringify([sc.system, sc.cluster, sc.destination, other]);
}

// ── Pod görünümü öğeleri ────────────────────────────────────────────────────
/** Pod görünümünde blok başına çizgi tavanı (sunucu kafkaPodSeriesCap ile aynı). */
export const KAFKA_POD_CAP = 12;

/**
 * kafkaPodItems — pod başına seriler → panel öğeleri: ilk KAFKA_POD_CAP seri
 * + "diğer N" (soluk, kesikli). Sunucu katlamışsa (folded>0) son seri
 * "diğer"dir; eski sunucu ya da katlanmamış bir liste gelirse burada SAVUNMA
 * katlaması yapılır (toplama: sum). Hiçbir yolda 13 çizgiden fazlası yok.
 */
export function kafkaPodItems(
  series: SpanMetricSeries[] | null | undefined,
  folded?: number,
): { items: CorePanelMultiItem[]; folded: number } {
  let rows = series ?? [];
  let other: SpanMetricSeries | null = null;
  let n = folded ?? 0;
  if (n > 0 && rows.length > 0) {
    other = rows[rows.length - 1];
    rows = rows.slice(0, -1);
  }
  if (rows.length > KAFKA_POD_CAP) {
    const rest = rows.slice(KAFKA_POD_CAP);
    rows = rows.slice(0, KAFKA_POD_CAP);
    const extra = sumSeries(rest);
    other = other ? sumSeries([other, extra]) : extra;
    n += rest.length;
  }
  const names = kafkaShortLabels(rows);
  const items: CorePanelMultiItem[] = rows.map((s, i) => ({ name: names[i], role: 'data', series: [s] }));
  if (other && n > 0) {
    items.push({ name: `diğer ${n}`, role: 'muted', dashed: true, series: [{ ...other, groupKey: [`diğer ${n}`] }] });
  }
  return { items, folded: n };
}

function sumSeries(list: SpanMetricSeries[]): SpanMetricSeries {
  const byT = new Map<number, number>();
  for (const s of list) for (const p of s.points ?? []) byT.set(p.time, (byT.get(p.time) ?? 0) + p.value);
  const points = [...byT.entries()].sort((a, b) => a[0] - b[0]).map(([time, value]) => ({ time, value }));
  return { groupKey: ['diğer'], points };
}

/** Blok için öğeler: pod görünümünde katlı liste, değilse çağıranın yolu. */
export function kafkaPodBlockItems(block: KafkaMetricBlock | null | undefined) {
  return kafkaPodItems(block?.series, block?.folded);
}

// ── Bağlantı paneli ─────────────────────────────────────────────────────────
export type KafkaConnState =
  | { kind: 'error'; message: string }
  | { kind: 'empty' }
  | { kind: 'ready'; items: CorePanelMultiItem[]; active: number | null; total: number; perPod: boolean };

/** null/undefined = eski sunucu (alan yok) → panel hiç çizilmez. */
export function kafkaConnState(c: KafkaConnections | null | undefined): KafkaConnState | null {
  if (!c) return null;
  if (c.error) return { kind: 'error', message: c.error };
  if (!c.total || !(c.series?.length)) return { kind: 'empty' };
  const { items } = kafkaPodItems(c.series, c.folded);
  return { kind: 'ready', items, active: c.activePods ?? null, total: c.total, perPod: !!c.podLabel };
}

/** Bağlantı paneli çizerken grafik ızgarasından düşen bloklar (aynı metrik iki kez çizilmesin). */
export const KAFKA_CONN_BLOCK_KEYS: readonly string[] = ['producer_connection_count', 'consumer_connection_count'];

// ── Kısa kaynak satırı ──────────────────────────────────────────────────────
const SOURCE_TR: Record<string, string> = { vm: 'VictoriaMetrics', ch: 'ClickHouse' };

export function fmtStepTR(sec: number | null | undefined): string | null {
  if (!sec || sec <= 0 || !Number.isFinite(sec)) return null;
  if (sec < 60) return `${Math.round(sec)} sn`;
  if (sec < 3600) return Number.isInteger(sec / 60) ? `${sec / 60} dk` : `${Math.round(sec)} sn`;
  return Number.isInteger(sec / 3600) ? `${sec / 3600} sa` : `${Math.round(sec / 60)} dk`;
}

/** "Kaynak: VictoriaMetrics · kafka client metrikleri · 15 sn adım" — tam not ipucunda. */
export function kafkaSourceLineTR(source: string | null | undefined, stepSeconds?: number | null): string {
  const parts = [`Kaynak: ${SOURCE_TR[source ?? ''] ?? (source || 'metrik')}`, 'kafka client metrikleri'];
  const step = fmtStepTR(stepSeconds);
  if (step) parts.push(`${step} adım`);
  return parts.join(' · ');
}
