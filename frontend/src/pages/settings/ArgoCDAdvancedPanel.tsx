import type { ReactNode } from 'react';
import { ChevronRight } from 'lucide-react';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { Field, SelectField } from '@/components/coremetry/Field';
import type { ArgoCDBound, ArgoCDPin } from '@/lib/types';
import {
  ADV_GROUPS, advError, advInputId, advSummary, fmtTr,
  type AdvNumKey, type Draft, type Issue, type MetricsOnlyMode,
} from './argocdForm';

// ArgoCDAdvancedPanel — v0.10.974 — kapalı "Gelişmiş" bölümü + kapalı, salt
// okunur "Elle eşlemeler (pins)" notu (mockup Main).
//
// Aralıklar ve varsayılanlar GET `bounds`tan (sunucunun intField tablosu —
// kod ve arayüz ayrışamaz); boş kutu = varsayılan (PUT'ta 0). Çapraz alanlar
// (out_of_band ≥ pencere, namespace güveni < ad güveni) ETKİN değerlerle,
// sunucunun hata yoluyla. Pin düzenleyicisi yok: burada yalnız sayı ve bağlı
// instance'lar; Kaydet pins[]'i olduğu gibi geri gönderir. v0.10.985 (P3.2):
// notun metni pinlerin NE ZAMAN kullanıldığını söyler (metrik işçisi açıkken
// eşleyici her mapperMin turunda; GitOps sekmesi işçi kapalıyken de uygular).
// v0.10.974 — pins notunun açıklığı sekmede (`pinsOpen`): sunucunun
// `pins[i]…` 400'ünün bağlantısı notu açıp başlığına odaklanır; kip seçimi
// `classification.metricsOnlyMode` 400'ünü alanın altında gösterir.

//
// Faz 2 dilim 1 R15: ui DisclosureButton → shadcn collapsible (SsoPresets R10
// deseni; açıklık yine sekmede — denetimli `open`), tetikleyicinin id'si ve
// `aria-controls`'u, gövdenin id'si aynı (goTo odak hedefleri); gövde yalnız
// açıkken bağlanır. Field/SelectField → coremetry/Field; CSSProperties → utility.

const SUMMARY = 'min-w-0 truncate text-[length:var(--fs-sm)] text-(--text3)';
const TEXT = 'm-0 max-w-[760px] text-[length:var(--fs-sm)] leading-[18px] text-(--text2)';

/** Başlık + özet satırı; başlığın içinde collapsible tetikleyicisi (h3 > button). */
function Head({ hId, btnId, bodyId, summary, children }: { hId: string; btnId: string; bodyId: string; summary: ReactNode; children: ReactNode }) {
  return (
    <div className="flex items-center gap-4">
      <h3 id={hId} className="m-0 text-[length:var(--fs-md)] font-semibold">
        <CollapsibleTrigger id={btnId} aria-controls={bodyId}
          className="group/dsc inline-flex cursor-pointer items-center gap-1.5 border-0 bg-transparent p-0 text-left text-[length:var(--fs-md)] font-semibold text-(--text) hover:text-(--accent2)">
          <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 transition-transform group-data-[state=open]/dsc:rotate-90" />
          {children}
        </CollapsibleTrigger>
      </h3>
      <span className={SUMMARY}>{summary}</span>
    </div>
  );
}

