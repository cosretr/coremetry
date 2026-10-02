import { useCallback, useMemo, useState } from 'react';
import { LogsHistogram } from '@/components/LogsHistogram';
import { fmtNum, tsLong } from '@/lib/utils';
import type { AnomalyEvent, BehaviorChangeDetails } from '@/lib/types';
import { fmtDurationNs } from './problemTime';
import { ANOMALY_CHART_EMPTY, anomalyDurationNs, hourOfWeekLabel } from './anomalyDetail';

// anomalyDetailParts — anomali olayı detayının görsel parçaları (v0.10.1032).
//
// Operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception
// gibi detay gözükmüyor." Bu parçalar AnomalyDetailDrawer.tsx'in (v0.8.267)
// gövdesinden ÇIKARILDI; çekmece ve tam sayfa (AnomalyEventDetail) ikisi de
// buradan çizer, yani spike olguları, davranış kanıtı, ham örnek ve log hacmi
// iki ekranda birebir aynı okunur. Saf kararlar kardeş anomalyDetail.ts'te.

export function Fact({ k, v, title }: { k: string; v: React.ReactNode; title?: string }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{
        fontSize: 10, color: 'var(--text3)', fontWeight: 600,
        textTransform: 'uppercase', letterSpacing: '.05em',
      }}>{k}</div>
      <div className="mono" style={{
        fontSize: 12, color: 'var(--text)', marginTop: 2,
        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
      }} title={title}>{v}</div>
    </div>
  );
}

// SpikeFacts — "ne zaman spike oldu" cevabı (v0.8.267 operatör isteği):
// başladı / son görülme / süre / tepe ve cari oran / penceredeki sayım.
// v0.10.1032 (operatör: "Anlaşılır olsun") — etiketler düz Türkçe ("Peak
// ratio" → "En yüksek artış"); çekmece ve tam sayfanın "Teknik ayrıntı"sı
// aynı ızgarayı basar.
export function SpikeFacts({ event }: { event: AnomalyEvent }) {
  const durationNs = anomalyDurationNs(event);
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
      gap: 12, padding: 12, marginBottom: 12,
      background: 'var(--bg1)', border: '1px solid var(--border)', borderRadius: 8,
    }}>
      <Fact k="Başladı" v={tsLong(event.startedAt)} />
      <Fact k="Son görülme" v={tsLong(event.lastSeen)} />
      <Fact k="Süre" v={event.status === 'active'
        ? `${fmtDurationNs(durationNs)} · sürüyor`
        : fmtDurationNs(durationNs)} />
      <Fact k="En yüksek artış" v={`×${event.peakRatio.toFixed(1)}`}
        title="Tepe sayım / spike öncesi taban penceresi" />
      {event.currentRatio > 0 && (
        <Fact k="Şimdiki artış" v={`×${event.currentRatio.toFixed(1)}`} />
      )}
      {event.currentCount > 0 && (
        <Fact k="Penceredeki sayım" v={fmtNum(event.currentCount)} />
      )}
    </div>
  );
}

// v0.9.936 — davranış olayının yapılandırılmış kanıtı; ham JSON yerine
// okunur kutu (ayrıştırma anomalyDetail.parseBehaviorDetails'te, korumalı).
// v0.10.1032 — düz Türkçe etiketler; "Robust z" teknik terim olarak kalır
// (tam sayfada yalnız kapalı "Teknik ayrıntı" bölümünde görünür).
export function BehaviorDetailsBox({ d }: { d: BehaviorChangeDetails }) {
  const up = d.direction === 'up';
  const signalLabel = d.signal === 'regime'
    ? 'Kalıcı kayma — haftanın aynı saatine göre sürüyor'
    : 'Mevsimsel sapma — kendi haftalık saat tabanından uzak';
  const fmt = (v: number) => `${fmtNum(Math.round(v * 100) / 100)}${d.unit}`;
  return (
    <div style={{
      border: '1px solid var(--border)', borderRadius: 6,
      padding: '10px 12px', marginBottom: 12, background: 'var(--bg1)',
    }}>
      <div style={{ fontSize: 12, color: 'var(--text)', marginBottom: 8 }}>
        {signalLabel}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16, fontSize: 12 }}>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontWeight: 600 }}>NORMAL</div>
          <div className="mono">{fmt(d.baseline)}</div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontWeight: 600 }}>ŞİMDİ</div>
          <div className="mono" style={{ color: up ? 'var(--err)' : 'var(--warn)' }}>{fmt(d.current)}</div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontWeight: 600 }}>DEĞİŞİM</div>
          <div className="mono">{up ? '↑' : '↓'} {fmtNum(Math.round(d.ratio * 100) / 100)}×</div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontWeight: 600 }}>ROBUST z</div>
          <div className="mono">{fmtNum(Math.round(d.z * 10) / 10)}σ</div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontWeight: 600 }}>SÜRDÜ</div>
          <div className="mono">{d.dwell} × 5 dk</div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: 'var(--text3)', fontWeight: 600 }}>KIYAS SAATİ</div>
          <div className="mono">{hourOfWeekLabel(d.hourOfWeek)}</div>
        </div>
      </div>
      {d.deploy && (
        <div style={{ fontSize: 12, color: 'var(--text2)', marginTop: 8 }}>
          ⬇ Deploy <b className="mono">{d.deploy.version}</b> kaymadan{' '}
          <b>{Math.max(1, Math.round(d.deploy.ageSeconds / 60))} dk önce</b> indi.
        </div>
      )}
    </div>
  );
}

