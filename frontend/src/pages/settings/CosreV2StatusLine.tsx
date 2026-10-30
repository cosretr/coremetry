import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { CheckboxField } from '@/components/coremetry/Field';
import { api } from '@/lib/api';
import type { CosreV2Flag } from '@/lib/types';

// v0.10.1604 (operatör kararı 2026-10-29 §4) — /ai sayfasına ayrı bir v2
// görünümü EKLENMEDİ; yerine Settings › CoSRE başında tek satır.
// v0.10.1647 (CoSRE v2 Faz 2b B1, operatör Q10 2026-10-30) — salt okunur durum
// satırı ADMIN anahtarına döndü: GET/PUT /api/admin/cosre-v2 (admin; audit
// `settings.cosre_v2.update` sunucuda). Bayrak istemciye /api/copilot/config
// modül önbelleğiyle gelir (süreç ömrü sabit) → değişiklik YENİ SAYFA
// YÜKLEMESİNDE görünür; diğer podlara ≤30 s (sunucu memo'su), açık akışlar
// kesilmez (v0.10.1602). İki gerçekleme aynı kalıcılığı kullanır: çevirmek
// thread kaybettirmez. Dosya adı Settings dilim planlarındaki atıflar için
// korunur.
const KEY = ['admin', 'cosre-v2'] as const;

export function CosreV2StatusLine() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: KEY, queryFn: () => api.cosreV2Flag(), staleTime: 30_000 });
  const m = useMutation({
    mutationFn: (enabled: boolean) => api.putCosreV2Flag({ enabled }),
    onSuccess: (d: CosreV2Flag) => qc.setQueryData(KEY, d),
  });
  return (
    <div className="mt-1 mb-2 flex flex-wrap items-center gap-3" data-testid="cosre-v2-status">
      {q.isPending ? <Spinner label="CoSRE v2 durumu okunuyor…" />
        : q.isError ? (
          <span className="field-hint" title={q.error instanceof Error ? q.error.message : ''}>
            CoSRE v2 sohbeti: durum okunamadı
          </span>
        ) : (
          <CheckboxField
            label="CoSRE v2 sohbeti (önizleme)"
            hint="Değişiklik yeni sayfa yüklemesinde görünür. Kapatınca v1 sohbeti aynı konuşmalarla döner."
            checked={m.isPending ? m.variables === true : q.data.enabled}
            disabled={m.isPending}
            onCheckedChange={v => m.mutate(v)} />
        )}
      {m.isPending && <Spinner label="Kaydediliyor…" />}
      {m.isError && (
        <span role="alert" className="text-[length:var(--fs-sm)] text-(--err)">
          Kaydedilemedi{m.error instanceof Error && m.error.message ? `: ${m.error.message}` : ''}
        </span>
      )}
      {m.isSuccess && (
        <>
          <span role="status" className="text-[length:var(--fs-sm)] text-(--text2)">Kaydedildi — yeni yüklemede görünür.</span>
          <Button variant="outline" size="sm" onClick={() => window.location.reload()}>Şimdi yenile</Button>
        </>
      )}
    </div>
  );
}
