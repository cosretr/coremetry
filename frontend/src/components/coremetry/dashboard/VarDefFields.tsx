// coremetry/dashboard/VarDefFields — v0.10.2001 (Dashboards Faz 3 dilim 3.8).
// VariableEditor'ün yeni alanları (etiket adı, öneri metriği, çoklu seçim);
// Tailwind kaynağında dursun diye ayrı dosya (components/dashboard kaynak değil).
import { Input } from '@/components/shadcn/input';
import { Button } from '@/components/shadcn/button';
import type { DashboardVariable } from '@/lib/types';

export function VarDefFields({ v, onPatch }: { v: DashboardVariable; onPatch(p: Partial<DashboardVariable>): void }) {
  return (
    <>
      {v.type === 'label' && (
        <>
          <Input value={v.labelName ?? ''} placeholder="etiket adı (ör. deployment_environment)"
            aria-label="Etiket adı" className="h-8 w-56 font-mono text-xs"
            onChange={e => onPatch({ labelName: e.target.value.trim() || undefined })} />
          <Input value={v.metric ?? ''} placeholder="metrik (isteğe bağlı)"
            aria-label="Önerileri daraltan metrik" className="h-8 w-48 font-mono text-xs"
            onChange={e => onPatch({ metric: e.target.value.trim() || undefined })} />
        </>
      )}
      {v.type === 'label' && (
        <Button type="button" variant="outline" size="xs" aria-pressed={!!v.multi}
          title='Çoklu seçim: MetricsQL eşleyicisinde =~"a|b" olarak açılır; diğer alanlarda ilk değer'
          onClick={() => onPatch({ multi: v.multi ? undefined : true })}>
          Çoklu seçim
        </Button>
      )}
    </>
  );
}
