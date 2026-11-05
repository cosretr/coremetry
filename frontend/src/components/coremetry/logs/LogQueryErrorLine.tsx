// coremetry/logs/LogQueryErrorLine — v0.10.1821 (Log Search Faz 1 dilim 1.5,
// mockup görünüm 1 "Sorgu sözdizimi (konum N): … Sonuna ) ekleyin.").
//
// Sorgu çubuğunun ALTINDA, akışta (yüzen değil) konumlu hata satırı:
//   · istemci denetimi (kqlLint) — Enter'da, istek atılmadan;
//   · sunucunun 400 {error, pos, detail, stage} gövdesi (v0.10.1818/1819):
//     stage=precheck → sorgu arka uca hiç gitmedi; backend → arka ucun reddi.
// pos verilmişse GÖNDERİLEN sorgu (süzgeç hapları dahil derlenmiş metin)
// mono satırda, altında ^ ile (pos = 0 tabanlı karakter). Teknik ayrıntı
// (detail / admin raw) kapalı <details> içinde. role="alert".
import type { LogQueryError } from '@/lib/types';

export interface LogQueryErrorLineProps {
  /** İstemci denetimi mesajı (öncelikli: gönderilmemiş sorgu). */
  lint?: string | null;
  /** Sunucu reddi + o reddin ait olduğu GÖNDERİLEN sorgu metni. */
  error?: LogQueryError | null;
  query?: string;
}

export function LogQueryErrorLine({ lint, error, query }: LogQueryErrorLineProps) {
  if (!lint && !error) return null;
  const msg = lint || error!.error;
  const pos = !lint && error?.pos !== undefined && query !== undefined && error.pos <= query.length ? error.pos : undefined;
  const notRun = !!lint || error?.stage === 'precheck';
  const detail = !lint ? [error?.detail, error?.raw].filter(Boolean).join('\n') : '';
  return (
    <div role="alert" data-testid="log-query-error"
      className="mb-2 rounded-md border border-status-critical-border bg-status-critical-bg px-3 py-2 text-sm">
      <div>
        <b className="text-status-critical">{pos !== undefined ? `Sorgu sözdizimi (konum ${pos + 1}):` : 'Sorgu sözdizimi:'}</b>{' '}
        {msg}{notRun ? ' Sorgu çalıştırılmadı.' : ''}
      </div>
      {pos !== undefined && (
        <pre className="m-0 mt-1 overflow-x-auto font-mono text-xs leading-snug" aria-label={`Hata konumu: ${pos + 1}. karakter`}>
          {query}{'\n'}{' '.repeat(pos)}<span className="text-status-critical">^</span>
        </pre>
      )}
      {detail && (
        <details className="mt-1 text-xs text-muted-foreground">
          <summary className="cursor-pointer">Ayrıntı</summary>
          <pre className="m-0 mt-1 font-mono break-all whitespace-pre-wrap">{detail}</pre>
        </details>
      )}
    </div>
  );
}