export function ArgoCDAdvancedPanel({ draft, bounds, pins, issues, open, onOpen, pinsOpen, onPinsOpen, onAdv, onMode }: {
  draft: Draft;
  bounds: Record<string, ArgoCDBound>;
  pins: ArgoCDPin[];
  issues: Issue[];
  open: boolean;
  onOpen: (open: boolean) => void;
  pinsOpen: boolean;
  onPinsOpen: (open: boolean) => void;
  onAdv: (key: AdvNumKey, value: string) => void;
  onMode: (mode: MetricsOnlyMode) => void;
}) {
  const byInstance = new Map<string, number>();
  for (const p of pins) byInstance.set(p.instanceId, (byInstance.get(p.instanceId) ?? 0) + 1);
  const pinsList = [...byInstance].map(([id, n]) => `${id} (${n})`).join(', ') || 'yok';
  const mode = draft.metricsOnlyMode || 'estimate';
  const modeIssue = issues.find(i => i.target.kind === 'advMode');

  return (
    <>
      <Collapsible asChild open={open} onOpenChange={onOpen}>
        <section aria-labelledby="acd-adv-h" className="flex flex-col gap-3">
          <Head hId="acd-adv-h" btnId="acd-adv-btn" bodyId="acd-adv-body" summary={advSummary(draft, bounds)}>Gelişmiş</Head>
          <CollapsibleContent id="acd-adv-body" className="card flex flex-col gap-4">
            <p className={TEXT}>Boş alan = varsayılan. Aralık dışı değer kaydedilmez (400, alan yolu ile). Okuyucu tavanları karar 2'den: ≤ 50.000 seri, ≤ 64 MiB, ≤ 45 sn.</p>
            <div className="grid grid-cols-3 gap-6">
              {ADV_GROUPS.map(g => (
                <fieldset key={g.title} className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0">
                  <legend className="mb-1.5 p-0 text-[length:var(--fs-sm)] font-semibold text-(--text2)">{g.title}</legend>
                  {g.fields.map(f => {
                    const b = bounds[f.key];
                    const server = issues.find(i => i.target.kind === 'adv' && i.target.key === f.key);
                    const err = advError(draft.adv, f.key, bounds) || server?.message || '';
                    return (
                      <Field key={f.key} id={advInputId(f.key)} label={f.label} inputMode="decimal" autoComplete="off"
                        value={draft.adv[f.key]} error={err || undefined}
                        hint={b ? `${fmtTr(b.min)}–${fmtTr(b.max)} · varsayılan ${fmtTr(b.default)}` : undefined}
                        onChange={e => onAdv(f.key, e.target.value)} />
                    );
                  })}
                  {g.mode && (
                    <SelectField id="acd-adv-mode" label="tokenRef yokken" value={mode} error={modeIssue?.message}
                      hint={mode === 'unknown' ? 'Yalnız unknown; tahmin gösterilmez.' : 'Metriklerden “tahmini” argo_manual / argo_auto (karar 15).'}
                      onChange={e => onMode(e.target.value === 'unknown' ? 'unknown' : 'estimate')}>
                      <option value="estimate">estimate</option>
                      <option value="unknown">unknown</option>
                    </SelectField>
                  )}
                </fieldset>
              ))}
            </div>
          </CollapsibleContent>
        </section>
      </Collapsible>

      <Collapsible asChild open={pinsOpen} onOpenChange={onPinsOpen}>
        <section aria-labelledby="acd-pins-h" className="flex flex-col gap-3">
          <Head hId="acd-pins-h" btnId="acd-pins-btn" bodyId="acd-pins-body"
            summary={`${pins.length} kayıt (API ile yazıldı) · GitOps sekmesi ve eşleyici kullanır`}>
            Elle eşlemeler <span className="font-mono font-normal text-(--text3)">pins</span>
          </Head>
          <CollapsibleContent asChild id="acd-pins-body">
            <p className={TEXT}>
              Bir iş yükünü bir Application'a elle bağlar; elle bağlantı o iş yükü için ad tahminini devre dışı bırakır. Metrik işçisi
              (<code>argocd-metrics</code>) açıkken eşleyici pin'leri her turda (<code>intervals.mapperMin</code>){' '}
              <code>argocd_app_mapping</code>'e <code>match_method='manual'</code> olarak kopyalar; GitOps sekmesi işçi kapalıyken de pin'leri
              istek anında uygular. Düzenleyici yok — şimdilik yalnız API: <code>PUT /api/settings/argocd</code>{' '}
              gövdesinde <code>pins[]</code>. Kaydet <code>pins[]</code>'i olduğu gibi geri gönderir. Pin'i olan instance tablodan kaldırılamaz;
              önce API'den pin'leri kaldırın. Bağlı instance'lar: <span className="font-mono">{pinsList}</span>.
            </p>
          </CollapsibleContent>
        </section>
      </Collapsible>
    </>
  );
}
