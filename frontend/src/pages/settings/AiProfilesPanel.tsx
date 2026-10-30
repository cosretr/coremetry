// AiProfilesPanel — v0.10.176 (dilim B; spec onayı 2026-08-30): model
// profilleri tablosu + düzenleme modalı + yüzey eşlemesi. Her profil kendi
// sağlayıcı/endpoint/anahtar/model'i (ai_settings_profiles.go); admin bir
// profili VARSAYILAN yapar, aşağıdaki eski form o profili düzenler.
// Anahtar hiç geri gelmez (hasKey); boş anahtar = mevcut korunur.
import { useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay';
import { useConfirm } from '@/components/coremetry/Confirm';
import { api } from '@/lib/api';
import type { AIModelProfile, AIModelProfileInput, AIProfilesPayload, AIProvider, AIProfileTestResult, AIThinking, AIToolset, Role } from '@/lib/types';
import { FlashBox } from './shared';
import { slugifyProfileId, PROFILE_ID_RE, tuningSummary, endpointLabel, profileUsable, PROFILE_ROLES, rolesSummary, toggleRole, defaultRolesWarning, DEFAULT_ROLES_WARNING, formatHeaderLines, parseHeaderLines } from './aiProfiles';

// v0.10.1139 — varsayılan profilin rol kısıtı uyarısı (FlashBox'ın uyarı tonu).
function RolesWarning({ text }: { text: string }) {
  return (
    <div role="note" data-default-roles-warning=""
      className="mt-2 rounded-sm border border-[color-mix(in_srgb,var(--warn)_30%,transparent)] bg-[color-mix(in_srgb,var(--warn)_8%,transparent)] px-2.5 py-1.5 text-[length:var(--fs-sm)] text-(--warn)">
      ⚠ {text}
    </div>
  );
}

// v0.10.942 (tablo standardı dilim 3) — 11px ikincil hücreler `tone: muted`
// (S3); eylemler `kind: 'actions'`. 340 = eski `trailing` 330 + ButtonGroup'un
// 6px aralığı (3×6px, eski 3×~3.4px boşluk yerine): 330'da 4 buton 308.7px'e
// çıkıp 306px içeriği aşıyor, `.btn-group` sarıyor ve «Sil» ikinci satıra düşüyordu.
const COLS: ColumnDef<AIModelProfile>[] = [
  { id: 'label',    label: 'Profil',    sortValue: p => p.label || p.id,                naturalDir: 'asc', width: 230 },
  { id: 'provider', label: 'Sağlayıcı', sortValue: p => p.provider,                     naturalDir: 'asc', width: 115 },
  { id: 'model',    label: 'Model',     sortValue: p => p.model ?? '',                  naturalDir: 'asc', width: 160, mono: true, tone: () => 'muted' },
  { id: 'endpoint', label: 'Endpoint',  sortValue: p => endpointLabel(p.provider, p.baseUrl), naturalDir: 'asc', width: 210, mono: true, tone: () => 'muted' },
  { id: 'key',      label: 'Anahtar',   sortValue: p => (p.hasKey ? 1 : 0),             width: 90 },
  { id: 'tuning',   label: 'Tuning',    sortValue: p => tuningSummary(p),                width: 130, tone: () => 'muted' },
  { id: 'actions',  label: 'Eylemler',  kind: 'actions', width: 340, minWidth: 340 },
];

type Draft = { id: string; label: string; provider: AIProvider; baseUrl: string; apiKey: string; model: string; skipTls: boolean; maxTokens: string; temperature: string; timeoutS: string; thinking: AIThinking; toolset: AIToolset; description: string; roles: Role[]; deep: boolean; headers: string; localOnly: boolean };
const emptyDraft = (): Draft => ({ id: '', label: '', provider: 'openai', baseUrl: '', apiKey: '', model: '', skipTls: false, maxTokens: '', temperature: '', timeoutS: '', thinking: '', toolset: '', description: '', roles: [], deep: false, headers: '', localOnly: false });
const draftOf = (p: AIModelProfile): Draft => ({
  id: p.id, label: p.label ?? '', provider: p.provider, baseUrl: p.baseUrl ?? '', apiKey: '', model: p.model ?? '', skipTls: !!p.skipTls,
  maxTokens: p.maxTokens ? String(p.maxTokens) : '', temperature: p.temperature === undefined || p.temperature === null ? '' : String(p.temperature), timeoutS: p.timeoutS ? String(p.timeoutS) : '',
  thinking: p.thinking ?? '',
  toolset: p.toolset ?? '', // v0.10.1302
  description: p.description ?? '', roles: p.roles ?? [], // v0.10.1138
  deep: !!p.deep, // v0.10.1150
  headers: formatHeaderLines(p.headers), // v0.10.1238
  localOnly: !!p.localOnly, // v0.10.1451
});

export function AiProfilesPanel({ payload, onChange }: { payload: AIProfilesPayload; onChange: (p: AIProfilesPayload) => void }) {
  const confirm = useConfirm();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const defaultWarn = defaultRolesWarning(payload.profiles); // v0.10.1139
  const [tests, setTests] = useState<Record<string, AIProfileTestResult | 'pending'>>({});
  const [surface, setSurface] = useState({ intent: payload.surfaceMap.intent ?? '', background: payload.surfaceMap.background ?? '' });
  // silme/varsayılan sonrası sunucunun haritası yeniden yüklenir (bayat kimlik 400 verirdi, inceleme #4)
  useEffect(() => { setSurface({ intent: payload.surfaceMap.intent ?? '', background: payload.surfaceMap.background ?? '' }); }, [payload.surfaceMap.intent, payload.surfaceMap.background]);
  const set = (patch: Partial<Draft>) => setDraft(d => (d ? { ...d, ...patch } : d));

  const run = async (fn: () => Promise<AIProfilesPayload>, ok: string) => {
    setBusy(true); setMsg(null);
    try { onChange(await fn()); setMsg({ kind: 'ok', text: ok }); }
    catch (e) { setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) }); }
    finally { setBusy(false); }
  };
  const saveDraft = async (e: FormEvent) => {
    e.preventDefault();
    if (!draft) return;
    const id = isNew ? (draft.id || slugifyProfileId(draft.label)) : draft.id;
    if (!PROFILE_ID_RE.test(id)) { setMsg({ kind: 'err', text: 'Kimlik [a-z0-9][a-z0-9_-]{0,39} olmalı (etiketten türetilir, düzenlenebilir).' }); return; }
    const hdr = parseHeaderLines(draft.headers); // v0.10.1238
    if ('error' in hdr) { setMsg({ kind: 'err', text: hdr.error }); return; }
    const body: AIModelProfileInput = {
      label: draft.label.trim() || undefined, provider: draft.provider, baseUrl: draft.baseUrl.trim() || undefined,
      apiKey: draft.apiKey || undefined, model: draft.model.trim() || undefined, skipTls: draft.skipTls,
      maxTokens: draft.maxTokens ? Number(draft.maxTokens) : undefined,
      temperature: draft.temperature === '' ? undefined : Number(draft.temperature),
      timeoutS: draft.timeoutS ? Number(draft.timeoutS) : undefined,
      thinking: draft.thinking || undefined,
      toolset: draft.toolset, // v0.10.1302 — '' = otomatik (gönderilir: eski açık ayar temizlenebilsin)
      description: draft.description.trim() || undefined, // v0.10.1138 — sohbet seçicisindeki kısa açıklama
      roles: draft.roles, // v0.10.1138 — boş = herkes (sunucu üçü de seçiliyse boşa indirir)
      deep: draft.deep, // v0.10.1150 — "Derin düşün" profili (tek; sunucu öncekini düşürür)
      headers: hdr.headers, // v0.10.1238 — tam liste; boş = hepsi silinir
      localOnly: draft.provider === 'openai' && draft.localOnly, // v0.10.1451 — yalnız openai-uyumlu (sunucu da reddeder)
    };
    await run(() => api.putAIProfile(id, body), isNew ? `Profil eklendi: ${id}` : `Profil güncellendi: ${id}`);
    setDraft(null);
  };
  const test = async (id: string) => {
    setTests(t => ({ ...t, [id]: 'pending' }));
    try { const r = await api.testAIProfile(id); setTests(t => ({ ...t, [id]: r })); }
    catch (e) { setTests(t => ({ ...t, [id]: { ok: false, ms: 0, profile: id, error: e instanceof Error ? e.message : String(e) } })); }
  };
  const saveSurface = () => run(() => api.putAISurfaceMap({ intent: surface.intent, background: surface.background }), 'Yüzey eşlemesi kaydedildi');
  const options = payload.profiles.map(p => <option key={p.id} value={p.id}>{p.label || p.id}</option>);

  // Faz 2 dilim 1 R11 — hücreler coremetry DataTable'ın renderCell'inde (kolonlar,
  // sıra, genişlikler aynı). undefined = DataTableCell'in soluk "—"sı.
  const renderCell = (p: AIModelProfile, col: ColumnDef<AIModelProfile>): ReactNode => {
    switch (col.id) {
      case 'label': return (
        <>
          <div className="cell-strong truncate" title={p.id}>{p.label || p.id}</div>
          {/* v0.10.179 — rozet kendi satırında: ellipsis rozeti yutuyordu (178 canlı görüntüsü) */}
          <div className="field-hint flex items-center gap-1.5 font-mono">
            {p.label ? p.id : null}{p.default && <Badge variant="outline">varsayılan</Badge>}
            {/* v0.10.1138 — rol allowlist'i: yalnız kısıtlıysa görünür */}
            {!!p.roles?.length && <Badge variant="secondary" title="Sohbette bu profili açıkça seçebilen roller">{rolesSummary(p.roles)}</Badge>}
            {/* v0.10.1150 — sohbette "Derin" açıkken (açık seçim yoksa) bu profil */}
            {p.deep && <Badge variant="secondary" title="Derin düşün profili: sohbette Derin açıkken ve model açıkça seçilmemişken kullanılır">derin</Badge>}
            {/* v0.10.1451 (karar 8) — kod ve wiki içeriği yalnız bu işaretli profillere gider */}
            {p.localOnly && <Badge variant="secondary" title="Yerel (self-hosted) model: kaynak kod ve wiki içeriği yalnız bu işaretli profillere gider">yerel</Badge>}
            {/* v0.10.1302 — CoSRE araç seti: yalnız basit katalogda görünür (açık ayar ya da ≤12B otomatik) */}
            {p.effectiveToolset === 'simple' && <Badge variant="secondary" title={p.toolset === 'simple' ? 'Araç seti: basit (açık ayar) — CoSRE sohbetinde serbest-sorgu araçları (query_metric, search_logs, search_traces, list_metric_labels) gizli' : 'Araç seti: basit (otomatik — model adı ≤12B) — CoSRE sohbetinde serbest-sorgu araçları gizli'}>basit</Badge>}
          </div>
        </>
      );
      case 'provider': return <Badge variant="secondary">{p.provider}</Badge>;
      case 'model': return p.model || undefined;
      case 'endpoint': return endpointLabel(p.provider, p.baseUrl) || undefined;
      case 'key': return p.hasKey ? <Badge variant="secondary">stored</Badge> : profileUsable(p) ? <Badge variant="secondary">no auth</Badge> : <StatusBadge status="warn">yok</StatusBadge>;
      case 'tuning': return tuningSummary(p) || undefined;
      case 'actions': {
        const t = tests[p.id];
        return (
          <>
            <div role="group" aria-label={`${p.label || p.id} eylemleri`} className="flex flex-wrap items-center justify-end gap-1.5">
              <Button variant="ghost" size="xs" onClick={() => { setIsNew(false); setDraft(draftOf(p)); }}>Düzenle</Button>
              {!p.default && <Button variant="ghost" size="xs" disabled={busy} onClick={() => run(() => api.defaultAIProfile(p.id), `Varsayılan: ${p.id}`)}>Varsayılan yap</Button>}
              <Button variant="ghost" size="xs" loading={t === 'pending'} onClick={() => test(p.id)}>Bağlantıyı dene</Button>
              {!p.default && <Button variant="ghost-danger" size="xs" disabled={busy} onClick={async () => {
                if (!await confirm({ title: 'Profil silinsin mi?', body: <><b>{p.label || p.id}</b> silinecek; bu profile eşlenmiş yüzeyler varsayılana döner.</>, confirmLabel: 'Sil', danger: true })) return;
                await run(() => api.deleteAIProfile(p.id), `Silindi: ${p.id}`);
              }}>Sil</Button>}
            </div>
            {t && t !== 'pending' && (
              <div className={`field-hint text-right ${t.ok ? '' : 'is-err'}`} title={t.error ?? t.sample}>
                {t.ok ? `✓ ${t.ms} ms${t.sample ? ` · «${t.sample}»` : ''}` : `✗ ${t.error ?? 'hata'}`}
              </div>
            )}
          </>
        );
      }
      default: return undefined;
    }
  };

  return (
    <div className="mb-5">
      <div className="flex flex-wrap items-baseline gap-3">
        <h3 className="m-0 text-[length:var(--fs-md)] font-semibold">Model profilleri</h3>
        <span className="field-hint">{payload.profiles.length} profil · varsayılan: <b>{payload.defaultProfile}</b> · aşağıdaki form varsayılan profili düzenler</span>
        <span className="ml-auto flex gap-2">
          <Button variant="outline" size="sm" onClick={() => { setIsNew(true); setDraft(emptyDraft()); }}>Profil ekle</Button>
        </span>
      </div>
      {defaultWarn && <RolesWarning text={defaultWarn} />}
      {/* v0.10.967 — tablo boş olamaz: AiTab paneli yalnız `profiles.length > 0` iken
          bağlar ve varsayılan profil silinemez (copilot.DeleteProfile); yükleniyor /
          hata sekmenin ayar yükleme kapısında. Faz 2 dilim 1 R11: coremetry DataTable
          `state` ister → boş türü (asla görünmez, boş panel kuralının sözleşmesi). */}
      <div className="table-wrap mt-2">
        <DataTable<AIModelProfile> storageKey="settings-ai-profiles" columns={COLS} rows={payload.profiles}
          sort={{ initial: { id: 'label', dir: 'asc' } }} getRowKey={p => p.id}
          state={{ kind: 'empty', message: 'Profil yok' }} renderCell={renderCell} />
      </div>

      <div className="mt-3 flex flex-wrap items-end gap-3">
        <SelectField className="w-auto min-w-[200px] flex-1" label="Niyet sınıflandırıcısı (chat-intent)" value={surface.intent} onChange={e => setSurface(s => ({ ...s, intent: e.target.value }))} hint="Serbest soru sınıflandırma çağrısı için profil; boş = varsayılan (küçük yerel model önerilir). Eşleşmeyen sorunun genel bilgi cevabı (chat-general) VARSAYILAN profilden gelir — sınıflandırıcıya seçilen küçük model genel cevap yazmaz.">
          <option value="">varsayılan</option>{options}
        </SelectField>
        <SelectField className="w-auto min-w-[200px] flex-1" label="Arka plan açıklayıcılar (auto-explain)" value={surface.background} onChange={e => setSurface(s => ({ ...s, background: e.target.value }))} hint="Problem/exception otomatik özetleri için profil; boş = varsayılan.">
          <option value="">varsayılan</option>{options}
        </SelectField>
        <Button variant="outline" size="sm" disabled={busy} onClick={saveSurface}>Eşlemeyi kaydet</Button>
      </div>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

      <Dialog open={!!draft} onOpenChange={o => { if (!o) setDraft(null); }}>
        <DialogContent aria-describedby={undefined} className="max-h-[calc(100vh-32px)] gap-0 overflow-auto p-0 sm:max-w-[480px]">
          <DialogHeader className="border-b border-(--divider) px-[18px] py-3.5">
            <DialogTitle className="text-[length:var(--fs-lg)]">{isNew ? 'Yeni model profili' : `Profili düzenle · ${draft?.id ?? ''}`}</DialogTitle>
          </DialogHeader>
          {draft && (
            <form id="ai-profile-form" onSubmit={saveDraft} className="flex flex-col gap-3 px-[18px] py-4">
              <Field label="Etiket" hint="Tabloda ve seçicilerde görünen ad" value={draft.label} onChange={e => set({ label: e.target.value, id: isNew ? slugifyProfileId(e.target.value) : draft.id })} placeholder="Büyük model (gemma4-31b)" />
              <Field label="Kimlik" hint={isNew ? 'Etiketten türetilir; [a-z0-9][a-z0-9_-]{0,39}' : 'Kimlik değiştirilemez'} controlClassName="font-mono" value={draft.id} disabled={!isNew} onChange={e => set({ id: e.target.value })} />
              <SelectField label="Sağlayıcı" value={draft.provider} onChange={e => set({ provider: e.target.value as AIProvider })}>
                <option value="openai">OpenAI-uyumlu (vLLM / Ollama / LM Studio / OpenAI)</option>
                <option value="anthropic">Anthropic</option>
                <option value="github">GitHub Copilot</option>
              </SelectField>
              {draft.provider === 'openai' && <Field label="Base URL" hint="Örn. http://vllm:8000/v1 — her profil kendi endpoint'ine gider" value={draft.baseUrl} onChange={e => set({ baseUrl: e.target.value })} placeholder="http://vllm:8000/v1" />}
              <Field label="Model" value={draft.model} onChange={e => set({ model: e.target.value })} placeholder="gemma4-31b" />
              {/* v0.10.1138 — sohbet "model ▾" menüsünde ad + model + bu kısa açıklama görünür */}
              <Field label="Kısa açıklama" hint="Sohbetin model menüsünde görünür (≤120 karakter), ör. «Hızlı, kısa cevaplar»" value={draft.description} maxLength={120} onChange={e => set({ description: e.target.value })} placeholder="Derin analiz, yavaş" />
              <fieldset className="m-0 border-0 p-0">
                <legend className="field-hint mb-1">Sohbette seçebilen roller — hiçbiri işaretli değilse herkes</legend>
                <div className="flex gap-3">
                  {PROFILE_ROLES.map(r => (
                    <CheckboxField key={r} label={r} checked={draft.roles.includes(r)} onCheckedChange={() => set({ roles: toggleRole(draft.roles, r) })} />
                  ))}
                </div>
                {!isNew && draft.roles.length > 0 && payload.profiles.some(p => p.id === draft.id && p.default) && <RolesWarning text={DEFAULT_ROLES_WARNING} />}
              </fieldset>
              {/* v0.10.1150 — tek derin profil: işaretlemek öncekinin işaretini kaldırır (sunucu); rol allowlist'i burada da geçerli. */}
              <div title="Sohbette Derin açıkken ve model açıkça seçilmemişken bu profil kullanılır. Tek profil işaretli olabilir; rolü bu profile kapalı kullanıcıda mevcut model kalır.">
                <CheckboxField label="Derin düşün profili" checked={draft.deep} onCheckedChange={v => set({ deep: v })} />
              </div>
              <Field label="API anahtarı" hint={isNew ? 'Yerel endpoint için boş bırakılabilir' : 'Boş = mevcut anahtar korunur'} type="password" autoComplete="new-password" value={draft.apiKey} onChange={e => set({ apiKey: e.target.value })} />
              {/* v0.10.1238 — geçit yönlendirmesi vb. için her LLM isteğine eklenen başlıklar; auth başlıkları sunucuda reddedilir */}
              <TextareaField label="Ek HTTP başlıkları" hint="Satır başına «Ad: değer», ör. x-cluster: test-v2 — bu profilin her LLM isteğine eklenir. Authorization / api-key / Content-Type verilemez."
                controlClassName="font-mono" rows={3} value={draft.headers} onChange={e => set({ headers: e.target.value })} placeholder="x-cluster: test-v2" spellCheck={false} />
              {/* v0.10.1451 (karar 8) — admin beyanı, anında yürürlükte; model adından çıkarım yok */}
              {draft.provider === 'openai' && (
                <CheckboxField label="Yerel (self-hosted) model — kod ve wiki içeriği yalnız bu profile gider" checked={draft.localOnly} onCheckedChange={v => set({ localOnly: v })}
                  hint="Kaynak kod («Kodu da incele», read_source_code, search_code) ve wiki sayfa içeriği YALNIZ bu işaretli profillere gönderilir. Hiçbir profil işaretli değilse kod/wiki okuma her yerde «kapalı: yerel profil yok» der. Yalnız kurumunuzda barınan uçlar için işaretleyin; genel LLM adresleri reddedilir." />
              )}
              {draft.provider === 'openai' && <CheckboxField label="TLS doğrulamasını atla (öz-imzalı yerel uç)" checked={draft.skipTls} onCheckedChange={v => set({ skipTls: v })} />}
              <div className="flex flex-wrap items-start gap-3">
                <Field label="Max tokens" small hint="boş = küresel" value={draft.maxTokens} onChange={e => set({ maxTokens: e.target.value })} inputMode="numeric" />
                <Field label="Temperature" small hint="boş = küresel; 0 bir değerdir; Anthropic profillerinde uygulanmaz" value={draft.temperature} onChange={e => set({ temperature: e.target.value })} inputMode="decimal" />
                <Field label="Timeout (s)" small hint="boş = küresel" value={draft.timeoutS} onChange={e => set({ timeoutS: e.target.value })} inputMode="numeric" />
              </div>
              {/* v0.10.534 (modelcaps) — düşünme anahtarı modelin ailesine göre gövdeye iner
                  (Qwen3: chat_template_kwargs.enable_thinking); anahtarı bilinmeyen ailede
                  sunucu bir kez uyarır, gövde değişmez. Boş = modele dokunma. */}
              {draft.provider === 'openai' && (
                <SelectField label="Düşünme (thinking)" value={draft.thinking} onChange={e => set({ thinking: e.target.value as AIThinking })}
                  hint="Qwen3 gibi düşünen modellerde 'kapalı' boş cevap riskini keser; gemma/llama'da etkisiz. Boş = varsayılan.">
                  <option value="">varsayılan (dokunma)</option>
                  <option value="off">kapalı (enable_thinking=false)</option>
                  <option value="on">açık</option>
                </SelectField>
              )}
              {/* v0.10.1302 — CoSRE araç seti: küçük modelde serbest-sorgu araçları gizlenir;
                  dış MCP sunucusu ve guided/niyet kademeleri etkilenmez. */}
              <SelectField label="Araç seti" value={draft.toolset} onChange={e => set({ toolset: e.target.value as AIToolset })}
                hint="CoSRE sohbetinin bu profille gördüğü araçlar. Basit: query_metric / search_logs / search_traces / list_metric_labels gizli, yapısal query_metrics / query_logs / query_traces kalır (küçük model için). Otomatik: model adında ≤12B boyut varsa basit, aksi hâlde tam.">
                <option value="">otomatik (model adına göre, ≤12B basit)</option>
                <option value="simple">basit</option>
                <option value="full">tam</option>
              </SelectField>
            </form>
          )}
          <DialogFooter className="border-t border-(--divider) bg-(--bg2) px-[18px] py-3">
            <ActionRow
              secondary={<Button variant="outline" onClick={() => setDraft(null)}>Vazgeç</Button>}
              confirm={<Button type="submit" form="ai-profile-form" loading={busy}>Kaydet</Button>} />
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
