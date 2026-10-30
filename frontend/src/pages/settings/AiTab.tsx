import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/shadcn/tabs';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { api } from '@/lib/api';
import { useSettingsLoad, SettingsLoadError, FlashBox, ConfigStatusBanner } from './shared';
import type { AIProvider, AISettings, AIIntentClassify, AIProfilesPayload } from '@/lib/types';
import { AiProfilesPanel } from './AiProfilesPanel';
import { AiBudgetPanel } from './AiBudgetPanel'; // v0.10.411
import { AiChatRetentionPanel } from './AiChatRetentionPanel'; // v0.10.561
import { AiEvalPanel } from './AiEvalPanel'; // v0.10.940
import { EVAL_URL_PARAMS } from './aiEval';
import { CosreRulesPanel } from '@/components/ai/CosreRulesPanel'; // v0.10.1289
import { AiToolsetsPanel } from './AiToolsetsPanel'; // v0.10.1434
import { AiGeneralQuestionsPanel } from './AiGeneralQuestionsPanel'; // v0.10.1458
import { CosreV2StatusLine } from './CosreV2StatusLine'; // v0.10.1604
import { useT } from '@/lib/i18n'; // v0.10.1434
import { tuningToForm, tuningToWire } from './aiTuning';
import { IconSparkles } from '@/components/icons';
import { Link, useSearchParams } from 'react-router-dom';

// v0.10.940 (operatör onayı K1, 2026-09-26) — Settings › CoSRE iki sekme:
// "Sağlayıcı ve profiller" (bugünkü gövde, AYNEN) ve "Değerlendirme"
// (evalset paneli, AiEvalPanel.tsx). Sekme URL'de (`?tab=eval`, replace;
// yoksa ilk sekme — eski /settings/ai linkleri aynı yere düşer).
//
// Şerit ayar-okuma kapısının ÖNÜNDE çizilir: kapı (SettingsLoadError /
// Spinner) ilk sekmenin gövdesine taşındı. Şerit kapının ARKASINDA
// kalsaydı GET /api/settings/ai başarısız olduğunda Değerlendirme
// sekmesine hiç ulaşılamazdı — oysa panel o uca değil, kendi
// /api/ai/evalset/… uçlarına dayanıyor. Neden yeni bir /settings bölümü
// (slug) DEĞİL: evalset CoSRE'nin bir alt konusu; ayrı bölüm hem tabIndex
// hem TAB_COMPS'ta yer açar ve operatörü iki yerde "AI ayarı" aratırdı.
// v0.10.1289 — üçüncü sekme "Kurallar" (`?tab=rules`): CoSRE operatör
// kuralları (CosreRulesPanel; /cosre kenar çubuğundaki modalla aynı panel).
// Ayarlar admin-only olduğu için burada düzenleme açık.
// v0.10.1434 — dördüncü sekme "Araç setleri" (`?tab=toolsets`): 74 aracın kaynak
// bazlı 12 toolset'i, sağlık + aç/kapa (AiToolsetsPanel; kendi /api/ai/toolsets
// ucuna dayanır, ayar okuması düşse de açılır).
type AiTabKey = 'provider' | 'eval' | 'rules' | 'toolsets';

function aiTabFromParam(v: string | null): AiTabKey {
  return v === 'eval' || v === 'rules' || v === 'toolsets' ? v : 'provider';
}

