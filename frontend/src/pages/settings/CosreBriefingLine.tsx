import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { CheckboxField } from '@/components/coremetry/Field';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import type { CosreBriefingSettings } from '@/lib/types';

// v0.10.2105 (docs/cosre/roadmap-status.md Onay 4, §2 satır 7) — giriş özeti
// kapanışı: Settings › CoSRE'de CosreV2StatusLine'ın altında TEK aç/kapa.
// Varsayılan KAPALI (sunucu briefing.DefaultConfig). Eşik/aralık alanı YOK
// (operatör vida istemiyor); PUT bütün blobu yazar → GET'ten gelen tüm
// alanlar aynen ileri taşınır, yalnız `enabled` değişir. Audit sunucuda
// (`settings.cosre_briefing.update`). Üretici cosre_v2'ye bakmaz: v2 kapalı
// ve özet açıksa kart kimseye görünmeden üretilir → kapatmak HER ZAMAN
// serbest, açmak v2 açıkken.
const KEY = ['ai', 'briefing-settings'] as const;
const V2_KEY = ['admin', 'cosre-v2'] as const; // CosreV2StatusLine ile ortak

/** SAF: açıkken arka planda ne koştuğu — sayılar ayardan. */
export function briefingHint(c: CosreBriefingSettings): string {
  const min = Math.max(1, Math.round(c.refresh_interval_s / 60));
  const hrs = Math.max(1, Math.round(c.window_s / 3600));
  return `Açıkken worker ${min} dakikada bir, kullanıcısı olan her takım için son ${hrs} saati önceki ${hrs} saatle kıyaslar `
    + '(ClickHouse özetleri, VictoriaMetrics, loglar, Oracle hataları)'
    + (c.llm_phrasing ? ', başlık cümlesini modele yazdırır' : '')
    + ' ve kartı CoSRE v2 karşılamasına yazar.';
}

export function CosreBriefingLine() {
  const qc = useQueryClient();
  const role = useAuthRole(); // sağlayıcısız ağaç (Settings zaten admin kapılı) → düzenlenebilir
  const canEdit = role === undefined || role === 'admin';
  const q = useQuery({ queryKey: KEY, queryFn: () => api.cosreBriefingSettings(), staleTime: 30_000 });
  // Bayrak ucu admin-only; viewer zaten salt okunur, 403 isteği atılmaz.
  const v2 = useQuery({ queryKey: V2_KEY, queryFn: () => api.cosreV2Flag(), staleTime: 30_000, enabled: canEdit });
  const m = useMutation({
    mutationFn: (enabled: boolean) => {
      const cur = qc.getQueryData<CosreBriefingSettings>(KEY);
      if (!cur) return Promise.reject(new Error('ayar okunmadı'));
      return api.putCosreBriefingSettings({ ...cur, enabled });
    },
    onSuccess: (d: CosreBriefingSettings) => qc.setQueryData(KEY, d),
  });
  if (q.isPending) return <div className="mb-2"><Spinner label="Giriş özeti ayarı okunuyor…" /></div>;
  if (q.isError) {
    return (
      <div className="mb-2" data-testid="cosre-briefing-line">
        <span className="field-hint" title={q.error instanceof Error ? q.error.message : ''}>Giriş özeti: ayar okunamadı</span>
      </div>
    );
  }
  const on = m.isPending ? m.variables === true : q.data.enabled;
  const v2Off = canEdit && v2.data?.enabled !== true;
  const why = !canEdit ? 'Yalnız admin değiştirebilir.'
    : v2.isPending ? null
      : v2Off ? (on ? 'CoSRE v2 sohbeti kapalı: özet görünmüyor ama üretiliyor; kapatabilirsiniz.'
        : 'CoSRE v2 sohbeti kapalı; özet yalnız v2 karşılamasında görünür, önce onu açın.')
        : null;
  return (
    <div className="mb-2 flex flex-wrap items-center gap-3" data-testid="cosre-briefing-line">
      <CheckboxField
        label="Giriş özeti (CoSRE v2 karşılaması)"
        hint={briefingHint(q.data)}
        checked={on}
        disabled={!canEdit || m.isPending || v2.isPending || (v2Off && !on)}
        onCheckedChange={v => m.mutate(v)} />
      {why && <span className="field-hint">{why}</span>}
      {m.isPending && <Spinner label="Kaydediliyor…" />}
      {m.isError && (
        <span role="alert" className="text-[length:var(--fs-sm)] text-(--err)">
          Kaydedilemedi{m.error instanceof Error && m.error.message ? `: ${m.error.message}` : ''}
        </span>
      )}
    </div>
  );
}
