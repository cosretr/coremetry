// OracleGroupPanel — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün"). Oracle hata tablosu grubunun detayında Stack trace kartının
// yerini alır (Oracle satırı stack taşımaz): kaynak, hata kodu, operasyon,
// kanal kırılımı, etkilenen servisler (trace → servis çözümü) ve pivotlar —
// saklanan trace id'leri (örnek satırlardan; mevcut Oracle trace linki,
// Trace › Logs sekmesi Oracle satırlarını gösterir).
//
// Veri: GET /api/exception-groups/{fp}/oracle (fetch-on-open, yalnız Oracle
// grubunda). Öznitelik paneli = KeyValue atomu (tablo standardı: kayıt değil).
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { QueryErrorInline } from '@/components/QueryError';
import { serviceHref } from '@/lib/serviceHref';
import type { ExceptionGroup, ExceptionSample } from '@/lib/types';
import { oracleChannelsText, isSyntheticOracleService, oracleSourceName } from './oracleGroup';
import { sampleTraceLinkable } from './sampleTrace'; // v0.10.1104
import { useBranding } from '@/lib/branding'; // v0.10.1108
import { SampleTraceId } from './ExceptionSampleRow'; // v0.10.1369
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2287 — metinler arayüz dilinde (TR aynı)

export function OracleGroupPanel({ group, samples }: { group: ExceptionGroup; samples: ExceptionSample[] }) {
  const q = useQuery({
    queryKey: ['exc-oracle-detail', group.fingerprint],
    queryFn: () => api.exceptionGroupOracle(group.fingerprint),
    staleTime: 30_000,
  });
  const info = q.data ?? group.oracle;
  // v0.10.1108 — başlık + hata metni görünen adla ("Teknik hata grubu"); alt
  // satır ve dipnot kaynağı açıklar (Oracle hata tablosu / Settings yolu).
  const label = useBranding().oracleGroupLabel;
  const lang = useLang();
  const win = { fromNs: group.firstSeen, toNs: group.lastSeen };
  // v0.10.1104 — ayrık id (aynı trace birden çok Oracle satırı taşır; React
  // anahtarı çakışıyordu) ve Coremetry'de BULUNANLAR önce (kararlı sıralama):
  // ilk üç çip mümkünse açılabilen trace'ler.
  const traces = samples
    .filter((s, i, all) => s.traceId && all.findIndex(o => o.traceId === s.traceId) === i)
    .sort((a, b) => Number(!sampleTraceLinkable(a)) - Number(!sampleTraceLinkable(b)))
    .slice(0, 3);
  const services = info?.services ?? [];
  const channels = oracleChannelsText(info, 6, lang);
  return (
    <div className="card" style={{ minWidth: 0 }}>
      <div className="ov-card-h">
        <h3>{tf('ora.title', lang, { label })}</h3>
        <span className="ov-sub">{t('ora.sub', lang)}</span>
      </div>
      <div className="ov-card-b">
        {q.isError && !info ? (
          <QueryErrorInline text={`${tf('ora.err', lang, { label })}${q.error instanceof Error ? ` — ${q.error.message}` : ''}`}
            onRetry={() => q.refetch()} />
        ) : (
          <KeyValue>
            <KeyValueRow k={t('ora.source', lang)} v={info?.known === false ? tf('ora.source.unknown', lang, { name: oracleSourceName(group) }) : oracleSourceName(group)} />
            <KeyValueRow k={t('ora.code', lang)} v={group.type} mono />
            <KeyValueRow k={t('ora.op', lang)} v={group.message} mono />
            <KeyValueRow k={t('ora.lastHour', lang)} v={info?.known ? tf('ora.lastHour.v', lang, { a: fmtNum(info.lastHour ?? 0), b: fmtNum(info.prevHour ?? 0) }) : ''}
              title={t('ora.lastHour.title', lang)} />
            <KeyValueRow k={t('ora.channel', lang)} v={channels || (q.isLoading ? '…' : '')}
              title={t('ora.channel.title', lang)} />
            <KeyValueRow k={t('ora.services', lang)} v={services.length === 0 ? (q.isLoading ? '…' : '') : (
              <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 8 }}>
                {services.map(s => isSyntheticOracleService(s.name) ? (
                  <span key={s.name} className="mono">{s.name}</span>
                ) : (
                  <Link key={s.name} to={serviceHref(s.name, { range: win })} className="mono"
                    title={tf('ora.services.votes', lang, { n: fmtNum(s.count) })}>{s.name}</Link>
                ))}
              </span>
            )} title={t('ora.services.title', lang)} />
            <KeyValueRow k={t('ora.traces', lang)} v={traces.length === 0 ? '' : (
              <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: 8 }}>
                {/* v0.10.1104 — Coremetry'de olmayan trace (traceInCoremetry=false)
                    linksiz + "Coremetry'de yok"; boş /trace sayfasına götürmez.
                    v0.10.1369 — tam id + kopyala; Tempo'da bulunan link + "Tempo". */}
                {traces.map(s => (
                  <SampleTraceId key={s.traceId} s={s}
                    linkTitle={t('ora.traces.link', lang)} />
                ))}
              </span>
            )} />
          </KeyValue>
        )}
        <div className="cell-faint" style={{ fontSize: 11, marginTop: 8 }}>
          {t('ora.foot', lang)}
        </div>
      </div>
    </div>
  );
}
