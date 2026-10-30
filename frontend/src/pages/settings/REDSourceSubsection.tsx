import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import type { AnomalyREDSourceStats } from '@/lib/types';
import { Field, SelectField } from '@/components/coremetry/Field';
import { humanize } from './shared';

// REDSourceSubsection — v0.10.1478 (operatör: "Servislerin kendi içerisinde
// haftalık olağan akışlarını takip etse ve gerçek bir anomaly öyle yakalasa").
// Metrik dedektörü + davranış motorunun RED serisi kaynağı
// (anomaly_sensitivity.redSource / redSourceEnv; Dedektör hassasiyeti bölümü,
// aynı Kaydet). Altında dedektörün son tikinin ölçümü (yalnız okunur; açılışta
// bir kez — ayar sayfası, polling yok).
//
// v0.10.1489 — alarm kurallarının kaynağı da burada (anomaly_sensitivity
// .alertSource; aynı VM kapsamı / ortam). Altında evaluator'ın son tiki.
const NOTE = 'mt-1 ml-6 text-[length:var(--fs-xs)] leading-normal text-(--text3)';
const STATUS = 'mt-2 ml-6 text-[length:var(--fs-xs)] leading-[1.6] text-(--text2)';

export function REDSourceSubsection({ source, env, alertSource, onChange, onAlertSourceChange }: {
  source?: 'auto' | 'span';
  env?: string;
  alertSource?: 'auto' | 'span';
  onChange: (source: 'auto' | 'span', env: string) => void;
  onAlertSourceChange?: (alertSource: 'auto' | 'span') => void;
}) {
  const mode = source === 'span' ? 'span' : 'auto';
  const alertMode = alertSource === 'span' ? 'span' : 'auto';
  const [st, setSt] = useState<AnomalyREDSourceStats | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    api.getAnomalyREDSourceStatus().then(setSt).catch(e => setErr(humanize(e)));
  }, []);
  // Faz 2 dilim 1 R17 — coremetry SelectField / Field (aria-label'lar aynı);
  // satır-içi stiller utility.
  return (
    <div className="mt-1 border-t border-(--border) pt-4">
      <SelectField label="RED serisi kaynağı" aria-label="RED serisi kaynağı" className="max-w-[420px]" value={mode}
        onChange={e => onChange(e.target.value === 'span' ? 'span' : 'auto', env ?? '')}>
        <option value="auto">Otomatik — VictoriaMetrics varsa örneklenmemiş metrik</option>
        <option value="span">Span — örneklenmiş trace (eski davranış)</option>
      </SelectField>
      <div className={NOTE}>
        Otomatik (varsayılan): servisin OTel HTTP/gRPC sunucu histogramı VictoriaMetrics&apos;te varsa hata
        oranı, istek hızı ve p99 oradan (örneklenmemiş) okunur; yoksa servis başına span&apos;e düşülür.
        Taban (14 gün aynı saat, 28 gün haftanın saati), sürdürme ve eşikler aynıdır. Problem açıklaması
        kaynağı yazar: &quot;kaynak: VictoriaMetrics (örneklenmemiş)&quot; ya da &quot;kaynak: span (örneklenmiş)&quot;.
      </div>
      <Field label="Ortam" aria-label="RED kaynağı ortamı" className="mt-2.5 ml-6 max-w-[396px]"
        placeholder="boş = tüm ortamlar" maxLength={200}
        value={env ?? ''} disabled={mode === 'span' && alertMode === 'span'}
        onChange={e => onChange(mode, e.target.value)}
        hint={<>
          Doluysa VM serisi bu ortama süzülür (deployment_environment_name; toleranslı eşleme). Eşleşmezse
          VM kullanılmaz, span&apos;e düşülür.
        </>} />
      <div className={STATUS}>
        {err && <span>Durum okunamadı: {err}</span>}
        {!err && st && !st.lastUnix && <span>Bu pod dedektörü koşturmuyor (lider başka pod) — ölçüm yok.</span>}
        {!err && st && !!st.lastUnix && (
          <>
            <div>
              Son tik: {st.configured ? 'VictoriaMetrics bağlı' : (st.reason || 'VM yok')} · {st.lastQueries} sorgu
              {st.lastQueryErrors ? ` (${st.lastQueryErrors} hata)` : ''} · {st.lastMs} ms · ısınma: {st.pendingBuckets === 0 ? 'tamam' : `${st.pendingBuckets} kova bekliyor`}
            </div>
            {st.families.filter(f => f.present || f.usable).map(f => (
              <div key={f.id}>
                {f.id}: {f.usable ? `hazır (${f.readyDetector || '—'})` : f.reason}
              </div>
            ))}
            {st.sources && Object.entries(st.sources).map(([m, c]) => (
              <div key={m}>{m}: VM {c.vm ?? 0} servis · span {c.span ?? 0} servis</div>
            ))}
            {st.lastError && <div>Son hata: {st.lastError}</div>}
          </>
        )}
      </div>
      {onAlertSourceChange && (
        <>
          <SelectField label="Alarm kuralı kaynağı" aria-label="Alarm kuralı kaynağı" className="mt-3.5 max-w-[420px]" value={alertMode}
            onChange={e => onAlertSourceChange(e.target.value === 'span' ? 'span' : 'auto')}>
            <option value="auto">Otomatik — VictoriaMetrics varsa örneklenmemiş metrik</option>
            <option value="span">Span — örneklenmiş trace (eski davranış)</option>
          </SelectField>
          <div className={NOTE}>
            Otomatik (varsayılan): alarm kuralları (error_rate, error_count, request_rate, avg_ms, p50/p95/p99_ms)
            servisin HTTP/gRPC sunucu histogramından, kuralın kendi penceresiyle ölçülür; yoksa kural×servis başına
            span&apos;e düşülür. Eşik, sürdürme ve bekleme aynıdır; VM verisi ~2 dk geriden gelir ve minSamples gerçek
            istek sayısını sayar. Kural önizlemesi ve eşik önerisi aynı kaynağı kullanır. Ortam yukarıdaki alandan.
          </div>
          {!err && st?.alert && !!st.alert.lastUnix && (
            <div className={STATUS}>
              <div>
                Son evaluator tiki: {st.alert.configured ? 'VictoriaMetrics bağlı' : (st.alert.reason || 'VM yok')} · {st.alert.lastQueries} sorgu
                {st.alert.lastQueryErrors ? ` (${st.alert.lastQueryErrors} hata)` : ''}
                {st.alert.lastSkipped ? ` · ${st.alert.lastSkipped} bütçe dışı` : ''} · {st.alert.lastMs} ms
              </div>
              {st.alert.sources && Object.entries(st.alert.sources).map(([m, c]) => (
                <div key={m}>{m}: VM {c.vm ?? 0} · span {c.span ?? 0} kural×servis</div>
              ))}
              {st.alert.lastError && <div>Son hata: {st.alert.lastError}</div>}
            </div>
          )}
        </>
      )}
    </div>
  );
}
