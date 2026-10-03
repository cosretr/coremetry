// OracleGroupPanel — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün"). Oracle hata tablosu grubunun detayında Stack trace kartının
// yerini alır (Oracle satırı stack taşımaz): kaynak, hata kodu, operasyon,
// kanal kırılımı, etkilenen servisler (trace → servis çözümü) ve pivotlar —
// saklanan trace id'leri (örnek satırlardan; mevcut Oracle trace linki,
// Trace › Logs sekmesi Oracle satırlarını gösterir).
//
// Veri: GET /api/exception-groups/{fp}/oracle (fetch-on-open, yalnız Oracle
// grubunda). Öznitelik paneli = KeyValue atomu (tablo standardı: kayıt değil).
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import { KeyValue, KeyValueRow } from '@/components/ui/KeyValue';
import { QueryErrorInline } from '@/components/QueryError';
import { serviceHref } from '@/lib/serviceHref';
import { traceHref } from '@/lib/traceHref';
import type { ExceptionGroup, ExceptionSample } from '@/lib/types';
import { oracleChannelsText, isSyntheticOracleService, oracleSourceName } from './oracleGroup';

export function OracleGroupPanel({ group, samples }: { group: ExceptionGroup; samples: ExceptionSample[] }) {
  const q = useQuery({
    queryKey: ['exc-oracle-detail', group.fingerprint],
    queryFn: () => api.exceptionGroupOracle(group.fingerprint),
    staleTime: 30_000,
  });
  const info = q.data ?? group.oracle;
  const win = { fromNs: group.firstSeen, toNs: group.lastSeen };
  const traces = samples.filter(s => s.traceId).slice(0, 3);
  const services = info?.services ?? [];
  const channels = oracleChannelsText(info, 6);
  return (
    <div className="card" style={{ minWidth: 0 }}>
      <div className="ov-card-h">
        <h3>Oracle hata grubu</h3>
        <span className="ov-sub">stack yok — Oracle hata tablosu satırları (kod × operasyon)</span>
      </div>
      <div className="ov-card-b">
        {q.isError && !info ? (
          <QueryErrorInline text={`Oracle kırılımı okunamadı${q.error instanceof Error ? ` — ${q.error.message}` : ''}`}
            onRetry={() => q.refetch()} />
        ) : (
          <KeyValue>
            <KeyValueRow k="Kaynak" v={info?.known === false ? `${oracleSourceName(group)} (kaynak bulunamadı)` : oracleSourceName(group)} />
            <KeyValueRow k="Hata kodu" v={group.type} mono />
            <KeyValueRow k="Operasyon" v={group.message} mono />
            <KeyValueRow k="Son 1 saat" v={info?.known ? `${fmtNum(info.lastHour ?? 0)} (önceki saat ${fmtNum(info.prevHour ?? 0)})` : ''}
              title="Öncelik girdisi: P1 yalnız son 1 sa ≥ eşik VE ≥3× önceki saat (patlama) ya da yeni grup + son 1 sa ≥ eşik" />
            <KeyValueRow k="Kanal" v={channels || (q.isLoading ? '…' : '')}
              title="Kanal kırılımı: kapanmış dakikaların ağırlık (Adet) payı" />
            <KeyValueRow k="Etkilenen servisler" v={services.length === 0 ? (q.isLoading ? '…' : '') : (
              <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 8 }}>
                {services.map(s => isSyntheticOracleService(s.name) ? (
                  <span key={s.name} className="mono">{s.name}</span>
                ) : (
                  <Link key={s.name} to={serviceHref(s.name, { range: win })} className="mono"
                    title={`${fmtNum(s.count)} trace/pod oyu`}>{s.name}</Link>
                ))}
              </span>
            )} title="Trace → servis çözümü (hata veren en derin span'ın servisi), yoksa pod adı" />
            <KeyValueRow k="Trace'ler" v={traces.length === 0 ? '' : (
              <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 8 }}>
                {traces.map(s => (
                  <Link key={s.traceId} to={traceHref(s.traceId)} className="mono"
                    title="Trace'i aç — Logs sekmesinde bu Oracle satırları görünür">{s.traceId.slice(0, 16)}…</Link>
                ))}
              </span>
            )} />
          </KeyValue>
        )}
        <div className="cell-faint" style={{ fontSize: 11, marginTop: 8 }}>
          Yalnız kapanmış dakikalar sayılır (özel SQL penceresinden çıkan dakika bir kez) — son görülme duvar
          saatinin birkaç dakika gerisindedir. Oracle P1'i yapışkan değil: son 1 saatin patlaması ya da yeni grup
          (eşik: Settings › Anomaly › Oracle grubu P1 eşiği).
        </div>
      </div>
    </div>
  );
}
