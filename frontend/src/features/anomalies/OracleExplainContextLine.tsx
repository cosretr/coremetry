// OracleExplainContextLine — v0.10.1100 (operatör onaylı: Oracle hata grubu
// için AI açıklaması span yerine Oracle bağlamı). AI panelinde (CopilotExplain,
// kind="exception", `ora:` parmak izi) "Kodu da incele" çipinin yerine çizilir:
// Oracle satırı stack taşımaz, kod okunacak satır yok. Tek satır
// "<etiket> · <kaynak> · <kod> · <operasyon>" (v0.10.1108: etiket branding
// oracleGroupLabel, varsayılan "Teknik hata"; title Oracle'ı açıklar).
//
// Veri: detay panelinin (OracleGroupPanel) AYNI sorgu anahtarı — detaydan
// açılan panelde ek istek yok; paylaşılan linkte bir kez okunur (fetch-on-open).
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useBranding } from '@/lib/branding';
import { oracleExplainLine } from './oracleGroup';

export function OracleExplainContextLine({ fingerprint }: { fingerprint: string }) {
  const label = useBranding().oracleGroupLabel;
  const q = useQuery({
    queryKey: ['exc-oracle-detail', fingerprint],
    queryFn: () => api.exceptionGroupOracle(fingerprint),
    staleTime: 30_000,
  });
  return (
    <div className="mono" data-testid="oracle-explain-context"
      title="Oracle hata tablosu grubu — açıklama stack yerine Oracle bağlamından (saatlik akış, kanal, servis, örnek satırlar) kurulur"
      style={{ fontSize: 12, color: 'var(--text2)', maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
      {oracleExplainLine(q.data, label)}
    </div>
  );
}