// AnomalySample — tespit anında yakalanan ham örnek satır. Davranış olayında
// (yapılandırılmış kanıt ayrıştırıldıysa) çağıran bunu çizmez.
export function AnomalySample({ sample }: { sample: string }) {
  return (
    <pre style={{
      fontSize: 11,
      whiteSpace: 'pre-wrap', overflowWrap: 'anywhere',
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 6, padding: '8px 10px', marginBottom: 12,
      color: 'var(--text2)', maxHeight: 120, overflowY: 'auto',
    }} title="Sample line captured at detection">{sample}</pre>
  );
}

// AnomalyLogVolume — servisin spike çevresindeki log hacmi. ES-maliyet
// sözleşmesi (operatör: "log anomalies elastic backend kullanıldığında çok
// fazla sorgu yapmasın"): yalnız MOUNT olduğunda (çekmece / sayfa açıkken),
// yalnız log türlerinde (çağıran kapılar), TEK sınırlı /api/logs/timeseries
// çağrısı — uç 30 s sunucu önbelleğinde. Aralık ve süzgeç memo'lu: her
// render'da yeni nesne fetch'i yeniden tetiklerdi (v0.5.184 sınıfı).
//
// v0.10.1032 (inceleme) — `bare`: tam sayfa bölüm başlığı ("Log hacmi") zaten
// söylediği için İngilizce alt yazı basılmaz; histogram boş ya da hatalıyken
// sessizce kaybolmak yerine sayfanın kısa notu çizilir (bileşen seçicisiz
// kullanımda boş/hata durumunda hiçbir şey çizmiyor — LogsHistogram onSeries
// tri-state'i: null = hata, [] / toplam 0 = boş). Çekmece eski biçimde.
export function AnomalyLogVolume({ service, win, bare = false }: {
  service: string;
  win: { fromNs: number; toNs: number };
  bare?: boolean;
}) {
  const range = useMemo(() => ({ from: win.fromNs, to: win.toNs }), [win.fromNs, win.toNs]);
  const filter = useMemo(() => ({
    service, search: '', severity: 0, traceId: '', spanId: '',
  }), [service]);
  const [state, setState] = useState<'pending' | 'ok' | 'empty' | 'error'>('pending');
  const onSeries = useCallback((s: { name: string; total: number }[] | null) => {
    if (s === null) setState('error');
    else setState(s.reduce((a, x) => a + x.total, 0) > 0 ? 'ok' : 'empty');
  }, []);
  return (
    <div style={{ marginBottom: 4 }}>
      {!bare && (
        <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 4 }}>
          {service} log volume around the spike
          (window {tsLong(win.fromNs)} → {tsLong(win.toNs)})
        </div>
      )}
      <LogsHistogram range={range} filter={filter} onSeries={bare ? onSeries : undefined} />
      {bare && state === 'empty' && <div className="field-hint">{ANOMALY_CHART_EMPTY}</div>}
      {bare && state === 'error' && (
        <div className="field-hint">Log hacmi okunamadı — bu bir hata, boş sonuç değil.</div>
      )}
    </div>
  );
}
