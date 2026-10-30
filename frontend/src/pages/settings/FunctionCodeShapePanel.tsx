// FunctionCodeShapePanel — v0.10.1226: function_code değerlerinin kaçı iş
// kodu biçimine (business_code_summary_5m kapısı) uyuyor, kaçı dışarıda
// kalıyor. Okuma pahalı olabilir: yalnız düğmeyle başlar (ES/CH maliyet
// disiplini), sunucu 5 dk önbellekler.
import { useState } from 'react';
import { Button, Badge } from '@/components/ui';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import { summarizeShape } from './functionCodeShape';
import type { FunctionCodeShapeReport } from '@/lib/types';

export function FunctionCodeShapePanel() {
  const [busy, setBusy] = useState(false);
  const [data, setData] = useState<FunctionCodeShapeReport | null>(null);
  const [err, setErr] = useState('');
  const measure = () => {
    setBusy(true); setErr('');
    api.functionCodeShape().then(setData, e => setErr((e as Error).message)).finally(() => setBusy(false));
  };
  const s = data ? summarizeShape(data.rows) : null;
  return (
    <div style={{ marginTop: 18, padding: 16, borderRadius: 8, background: 'var(--bg2)', border: '1px solid var(--border)' }}>
      <h3 style={{ fontSize: 13, fontWeight: 600, marginBottom: 6 }}>Fonksiyon kodu biçimi (son 1 saat)</h3>
      <p style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 10 }}>
        İş kodu özeti yalnız biçime uyan değerleri saklar (<code>^[A-Z][A-Z0-9]{'{4,9}'}$</code>).
        Bu ölçüm dışarıda kalan değerlerin sayısını ve örneklerini gösterir; gerçek kodlar
        dışarıda kalıyorsa biçim genişletilmeli.
      </p>
      <Button variant="secondary" size="sm" onClick={measure} loading={busy}>{data ? 'Yeniden ölç' : 'Ölç'}</Button>
      {busy && !data && <Spinner />}
      {err && <span style={{ fontSize: 12, color: 'var(--err)', marginLeft: 8 }}>{err}</span>}
      {data && data.source === '' && <Badge>okuma yolu yok (geniş rollup ve attr_function_code kolonu bulunamadı)</Badge>}
      {s && data && data.source !== '' && (
        <>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', margin: '8px 0' }}>
            <Badge tone="neutral">biçime uyan: {fmtNum(s.okDistinct)} değer · {fmtNum(s.okSpans)} span</Badge>
            <Badge tone={s.offDistinct > 0 ? 'warning' : 'neutral'}>biçim dışı: {fmtNum(s.offDistinct)} değer · {fmtNum(s.offSpans)} span</Badge>
            <span className="field-hint">kaynak {data.source} · farklı değer sayısı yaklaşık (uniq)</span>
          </div>
          {s.topOff.length > 0 && (
            <KeyValue>
              {s.topOff.map(r => (
                <KeyValueRow key={r.len} k={`uzunluk ${r.len}`} mono title={r.example}
                  v={<>{fmtNum(r.distinct)} değer · {fmtNum(r.spans)} span · örnek {r.example.length > 60 ? r.example.slice(0, 60) + '…' : r.example}</>} />
              ))}
            </KeyValue>
          )}
        </>
      )}
    </div>
  );
}