export function AITab() {
  const t = useT();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab: AiTabKey = aiTabFromParam(searchParams.get('tab'));
  const setTab = (t: AiTabKey) =>
    setSearchParams(prev => {
      const p = new URLSearchParams(prev);
      if (t !== 'provider') p.set('tab', t);
      else p.delete('tab');
      if (t !== 'eval') {
        // Değerlendirmenin koşu/vaka/kıyas parametreleri o sekmenin;
        // dönüşte bayat bir ?case= çekmeceyi kendiliğinden açmasın.
        for (const k of EVAL_URL_PARAMS) p.delete(k);
      }
      return p;
    }, { replace: true });
  // Faz 2 dilim 1 R11 — TabStrip → shadcn Tabs (öğeler, sıra, etiketler ve
  // URL sözleşmesi aynı); yalnız seçili sekmenin gövdesi bağlanır (Radix).
  const tabs: { key: AiTabKey; label: string }[] = [
    { key: 'provider', label: 'Sağlayıcı ve profiller' }, { key: 'eval', label: 'Değerlendirme' },
    { key: 'rules', label: 'Kurallar' }, { key: 'toolsets', label: t('aiToolsets.tab') },
  ];
  return (
    <>
      <CosreV2StatusLine />
      <Tabs value={tab} onValueChange={v => setTab(aiTabFromParam(v))}>
        <TabsList variant="line" aria-label="CoSRE ayar sekmeleri" className="mb-3">
          {tabs.map(x => <TabsTrigger key={x.key} value={x.key}>{x.label}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="provider"><AiProviderSettings /></TabsContent>
        <TabsContent value="eval"><AiEvalPanel /></TabsContent>
        <TabsContent value="rules"><CosreRulesPanel canEdit storageKey="settings-cosre-rules" /></TabsContent>
        <TabsContent value="toolsets"><AiToolsetsPanel /></TabsContent>
      </Tabs>
    </>
  );
}

// AiProviderSettings — editable AI Copilot configuration (v0.10.940'a dek
// AITab'ın kendisiydi; gövde değişmeden ilk sekmeye taşındı). Admin picks a
// provider, pastes their key, optionally sets a model, hits Save. Server stores
// the override in system_settings and updates the live service so the
// next Explain call uses the new creds without restart.
//
// Two providers:
//   - Anthropic: classic sk-ant-… key.
//   - GitHub Copilot: GitHub OAuth token (ghu_…) with Copilot access;
//     server exchanges it for a session token and calls
//     api.githubcopilot.com (OpenAI-compatible).
// `code` çipi (eskiden satır içi: --bg0, 1px 5px, 3px köşe).
const CODE = 'rounded-[3px] bg-(--bg0) px-[5px] py-px';

function AiProviderSettings() {
  const confirm = useConfirm();
  const [provider, setProvider] = useState<AIProvider>('anthropic');
  const [model, setModel] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [hasKey, setHasKey] = useState(false);
  const [apiKey, setApiKey] = useState('');
  const [skipTls, setSkipTls] = useState(false);
  // wf — master on/off toggle, distinct from hasKey. Default true so a
  // fresh / legacy backend (no "enabled" field) renders as enabled.
  const [enabled, setEnabled] = useState(true);
  // v0.9.1138 — arka plan otomatik açıklayıcı vidası (nil⇒açık).
  const [autoExplain, setAutoExplain] = useState(true);
  // v0.10.172 — serbest soru sınıflandırıcısı kipi (eksik ⇒ on_no_loop).
  const [intentClassify, setIntentClassify] = useState<AIIntentClassify>('on_no_loop');
  // v0.10.176 — model profilleri (dilim B); eski form varsayılan profili düzenler.
  const [profiles, setProfiles] = useState<AIProfilesPayload | null>(null);
  // v0.9.1120 (Faz 0.5) — LLM call tuning. Held as STRINGS, not
  // numbers, and that is the whole design:
  //
  // The backend returns OVERRIDES. maxTokens 0 / temperature null /
  // timeoutS 0 all mean "no override — running the built-in default"
  // (4096 / 0.2 / 180s). A number-typed state would have to pick some
  // value for "unset", and every candidate is wrong: 0 renders a
  // nonsense "0" in the box, and the default renders a value the
  // operator never chose — which the next Save would then WRITE into
  // the blob, freezing today's default forever and silently opting
  // this install out of any future default change.
  //
  // '' is the only honest representation of unset, so the inputs are
  // strings, the defaults appear as PLACEHOLDER text, and blank round-
  // trips back to 0 / null = reset.
  const [maxTokens, setMaxTokens] = useState('');
  const [temperature, setTemperature] = useState('');
  const [timeoutS, setTimeoutS] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Çeviri aiTuning.ts'te ve TABLO-TESTLİ: "boş kutu = sıfırla"
  // sözleşmesinin yanlış dalı sessiz ayar donmasına yol açıyor, o
  // yüzden bileşenin içinde satır içi kalmıyor.
  const [defaultModels, setDefaultModels] = useState<Partial<Record<AIProvider, string>>>({});
  const applyTuning = (s: Partial<AISettings>) => {
    const f = tuningToForm(s);
    setMaxTokens(f.maxTokens);
    setTemperature(f.temperature);
    setTimeoutS(f.timeoutS);
  };

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getAISettings(),
    s => {
      setDefaultModels(s.defaultModel ?? {});
      setProvider(s.provider || 'anthropic');
      setModel(s.model || '');
      setBaseUrl(s.baseUrl || '');
      setHasKey(s.hasKey);
      setSkipTls(s.skipTls ?? false);
      setEnabled(s.enabled ?? true);
      setAutoExplain(s.autoExplain ?? true);
      setIntentClassify(s.intentClassify ?? 'on_no_loop');
      if (s.profiles) setProfiles({ profiles: s.profiles, defaultProfile: s.defaultProfile ?? '', surfaceMap: s.surfaceMap ?? {} });
      applyTuning(s);
    },
  );

  // tuning — the knob triple EVERY PUT must carry, including the
  // Remove-key path. The PUT body is a whole-blob replace, so a call
  // that omits these resets the operator's overrides as a side effect
  // of an unrelated action ("I removed the key and my timeout went
  // back to 180s" — invisible until the next slow local generation).
  const tuning = () => tuningToWire({ maxTokens, temperature, timeoutS });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next = await api.putAISettings({ provider, apiKey, model, baseUrl, skipTls, enabled, autoExplain, intentClassify, ...tuning() });
      setHasKey(next.hasKey);
      setSkipTls(next.skipTls ?? false);
      setEnabled(next.enabled ?? true);
      // Echo the SERVER's view back: it is the authority on what the
      // stored override now is, and a rejected/normalised value must
      // not linger in the box looking saved.
      applyTuning(next);
      setApiKey('');
      setMsg({
        kind: 'ok',
        text: !next.enabled
          ? (next.hasKey ? 'Saved — CoSRE disabled (key kept).' : 'Saved — CoSRE disabled.')
          : (next.hasKey || (provider === 'openai' && baseUrl) ? 'Saved — CoSRE is live.' : 'Saved — CoSRE dormant (no key).'),
      });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  const clearKey = async () => {
    if (!await confirm({
      title: 'Kayıtlı API anahtarı silinsin mi?',
      body: <>Anahtar sunucudan kaldırılacak ve <b>CoSRE düğmeleri</b> yeni bir
        anahtar girilene kadar tüm yüzeylerden kaybolacak.</>,
      confirmLabel: 'Anahtarı sil',
      danger: true,
    })) return;
    setBusy(true); setMsg(null);
    try {
      const next = await api.putAISettings({ provider, apiKey: '', clearKey: true, model, baseUrl, skipTls, enabled, autoExplain, intentClassify, ...tuning() });
      setHasKey(next.hasKey);
      setSkipTls(next.skipTls ?? false);
      setEnabled(next.enabled ?? true);
      applyTuning(next);
      setApiKey('');
      setMsg({ kind: 'ok', text: 'Key cleared — CoSRE is dormant.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Clear failed' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  // Per-provider hint shown under the key field — explains where to
  // get the token + what shape it has, so users don't paste the wrong
  // thing.
  const keyHint = provider === 'github' ? (
    <>
      Paste a GitHub OAuth token with Copilot access (starts with{' '}
      <code className={CODE}>ghu_</code>).
      You can copy it from{' '}
      <code className={CODE}>~/.config/github-copilot/hosts.json</code>{' '}
      or run your own OAuth flow. Coremetry exchanges it for a Copilot session token automatically.
    </>
  ) : provider === 'openai' ? (
    <>
      Drives any OpenAI-compatible <code className={CODE}>/v1/chat/completions</code> endpoint —
      real OpenAI, Ollama, LM Studio, vLLM, llama.cpp server, LocalAI, OpenWebUI.
      Set <b>Base URL</b> below to your endpoint (e.g. <code>http://ollama:11434/v1</code>).
      API key is optional for local endpoints that don't gate on it (Ollama default).
    </>
  ) : (
    <>
      Paste your Anthropic API key (starts with{' '}
      <code className={CODE}>sk-ant-</code>).
      Get one at{' '}
      <a href="https://console.anthropic.com/settings/keys" target="_blank" rel="noopener"
         className="text-(--accent2)">console.anthropic.com</a>.
    </>
  );

  // v0.10.253 (prompt audit D5) — varsayılan model etiketi SUNUCUDAN
  // (/api/settings/ai defaultModel); literal yalnız yükleme öncesi.
  const modelPlaceholder =
    provider === 'github' ? `${defaultModels.github ?? 'gpt-4o'} (default)` :
    provider === 'openai' ? `${defaultModels.openai ?? 'gpt-4o-mini'} / llama3.1 / qwen2.5-coder …` :
    `${defaultModels.anthropic ?? 'claude-sonnet-4-6'} (default)`;

  const providerLabel =
    provider === 'github' ? 'GitHub Copilot' :
    provider === 'openai' ? 'OpenAI-compatible' :
    'Anthropic';

  return (
    // v0.10.178 — kök 1100: profil tablosu (6 sütun + eylemler) 640'ta sıkışıyordu
    // (176 canlı görüntüsü: «PRO… S… MO…»); metin/form 640'lık iç sargıda kalır.
    <div className="max-w-[1100px]">
      <div className="max-w-[640px]">
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">CoSRE</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Inline natural-language explanations for traces, Problems and exceptions.
        Pick a provider, paste your key, save — buttons appear automatically on the
        trace detail page and the Problems table. The OpenAI-compatible provider
        targets self-hosted local LLMs (Ollama / LM Studio / vLLM …) so traces
        never leave your perimeter.
      </p>

      {(() => {
        // Live state in three tiers: configured-and-enabled (active),
        // configured-but-disabled (creds kept, AI off), or not
        // configured. wf: the disabled tier is the whole point of the
        // toggle — show it distinctly so the operator sees AI is off
        // without thinking the key was lost.
        // v0.10.929 (K5) — üç kademe de bir ayar durumu: nötr bant; ayrım etikette.
        const configured = hasKey || (provider === 'openai' && !!baseUrl);
        const active = configured && enabled;
        const label = active ? 'ACTIVE' : configured ? 'DISABLED' : 'NOT CONFIGURED';
        return (
          <ConfigStatusBanner label={label}>
            {active
              ? (hasKey
                  ? `Provider: ${providerLabel} — ready.`
                  : `Provider: ${providerLabel} (no auth) — ready at ${baseUrl}.`)
              : configured
                ? `Provider: ${providerLabel} — credentials kept, CoSRE turned off.`
                : 'Not configured. Paste a key (or set a local endpoint URL) below.'}
          </ConfigStatusBanner>
        );
      })()}

      </div>
      {profiles && profiles.profiles.length > 0 && (
        <AiProfilesPanel payload={profiles} onChange={p => {
          setProfiles(p);
          // varsayılan değişince eski form da o profili göstersin (sunucu düz alanları = varsayılan)
          const d = p.profiles.find(x => x.id === p.defaultProfile);
          if (d) { setProvider(d.provider); setModel(d.model ?? ''); setBaseUrl(d.baseUrl ?? ''); setHasKey(d.hasKey); setSkipTls(!!d.skipTls); }
        }} />
      )}
      <AiBudgetPanel />
      <AiChatRetentionPanel />
      <AiGeneralQuestionsPanel />
      <div className="max-w-[640px]">
      <form onSubmit={save} className="mt-[18px] flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4">
        {/* Master on/off toggle (wf). Disabling stops the background
            problem-explainer, hides the in-app AI affordances, and
            503s the AI endpoints — all WITHOUT touching the stored
            key, so re-enabling is one click. Same checkbox markup as
            Skip-TLS below so the controls read as one family. */}
        <CheckboxField label="Enable CoSRE" checked={enabled} onCheckedChange={setEnabled}
          hint={<>Master switch. Uncheck + Save to turn CoSRE off
            without removing the stored key — the background
            problem-explainer stops, the ✨ Explain buttons hide,
            and AI endpoints return 503. Re-check + Save to resume.</>} />

        {/* v0.9.1138 — otomatik açıklayıcı vidası. Master açıkken bile
            arka plan worker'ları (problem/exception auto-explain)
            ayrı kapatılabilir: ücretli sağlayıcıda AI'yı açmak anında
            otomatik LLM harcaması başlatıyordu (canlı kanıt bulgusu).
            Tıklamalı ✨ yüzeyleri bu vidadan ETKİLENMEZ. */}
        <CheckboxField label="Otomatik açıklamalar" checked={autoExplain} onCheckedChange={setAutoExplain}
          hint={<>Arka plan worker'ları açık problem ve exception'lara
            kendiliğinden özet üretir (Inbox'taki pasif ✨ satırları).
            Kapatınca yalnız bu otomatik harcama durur; ✨ Explain
            düğmeleri ve chat çalışmaya devam eder.</>} />
        {/* v0.10.172 — serbest soru sınıflandırıcısı. Deterministik kılavuza
            oturmayan soruyu küçük model tek JSON çağrısıyla kılavuz niyetine
            eşler; none'da döngü yerine genel bilgi cevabı + öneri çipleri
            (v0.10.194; yerel küçük model). */}
        <SelectField label="Serbest soru sınıflandırıcısı (sohbet)" value={intentClassify} onChange={e => setIntentClassify(e.target.value as AIIntentClassify)}
          hint={<>
            Kılavuz kalıplarına uymayan soruyu model yalnız <em>sınıflandırır</em> (tek kısa JSON çağrısı, ~1 s);
            cevap yine sunucunun topladığı kanıtla üretilir. Adım panelinde «niyet» rozeti olarak görünür.
            Hiçbir kalıba oturmayan soru (v0.10.194) araçsız tek çağrıyla genel bilgiyle cevaplanır ve
            cevabın başında «telemetriyle eşleşmedi» notu bulunur — sayılar operatörün verisinden gelmez.
          </>}>
          <option value="on_no_loop">Açık — eşleşmezse genel bilgiyle cevaplar + öneri çipleri, araç döngüsü yok (yerel küçük model için önerilen)</option>
          <option value="on">Açık — eşleşmezse serbest araç döngüsü (frontier model)</option>
          <option value="off">Kapalı — eski davranış</option>
        </SelectField>

        <SelectField label="Provider" value={provider} onChange={e => setProvider(e.target.value as AIProvider)}>
          <option value="anthropic">Anthropic (Claude)</option>
          <option value="github">GitHub Copilot</option>
          <option value="openai">OpenAI-compatible (Ollama / LM Studio / vLLM / OpenAI)</option>
        </SelectField>

        {/* Base URL — only meaningful for the openai provider. The
            field is rendered for all providers but the openai branch
            is the only one that consumes it server-side; harmless
            otherwise (saved + ignored). Keeps the form layout
            stable when switching providers. */}
        {provider === 'openai' && (
          <Field label="Base URL" value={baseUrl} onChange={e => setBaseUrl(e.target.value)}
            placeholder="http://ollama:11434/v1   (or https://api.openai.com/v1 for real OpenAI)"
            autoComplete="off" controlClassName="font-mono"
            hint={<>
              Endpoint must serve <code>/chat/completions</code> in OpenAI's request shape.
              Common paths: Ollama → <code>http://&lt;host&gt;:11434/v1</code>,
              LM Studio → <code>http://&lt;host&gt;:1234/v1</code>,
              vLLM → <code>http://&lt;host&gt;:8000/v1</code>.
            </>} />
        )}

        {/* TLS verification toggle (v0.5.360). Matches the same
            opt-in pattern the Tempo + LDAP integrations expose
            for self-hosted endpoints fronted by an internal CA
            Go's default trust store doesn't know about. Off by
            default — operator flips it deliberately. */}
        <CheckboxField label="Skip TLS verification" checked={skipTls} onCheckedChange={setSkipTls}
          hint={<>Disables certificate verification on the outbound HTTPS
            call to the AI provider. Useful for self-hosted LLMs
            behind an enterprise CA. Leave off for public endpoints
            (Anthropic, GitHub Copilot, OpenAI).</>} />

        <Field label="API key" type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
          labelAside={(hasKey || provider === 'openai') ? (
            <span className="text-[length:var(--fs-sm)] text-(--text3)">
              {hasKey && '(saved — leave empty to keep current)'}
              {provider === 'openai' && ' (optional for local endpoints)'}
            </span>
          ) : undefined}
          placeholder={hasKey ? '••••••••••••••••' :
            provider === 'github' ? 'ghu_…' :
            provider === 'openai' ? 'sk-… (optional)' : 'sk-ant-…'}
          autoComplete="off" hint={keyHint} />

        <Field label="Model (optional)" value={model} onChange={e => setModel(e.target.value)}
          placeholder={modelPlaceholder} />

        {/* v0.9.1120 (Faz 0.5) — call tuning. Every box is an OVERRIDE:
            the built-in default lives in the placeholder, so an empty
            box is not "missing config", it is "use the default". Clear
            a box + Save = reset that knob. Bounds are enforced
            server-side (copilot.ValidateTuning) and repeated in the
            hint so a rejected save is predictable, not a surprise. */}
        <div className="text-[length:var(--fs-sm)] font-semibold text-(--text2)">
          Call tuning <span className="font-normal text-(--text3)">
            — leave a box empty to use the built-in default
          </span>
        </div>

        <Field label="Max tokens (optional)" type="number" inputMode="numeric" min={256} max={32768} step={256}
          value={maxTokens} onChange={e => setMaxTokens(e.target.value)} placeholder="4096 (default)"
          hint={<>Completion budget per call. Range <b>256–32768</b>. Too low and
            explanations get truncated mid-sentence; too high and a chatty
            model burns quota on every ✨ button.</>} />

        <Field label="Temperature (optional)" type="number" inputMode="decimal" min={0} max={2} step={0.1}
          value={temperature} onChange={e => setTemperature(e.target.value)} placeholder="0.2 (default)"
          hint={<>Range <b>0–2</b>. Applies to OpenAI-compatible and GitHub
            endpoints; strict-JSON surfaces on OpenAI-compatible endpoints
            always run at 0. Anthropic requests never carry temperature
            (current Claude models reject sampling parameters), so this
            field has no effect on Anthropic profiles.</>} />

        <Field label="Request timeout (seconds, optional)" type="number" inputMode="numeric" min={10} max={600} step={10}
          value={timeoutS} onChange={e => setTimeoutS(e.target.value)} placeholder="180 (default)"
          hint={<>Range <b>10–600</b>. A local LLM loading a large model cold can
            need 60s+ for a first generation. Above your reverse proxy /
            ingress timeout the value is a lie — the hop in front of
            Coremetry cuts the request first.</>} />

        {/* v0.10.974 deseni — FlashBox: hata role=alert, başarı role=status (eskiden satır içi kutu). */}
        {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

        {/* Save is actionable whenever there's something to persist:
            a new key, an already-stored key, or an openai endpoint
            with no key. The last clause keeps the Enable toggle
            actionable on a no-auth-local install.
            v0.9.1006 (M4/O6) — dolu Save'in yanında ikinci dolgu
            olmasın; kırmızı dil `ghost-danger`la korunuyor. */}
        <ActionRow
          destructive={hasKey ? (
            <Button variant="ghost-danger" onClick={clearKey} disabled={busy}>Remove key</Button>
          ) : undefined}
          confirm={
            <Button type="submit"
              disabled={!apiKey && !hasKey && !(provider === 'openai' && !!baseUrl)} loading={busy}>
              Save
            </Button>
          } />
      </form>

      {hasKey && (
        <div className="mt-[18px] rounded-lg border border-(--border) bg-(--bg2) p-4">
          <h3 className="mb-2 text-[length:var(--fs-md)] font-semibold">What it does</h3>
          <ul className="list-disc pl-[18px] text-[length:var(--fs-md)] leading-[1.7] text-(--text)">
            <li><b><IconSparkles /> Ask CoSRE</b> — on any trace detail page.</li>
            <li><b><IconSparkles /></b> column on the <Link to="/problems" className="text-(--accent2)">Problems</Link> page —
              plain-language meaning + ranked likely causes + first three things to check.</li>
          </ul>
        </div>
      )}
      </div>
    </div>
  );
}


// RAG / doküman soru-cevap bölümü v0.8.491'de kendi sekmesine taşındı:
// pages/settings/KnowledgeTab.tsx (/settings/knowledge).
