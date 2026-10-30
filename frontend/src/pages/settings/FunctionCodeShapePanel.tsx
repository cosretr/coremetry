// FunctionCodeShapePanel — v0.10.1226: function_code değerlerinin kaçı iş
// kodu biçimine (business_code_summary_5m kapısı) uyuyor, kaçı dışarıda
// kalıyor. Okuma pahalı olabilir: yalnız düğmeyle başlar (ES/CH maliyet
// disiplini), sunucu 5 dk önbellekler.
import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
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
    <div className="mt-[18px] rounded-lg border border-(--border) bg-(--bg2) p-4">
      <h3 className="mb-1.5 text-[length:var(--fs-md)] font-semibold">Fonksiyon kodu biçimi (son 1 saat)</h3>
      <p className="mb-2.5 text-[length:var(--fs-sm)] text-(--text2)">
        İş kodu özeti yalnız biçime uyan değerleri saklar (<code>^[A-Z][A-Z0-9]{'{4,9}'}$</code>).
        Bu ölçüm dışarıda kalan değerlerin sayısını ve örneklerini gösterir; gerçek kodlar
        dışarıda kalıyorsa biçim genişletilmeli.
      </p>
      <Button variant="secondary" size="sm" onClick={measure} loading={busy}>{data ? 'Yeniden ölç' : 'Ölç'}</Button>
      {busy && !data && <Spinner />}
      {err && <span className="ml-2 text-[length:var(--fs-sm)] text-(--err)">{err}</span>}
      {data && data.source === '' && <Badge variant="secondary">okuma yolu yok (geniş rollup ve attr_function_code kolonu bulunamadı)</Badge>}
      {s && data && data.source !== '' && (
        <>
          <div className="my-2 flex flex-wrap items-center gap-2">
            <Badge variant="secondary">biçime uyan: {fmtNum(s.okDistinct)} değer · {fmtNum(s.okSpans)} span</Badge>
            {s.offDistinct > 0
              ? <StatusBadge status="warn">biçim dışı: {fmtNum(s.offDistinct)} değer · {fmtNum(s.offSpans)} span</StatusBadge>
              : <Badge variant="secondary">biçim dışı: {fmtNum(s.offDistinct)} değer · {fmtNum(s.offSpans)} span</Badge>}
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
