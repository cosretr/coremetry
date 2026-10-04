// KafkaClientsTab.tsx — v0.10.1097 (operatör: "9'u yap" — dördü birden).
// /messaging/topic "Kafka istemcileri" sekmesinin gövdesi (set=clients):
//
//   1. topic / client_id süzgeci — YALNIZ serilerde o etiket varsa (keşif
//      sunucuda, tek /api/v1/labels; cevap `labels`). Etiket yoksa denetim
//      HİÇ çizilmez ve hakkında bir şey söylenmez (ölü denetim yok). Seçici
//      sunucu araması (KafkaLabelPicker), değer URL'de (?ktopic= / ?kclient=).
//   2. "Toplam / Pod bazlı" — pod etiketi varsa; pod görünümünde blok başına
//      ilk 12 pod + "diğer N" (sunucu katlar). URL'de ?kview=pod.
//   3. kısa kaynak satırı — "Kaynak: VictoriaMetrics · kafka client
//      metrikleri · 15 sn adım"; uzun not ipucunda (title).
//   4. "Bağlantılar" — pod başına açık bağlantı + "aktif pod" (son adımda ≥1).
//
// URL = tek doğruluk kaynağı: yazım prev'den türer (sayfanın tab/range/kimlik
// parametreleri korunur), replace:true. Okuma `search` dizesinden memo'lu —
// ilgisiz bir URL yazımı (range) süzgeci silmez (v0.8.253 sınıfı).
// Sorgu yalnız sekme mount'luyken; 30 s yoklama gizli sekmede durur.
import { lazy, Suspense, useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { KafkaAlertModal } from '@/pages/alerts/KafkaAlertModal';
import { Spinner } from '@/components/Spinner';
import { LazyMount } from '@/components/LazyMount';
import type { KafkaConnections, TimeRange } from '@/lib/types';
import { timeRangeToNs } from '@/lib/utils';
import { useKafkaClientsTab } from '@/lib/queries/messaging';
import { kafkaBlockItems, kafkaDegradeTR, kafkaLastRows, kafkaPanelUnit, kafkaScopeNoteTR } from './kafkaClients';
import {
  KAFKA_CONN_BLOCK_KEYS, KAFKA_POD_CAP, kafkaConnState, kafkaPodBlockItems, kafkaSourceLineTR,
  kafkaTabControls, parseKafkaTabState, writeKafkaTabState, type KafkaTabState,
} from './kafkaTab';
import { KafkaLabelPicker } from './KafkaLabelPicker';
import { KafkaLastTable } from './KafkaLastTable';

const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

type ViewKey = 'toplam' | 'pod';

export function KafkaClientsTab({ system, cluster, destination, range, xRange, syncKey, title }: {
  system: string; cluster: string; destination: string; range: TimeRange;
  xRange: { from: number; to: number }; syncKey?: string; title?: string;
}) {
  const [params, setParams] = useSearchParams();
  const search = params.toString();
  const st = useMemo(() => parseKafkaTabState(search), [search]);
  const patch = useCallback((p: Partial<KafkaTabState>) =>
    setParams(prev => writeKafkaTabState(prev, p), { replace: true }), [setParams]);
  // timeRangeToNs MEMO içinde (v0.5.184 sonsuz refetch sınıfı).
  const win = useMemo(() => timeRangeToNs(range), [range]);
  const q = useKafkaClientsTab({
    system, cluster, destination, fromNs: win.from, toNs: win.to,
    topicFilter: st.topic, clientFilter: st.client, view: st.view,
  });
  const [alertOpen, setAlertOpen] = useState(false);

  if (q.isPending) {
    return <div className="kc-line" role="status" aria-busy="true"><Spinner /> Kafka istemci metrikleri…</div>;
  }
  const data = q.isError ? null : (q.data ?? null);
  const filtered = !!(data?.filter?.topic || data?.filter?.clientId);
  const degrade = kafkaDegradeTR(data);
  // Süzgeç UYGULANMIŞKEN seri yoksa bölüm tek satıra DÜŞMEZ: denetimler
  // kalmalı ki operatör süzgeci kaldırabilsin.
  if (!data || (degrade && !filtered)) {
    return <div className="kc-line" title={data?.note}>◌ {degrade}</div>;
  }
  const controls = kafkaTabControls(data.labels);
  const podView = data.view === 'pod';
  // Bağlantı paneli bu iki bloğu kendi (pod) kırılımıyla çiziyor; ızgarada
  // ikinci kez çizilmesin. Son-değer tablosunda kalırlar.
  const blockKeys = Object.keys(data.blocks ?? {});
  const chartKeys = data.connections ? blockKeys.filter(k => !KAFKA_CONN_BLOCK_KEYS.includes(k)) : blockKeys;
  const anySeries = blockKeys.some(k => (data.blocks[k].series ?? []).length > 0);
  // Topic seçicisi varken "süzülemez" beyanı YANLIŞ olur — seçici ekranda.
  const scopeNote = controls.topic ? null : kafkaScopeNoteTR(data.scope);
  const view: ViewKey = st.view === 'pod' ? 'pod' : 'toplam';

  return (
    <section className="kc-sec" aria-label="Kafka istemcileri (metrik)">
      <div className="kc-head">
        <span aria-hidden className="kc-dot" />
        {title ?? 'Kafka istemcileri'} · METRİK ({data.source})
        {data.consumers.length > 0 && (
          <Button variant="secondary" size="sm" className="kc-head-act" onClick={() => setAlertOpen(true)}
            title="Bu topic için tüketici lag alarmı (istemcinin gördüğü lag; consumer group lag'i değil)">
            Lag alarmı
          </Button>
        )}
      </div>
      {alertOpen && (
        <KafkaAlertModal open onClose={() => setAlertOpen(false)} services={data.consumers}
          target={{ topic: destination }} defaultMetric="kafka_lag_max" />
      )}
      {(controls.topic || controls.client || controls.podToggle) && (
        <div className="kc-filters" role="group" aria-label="Kafka istemci süzgeçleri">
          {controls.topic && (
            <div className="kc-filter">
              <span className="kc-filter-lbl">topic</span>
              <KafkaLabelPicker label="topic" value={st.topic} onCommit={v => patch({ topic: v })}
                fromNs={win.from} toNs={win.to} placeholder="(tümü)" />
            </div>
          )}
          {controls.client && (
            <div className="kc-filter">
              <span className="kc-filter-lbl">client_id</span>
              <KafkaLabelPicker label="client_id" value={st.client} onCommit={v => patch({ client: v })}
                fromNs={win.from} toNs={win.to} placeholder="(tümü)" />
            </div>
          )}
          {controls.podToggle && (
            <SegmentedControl<ViewKey> size="sm" aria-label="Seri görünümü" value={view}
              onChange={v => patch({ view: v === 'pod' ? 'pod' : '' })}
              options={[
                { value: 'toplam', label: 'Toplam', title: "Pod'lar toplanır (servis · istemci kırılımı)" },
                { value: 'pod', label: 'Pod bazlı', title: `Pod başına seri (${data.labels?.pod}); ilk ${KAFKA_POD_CAP} + "diğer N"` },
              ]} />
          )}
        </div>
      )}
      {/* KISA KAYNAK SATIRI — uzun not (kaynak, lag tanımı, kapsam uyarısı,
          süzgeç cümleleri) ipucunda; sayfa paragrafla açılmıyor. */}
      <div className="kc-src" title={data.note}>{kafkaSourceLineTR(data.source, data.stepSeconds)}</div>
      {scopeNote && <div className="kc-scope">{scopeNote}</div>}
      <KafkaConnectionsPanel conn={data.connections} xRange={xRange} syncKey={syncKey} />
      {!anySeries ? (
        <div className="kc-empty">
          {filtered ? 'Bu süzgeçle istemci metriği serisi yok.' : 'Bu pencerede istemci metriği serisi yok.'}
        </div>
      ) : (
        <>
          <div className="kc-grid">
            {chartKeys.map(k => {
              const b = data.blocks[k];
              const pod = podView ? kafkaPodBlockItems(b) : null;
              const tot = podView ? null : kafkaBlockItems(b);
              const items = pod?.items ?? tot?.items ?? [];
              const folded = pod?.folded ?? 0;
              const truncated = tot?.truncated ?? 0;
              return (
                <LazyMount key={k} minHeight={170}>
                  <Suspense fallback={<div className="kc-fallback"><Spinner /></div>}>
                    <CorePanelMultiLazy
                      title={b.label || k}
                      storageKey={podView ? `msg-topic-kafka-${k}-pod` : `msg-topic-kafka-${k}`}
                      height={150} unit={kafkaPanelUnit(b.unit)} xRange={xRange} syncKey={syncKey}
                      note={b.error ? `sorgu hatası: ${b.error}`
                        : folded ? `${KAFKA_POD_CAP} pod + diğer ${folded}`
                        : truncated ? `+${truncated} seri gösterilmiyor`
                        : b.groupBy.join(' · ') || undefined}
                      items={items} />
                  </Suspense>
                </LazyMount>
              );
            })}
          </div>
          <div className="kc-grid">
            <KafkaLastTable storageKey={podView ? 'msg-topic-clients-last-pod' : 'msg-topic-clients-last'} title="Son değer"
              keyLabel={podView ? 'Servis · pod' : 'Servis · istemci'}
              rows={kafkaLastRows(data.blocks, blockKeys)}
              cols={blockKeys.map(k => ({ id: k, label: data.blocks[k].label || k }))} />
          </div>
        </>
      )}
    </section>
  );
}

// ── "Bağlantılar" — kompakt panel ────────────────────────────────────────────
function KafkaConnectionsPanel({ conn, xRange, syncKey }: {
  conn: KafkaConnections | undefined; xRange: { from: number; to: number }; syncKey?: string;
}) {
  const st = kafkaConnState(conn);
  if (!st) return null; // eski sunucu: alan yok → panel yok
  return (
    <div className="kc-conn" role="region" aria-label="Bağlantılar">
      <div className="kc-conn-head">
        <span className="kc-subhead">Bağlantılar</span>
        {st.kind === 'ready' && st.perPod && (
          <span className="kc-conn-stat" title="Son adımda ≥ 1 açık bağlantısı olan pod / penceredeki pod sayısı">
            aktif pod <b>{st.active ?? '—'}</b> / {st.total}
          </span>
        )}
      </div>
      {st.kind === 'error' ? (
        <div className="kc-empty">sorgu hatası: {st.message}</div>
      ) : st.kind === 'empty' ? (
        <div className="kc-empty" title="kafka.producer.connection_count / kafka.consumer.connection_count bu pencerede seri döndürmedi">metrik yok</div>
      ) : (
        <LazyMount minHeight={150}>
          <Suspense fallback={<div className="kc-fallback"><Spinner /></div>}>
            <CorePanelMultiLazy
              title={st.perPod ? 'Açık bağlantı — pod (üretici + tüketici)' : 'Açık bağlantı — servis · istemci'}
              storageKey="msg-topic-kafka-conn"
              height={130} xRange={xRange} syncKey={syncKey}
              items={st.items} />
          </Suspense>
        </LazyMount>
      )}
    </div>
  );
}
