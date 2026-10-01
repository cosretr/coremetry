import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useOracleFunctionCodes } from '@/lib/queries';
import type { SpanRow, TimeRange } from '@/lib/types';
import { functionCodeTracesHref } from '@/lib/pivotHref';
import { traceFunctionCodes, traceOracleOperations, oracleOperationTitle } from '@/lib/oracleFunctionOps';

// OracleOperationChip — v0.10.1003 (kuyruk: "operasyon adını trace sayfasında
// göster"). Trace özet şeridinde, span'lerin FUNCTION_CODE değerinden çözülen
// Oracle operasyon adı. Tıklayınca aynı fonksiyon kodunun diğer trace'leri.
//
// İstek disiplini: sözlük (kod → ad) TEK istek, 5 dk taze (sunucu TTL'iyle
// aynı) ve YALNIZ trace bir fonksiyon kodu taşıyorsa çekilir — trace başına
// istek yok, kod taşımayan trace hiç istek atmaz. Sözlükte karşılığı olmayan
// kod için hiçbir şey çizilmez (ad uydurulmaz, boş çip yok).
export function OracleOperationChip({ spans, pageRange }: { spans: SpanRow[]; pageRange: TimeRange }) {
  const codes = useMemo(() => traceFunctionCodes(spans), [spans]);
  const q = useOracleFunctionCodes(codes.length > 0);
  const ops = useMemo(() => traceOracleOperations(codes, q.data), [codes, q.data]);
  if (ops.length === 0) return null;
  return (
    <>
      {ops.map(o => (
        <span key={o.code} className="cell-hint" title={oracleOperationTitle(o)}>
          Operasyon:{' '}
          <Link className="sec mono" to={functionCodeTracesHref({ window: pageRange, attrKey: o.key, codes: [o.code] })}>
            {o.operation}
          </Link>
          {o.total > 1 ? ` (+${o.total - 1})` : ''}
        </span>
      ))}
    </>
  );
}
