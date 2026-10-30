import { useId, useMemo, useState, type FormEvent, type ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { Combobox } from '@/components/Combobox';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay';
import { api } from '@/lib/api';
import type { NotificationChannel, ChannelType, NotifyKind } from '@/lib/types';
import { NOTIFY_KIND_OPTIONS, normalizeKinds, toggleKind } from '@/lib/notifyKinds';
import { FlashBox, humanize } from './shared';
import { ZoomChannelPicker } from './ZoomChannelPicker';
import { useServicesMetadata } from '@/lib/queries';
import { teamOptionsCI } from '@/lib/teamOptions';

// Faz 2 dilim 1 R9 (D8): elle yazılmış sabit perdeli kutu → coremetry/overlay
// Dialog — role=dialog + başlık, × kapat, odak tuzağı, kaydırma kilidi, Esc
// lib/escLayer yığınından (iç içe ZoomChannelPicker önce kapanır). Ortada
// diyalog + aynı alan sırası (plan Q-IA3); coremetry/Field ailesi, inline
// stil yok. Secret sözleşmesi: ChannelModal.secret.test.tsx.
//
// Combobox'ın öneri listesi (PickerPopover) body'ye PORTAL edilir, yani
// Radix'in gözünde diyaloğun DIŞI: (1) o listeye basmak "dışarı tık" sayılıp
// diyaloğu kapatmasın diye dışarı-etkileşimi `.pick-pop` için yutuyoruz;
// (2) modal diyalog body'ye pointer-events:none basar → bu diyalog açıkken
// `.pick-pop` pointer-events:auto alır (globals.css `data-cm-picker-host` kuralı).
const ROW = 'mb-3 flex flex-wrap items-start gap-3';
const LABEL = 'mb-1 text-[length:var(--fs-sm)] leading-tight text-(--text2)';
const NOTE = 'mt-1 text-[length:var(--fs-xs)] leading-normal text-(--text3)';
const CODE = 'font-mono';

function keepPickerClicks(e: { target: EventTarget | null; preventDefault(): void }) {
  if (e.target instanceof Element && e.target.closest('.pick-pop')) e.preventDefault();
}

/** Combobox atomu id almıyor: R7 emsali, etiket onu SARAR (label ↔ girdi bağı). */
function LabeledCombobox({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="mb-3 block">
      <div className={LABEL}>{label}</div>
      {children}
    </label>
  );
}

export function ChannelModal({ initial, onClose, onSaved }: {
  initial: NotificationChannel | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const kindsLabelId = useId();
  const [name, setName] = useState(initial?.name ?? '');
  const [type, setType] = useState<ChannelType>(initial?.type ?? 'email');
  const [recipients, setRecipients] = useState((initial?.config.recipients ?? []).join(', '));
  const [webhookUrl, setWebhookUrl] = useState(initial?.config.webhookUrl ?? '');
  const [url, setUrl] = useState(initial?.config.url ?? '');
  // v0.8.445 — webhook headers (satır başına "Ad: değer"; kayıtlı
  // değerler güvenlik gereği geri gelmez — boş bırakılan korunur) +
  // opsiyonel Go-template gövde.
  const [webhookHeaders, setWebhookHeaders] = useState('');
  const [webhookTemplate, setWebhookTemplate] = useState(initial?.config.bodyTemplate ?? '');
  // Zoom Chat Server-to-Server OAuth fields. clientSecret is
  // write-only — the GET endpoint never echoes it back, so the
  // initial value is always empty. Existing channels keep their
  // secret intact unless the operator types a replacement.
  const [zoomAccountId, setZoomAccountId] = useState(initial?.config.accountId ?? '');
  const [zoomClientId, setZoomClientId] = useState(initial?.config.clientId ?? '');
  const [zoomClientSecret, setZoomClientSecret] = useState('');
  const [zoomChannelId, setZoomChannelId] = useState(initial?.config.channelId ?? '');
  const [zoomToContact, setZoomToContact] = useState(initial?.config.toContact ?? '');
  // Optional proxy / sandbox host overrides. Empty → public
  // Zoom defaults (api.zoom.us + zoom.us). Banks routing
  // outbound traffic through a corporate gateway fill these.
  const [zoomAPIBaseURL, setZoomAPIBaseURL] = useState(initial?.config.apiBaseUrl ?? '');
  const [zoomOAuthBaseURL, setZoomOAuthBaseURL] = useState(initial?.config.oauthBaseUrl ?? '');
  // TLS verification toggle — defaults to off (verify enabled).
  // Operators in corp networks where api.zoom.us is fronted by
  // a MITM proxy with a private CA can flip this on as a
  // workaround. Public Zoom traffic should always verify.
  const [zoomSkipVerify, setZoomSkipVerify] = useState(
    initial?.config.insecureSkipVerify ?? false);
  // WhatsApp / Twilio fields
  const [twilioSid, setTwilioSid] = useState(initial?.config.accountSid ?? '');
  const [twilioToken, setTwilioToken] = useState(initial?.config.authToken ?? '');
  const [waFrom, setWaFrom] = useState(initial?.config.from ?? '');
  const [waTo, setWaTo] = useState((initial?.config.to ?? []).join(', '));
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [minSeverity, setMinSeverity] = useState<'info' | 'warning' | 'critical'>(initial?.minSeverity ?? 'warning');
  // Routing predicates — comma-separated in the UI, parsed
  // into string arrays on save. Empty / blank inputs leave
  // the predicate unset so the channel stays a catch-all.
  const [matchServices, setMatchServices] = useState((initial?.matchRules?.services ?? []).join(', '));
  const [matchSREs, setMatchSREs] = useState((initial?.matchRules?.sreTeams ?? []).join(', '));
  const [matchOwners, setMatchOwners] = useState((initial?.matchRules?.ownerTeams ?? []).join(', '));
  const [matchClusters, setMatchClusters] = useState((initial?.matchRules?.clusters ?? []).join(', '));
  const [matchQuietHours, setMatchQuietHours] = useState(initial?.matchRules?.quietHours ?? '');
  const [matchQuietHoursTz, setMatchQuietHoursTz] = useState(initial?.matchRules?.quietHoursTz ?? '');
  // v0.9.828 — en düşük triyaj basamağı. Boş = hepsi (mevcut kanallar
  // aynen davranır; alan match_rules JSON'ında olmadığı için DDL yok).
  const [matchMinPriority, setMatchMinPriority] =
    useState<'' | 'P1' | 'P2' | 'P3'>(initial?.matchRules?.minPriority ?? '');
  // v0.10.747 — olay türü allow-list'i (problem/anomali/incident). Boş = hepsi
  // (mevcut kanallar aynen); JSON'da olmayan alan = süzgeç yok, DDL yok.
  const [matchKinds, setMatchKinds] = useState<NotifyKind[]>(normalizeKinds(initial?.matchRules?.kinds));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // v0.9.828 — takım adı ÖNERİLERİ. Katalog zaten yüklü (60 sn cache,
  // Settings'in başka sekmeleri de okuyor), yani ek istek yok.
  // teamOptionsCI harf-durumu varyantlarını tekilleştiriyor: aynı takım
  // "orionSY" ve "Orionsy" olarak iki kez listelenmesin (v0.8.330).
  const mdQ = useServicesMetadata();
  const teamOptions = useMemo(
    () => teamOptionsCI(Object.values(mdQ.data ?? {}).flatMap(m => [m.sreTeam, m.ownerTeam])),
    [mdQ.data]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      const config: NotificationChannel['config'] = {};
      if (type === 'email') {
        config.recipients = recipients.split(/[,;\s]+/).map(s => s.trim()).filter(Boolean);
        if (config.recipients.length === 0) throw new Error('At least one recipient is required');
      } else if (type === 'slack' || type === 'mattermost') {
        if (!webhookUrl) throw new Error(`${type === 'slack' ? 'Slack' : 'Mattermost'} webhook URL is required`);
        config.webhookUrl = webhookUrl;
      } else if (type === 'teams') {
        if (!webhookUrl) throw new Error('Microsoft Teams webhook URL is required');
        config.webhookUrl = webhookUrl;
      } else if (type === 'zoomchat') {
        if (!zoomAccountId.trim()) throw new Error('Zoom Account ID is required');
        if (!zoomClientId.trim()) throw new Error('Zoom Client ID is required');
        // Secret is required on a NEW channel; on edit, leaving it
        // blank means "keep the saved secret" — the server detects
        // that by passing through the existing value.
        if (!initial && !zoomClientSecret.trim()) {
          throw new Error('Zoom Client Secret is required');
        }
        if (!zoomChannelId.trim() && !zoomToContact.trim()) {
          throw new Error('Either a Channel ID or a contact email is required');
        }
        config.accountId = zoomAccountId.trim();
        config.clientId = zoomClientId.trim();
        if (zoomClientSecret.trim()) config.clientSecret = zoomClientSecret.trim();
        if (zoomChannelId.trim()) config.channelId = zoomChannelId.trim();
        if (zoomToContact.trim()) config.toContact = zoomToContact.trim();
        if (zoomAPIBaseURL.trim()) config.apiBaseUrl = zoomAPIBaseURL.trim();
        if (zoomOAuthBaseURL.trim()) config.oauthBaseUrl = zoomOAuthBaseURL.trim();
        if (zoomSkipVerify) config.insecureSkipVerify = true;
      } else if (type === 'webhook') {
        if (!url) throw new Error('Webhook URL is required');
        config.url = url;
        const hdrs: Record<string, string> = {};
        for (const line of webhookHeaders.split('\n')) {
          const t = line.trim();
          if (!t) continue;
          const i = t.indexOf(':');
          if (i <= 0) throw new Error(`Header satırı "Ad: değer" olmalı: ${t}`);
          hdrs[t.slice(0, i).trim()] = t.slice(i + 1).trim();
        }
        if (Object.keys(hdrs).length) config.headers = hdrs;
        if (webhookTemplate.trim()) config.bodyTemplate = webhookTemplate;
      } else if (type === 'whatsapp') {
        if (!twilioSid || !twilioToken) throw new Error('Twilio Account SID and Auth Token are required');
        if (!waFrom) throw new Error('Sender number (whatsapp:+E164) is required');
        const tos = waTo.split(/[,;\s]+/).map(s => s.trim()).filter(Boolean);
        if (tos.length === 0) throw new Error('At least one WhatsApp recipient is required');
        config.accountSid = twilioSid.trim();
        config.authToken = twilioToken.trim();
        config.from = waFrom.trim();
        config.to = tos;
      }
      const splitCSL = (s: string) =>
        s.split(/[,;\s]+/).map(x => x.trim()).filter(Boolean);
      const matchRules = {
        services:     splitCSL(matchServices),
        sreTeams:     splitCSL(matchSREs),
        ownerTeams:   splitCSL(matchOwners),
        clusters:     splitCSL(matchClusters),
        quietHours:   matchQuietHours.trim(),
        quietHoursTz: matchQuietHoursTz.trim(),
        minPriority:  matchMinPriority,
        kinds:        matchKinds,
      };
      const payload = { name, type, config, enabled, minSeverity, matchRules };
      if (initial) await api.updateChannel(initial.id, payload);
      else        await api.createChannel(payload);
      onSaved();
    } catch (err) {
      setError(humanize(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={o => { if (!o) onClose(); }}>
      {/* v0.9.988 (D6.2) — görüntü alanına kelepçeli genişlik + dikey
          taşma. Kanal formu en uzun diyalog (tip seçimi yeni alanlar
          açıyor); telefonda hem yanlardan taşıyor hem alt kısmı
          erişilemez kalıyordu. */}
      <DialogContent aria-describedby={undefined}
        onPointerDownOutside={keepPickerClicks} onInteractOutside={keepPickerClicks}
        className="block max-h-[calc(100vh-32px)] w-[min(460px,calc(100vw-32px))] max-w-none overflow-auto rounded-lg border-(--border) bg-(--bg2) p-6 sm:max-w-none" data-cm-picker-host="">
        <DialogHeader className="mb-3.5 pr-6">
          <DialogTitle className="text-[length:var(--fs-lg)] font-semibold">
            {initial ? `Edit channel — ${initial.name}` : 'New channel'}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={submit}>
          <Field className="mb-3" label="Name" required autoFocus value={name}
            onChange={e => setName(e.target.value)} />
          <div className={ROW}>
            <SelectField className="flex-1" label="Type" value={type} onChange={e => setType(e.target.value as ChannelType)}>
              <option value="email">Email</option>
              <option value="slack">Slack</option>
              <option value="mattermost">Mattermost</option>
              <option value="teams">Microsoft Teams</option>
              <option value="zoomchat">Zoom Chat</option>
              <option value="webhook">Webhook (generic JSON POST)</option>
              <option value="whatsapp">WhatsApp (via Twilio)</option>
            </SelectField>
            <SelectField className="flex-1" label="Min severity" value={minSeverity}
              onChange={e => setMinSeverity(e.target.value as 'info' | 'warning' | 'critical')}>
              <option value="info">Info (every problem)</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical only</option>
            </SelectField>
          </div>

          {type === 'email' && (
            <Field className="mb-3" label="Recipients (comma-separated)" required value={recipients}
              placeholder="oncall@acme.com, sre@acme.com" onChange={e => setRecipients(e.target.value)} />
          )}
          {type === 'slack' && (
            <Field className="mb-3" label="Slack incoming webhook URL" required value={webhookUrl}
              placeholder="https://hooks.slack.com/services/T.../B.../..." onChange={e => setWebhookUrl(e.target.value)} />
          )}
          {type === 'mattermost' && (
            <Field className="mb-3" label="Mattermost incoming webhook URL" required value={webhookUrl}
              placeholder="https://your-mattermost.example.com/hooks/..." onChange={e => setWebhookUrl(e.target.value)} />
          )}
          {type === 'teams' && (
            <Field className="mb-3" label="Microsoft Teams incoming webhook URL" required value={webhookUrl}
              placeholder="https://outlook.office.com/webhook/..." onChange={e => setWebhookUrl(e.target.value)} />
          )}
          {type === 'zoomchat' && (
            <>
              <div className="mb-2 rounded-sm border border-(--border) bg-(--bg2) px-2.5 py-2 text-[length:var(--fs-xs)] leading-relaxed text-(--text2)">
                Server-to-Server OAuth flow. Create a "Server-to-Server OAuth" app in the
                Zoom App Marketplace with the <code>chat_message:write:admin</code> scope,
                then paste its credentials below. Coremetry exchanges them for an access
                token (~1h cache) and posts to Zoom's REST API.
              </div>
              <div className={ROW}>
                <Field className="flex-1" label="Account ID" required value={zoomAccountId}
                  placeholder="ABC1234d-XYZ..." onChange={e => setZoomAccountId(e.target.value)} />
                <Field className="flex-1" label="Client ID" required value={zoomClientId}
                  placeholder="from the S2S OAuth app" onChange={e => setZoomClientId(e.target.value)} />
              </div>
              <Field className="mb-3"
                label={initial ? 'Client Secret (leave empty to keep saved value)' : 'Client Secret'}
                required={!initial} value={zoomClientSecret} type="password"
                placeholder={initial ? '•••••••• (unchanged)' : 'never echoed back after save'}
                onChange={e => setZoomClientSecret(e.target.value)} />
              <div className="mb-3 flex items-end gap-1.5">
                <Field className="min-w-0 flex-1" label="Channel ID (JID) — target chat channel" value={zoomChannelId}
                  placeholder='e.g. "1234567890abcdef@xmpp.zoom.us" — copy from Zoom channel info'
                  onChange={e => setZoomChannelId(e.target.value)} />
                <ZoomChannelPicker
                  existingChannelId={initial?.id}
                  accountId={zoomAccountId}
                  clientId={zoomClientId}
                  clientSecret={zoomClientSecret}
                  oauthBaseUrl={zoomOAuthBaseURL}
                  apiBaseUrl={zoomAPIBaseURL}
                  insecureSkipVerify={zoomSkipVerify}
                  onPick={jid => setZoomChannelId(jid)} />
              </div>
              <Field className="mb-3" label="Or DM contact email (fallback if Channel ID is empty)" value={zoomToContact} type="email"
                placeholder="oncall@example.com" onChange={e => setZoomToContact(e.target.value)} />
              {/* Optional API + OAuth host overrides — proxy /
                  sandbox use cases. Leave empty for public Zoom. */}
              <details className="mt-1">
                <summary className="cursor-pointer text-[length:var(--fs-sm)] text-(--text2)">
                  Advanced: proxy / sandbox host overrides
                </summary>
                <div className="pt-2">
                  <div className={ROW}>
                    <Field className="flex-1" label="API base URL (chat messages)" value={zoomAPIBaseURL}
                      placeholder="https://api.zoom.us (default)" onChange={e => setZoomAPIBaseURL(e.target.value)} />
                    <Field className="flex-1" label="OAuth base URL (token exchange)" value={zoomOAuthBaseURL}
                      placeholder="https://zoom.us (default)" onChange={e => setZoomOAuthBaseURL(e.target.value)} />
                  </div>
                  <div className={NOTE}>
                    Banks routing outbound traffic through a corporate gateway can point both
                    fields at their proxy. Endpoint paths stay the same
                    (<code>/v2/chat/users/me/messages</code> and <code>/oauth/token</code>) —
                    only the host changes. Leave empty to hit Zoom's public hosts.
                  </div>
                  <CheckboxField className="mt-3" checked={zoomSkipVerify} onCheckedChange={setZoomSkipVerify}
                    label={<span className="font-semibold">Skip TLS certificate verification</span>}
                    hint={<>
                      Disables certificate trust checks on the OAuth + chat calls. Turn this on
                      only when the corporate proxy fronting <code>api.zoom.us</code> terminates
                      TLS with a private CA the pod doesn't trust. Equivalent to <code>curl -k</code> —
                      public Zoom hosts should leave it off.
                    </>} />
                </div>
              </details>
              {/* Legacy webhook nudge — surfaces when editing a
                  channel that still carries the pre-v0.4.78 shape. */}
              {initial?.config.webhookUrl && !initial?.config.accountId && (
                <div className="mt-1 rounded-sm border border-status-warn-border bg-status-warn-bg px-2.5 py-1.5 text-[length:var(--fs-xs)] text-status-warn">
                  ⚠ This channel still uses the legacy webhook URL. Fill the fields
                  above and save to migrate; the webhook URL will be cleared.
                </div>
              )}
            </>
          )}
          {type === 'webhook' && (
            <>
              <Field className="mb-3" label="Webhook URL (raw Problem JSON is POSTed here)" required value={url}
                placeholder="https://your-receiver.example.com/incidents" onChange={e => setUrl(e.target.value)} />
              <TextareaField className="mb-3" controlClassName={CODE}
                label="Headers (opsiyonel — satır başına 'Ad: değer'; auth key'ler burada)"
                rows={2} value={webhookHeaders}
                placeholder={initial ? 'kayıtlı — boş bırakırsan korunur' : 'Authorization: Bearer sk-…'}
                onChange={e => setWebhookHeaders(e.target.value)} spellCheck={false} />
              <TextareaField className="mb-3" controlClassName={CODE}
                label={'Gövde şablonu (opsiyonel Go template — boş = varsayılan {problem, coremetryUrl} JSON)'}
                rows={4} value={webhookTemplate}
                placeholder={'{"service":"{{.Problem.Service}}","severity":"{{.Problem.Severity}}","url":"{{.CoremetryURL}}"}'}
                onChange={e => setWebhookTemplate(e.target.value)} spellCheck={false} />
            </>
          )}
          {type === 'whatsapp' && (
            <>
              <div className={ROW}>
                <Field className="flex-1" label="Twilio Account SID" required value={twilioSid}
                  placeholder="ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" onChange={e => setTwilioSid(e.target.value)} />
                <Field className="flex-1" label="Auth Token" required type="password" value={twilioToken}
                  placeholder="32-char Auth Token" onChange={e => setTwilioToken(e.target.value)} />
              </div>
              <Field className="mb-3" label="Sender number (with whatsapp: prefix)" required value={waFrom}
                placeholder="whatsapp:+14155238886 (Twilio sandbox) or your approved number" onChange={e => setWaFrom(e.target.value)} />
              <Field className="mb-3" label="Recipient numbers (comma-separated, E.164)" required value={waTo}
                placeholder="+905XXXXXXXXX, +1XXXXXXXXXX" onChange={e => setWaTo(e.target.value)} />
              <p className="-mt-1 text-[length:var(--fs-xs)] text-(--text3)">
                Twilio is the de-facto WhatsApp Business API broker. The sandbox lets you test for free
                (recipients must opt in by texting the join code). Production usage requires a Twilio-approved sender.
              </p>
            </>
          )}

          {/* Routing predicates — gate this channel to a
              subset of services / SRE teams / owner teams.
              Empty = catch-all; populated lists AND together
              with the existing severity threshold. Each
              channel can pin to a specific team's Zoom Chat
              while a "default" channel without rules still
              fires for everything. */}
          <details className="mt-4 text-[length:var(--fs-sm)] text-(--text2)">
            <summary className="cursor-pointer font-semibold">
              Routing rules (leave empty for catch-all)
            </summary>
            <div className="mt-2">
              <Field className="mb-3" label="Match services (comma-separated)" value={matchServices}
                placeholder="payments, order-service" onChange={e => setMatchServices(e.target.value)} />
              {/* v0.9.828 — takım alanları ZATEN çalışıyordu (sunucu
                  tarafında ChannelMatchRules.SRETeams/OwnerTeams +
                  MatchesProblem); eksik olan GÖRÜNÜRLÜKTÜ: operatör
                  katalogda hangi takım adlarının geçtiğini bilmeden
                  buraya bir şey yazamıyordu. Alanlar o adları öneriyor
                  — serbest metin olarak kalıyor, çünkü katalog henüz
                  doldurulmamış bir takım da yazılabilmeli.
                  v0.9.1020 — öneri kaynağı aynı (`teamOptions`), taşıyıcı
                  native <select> kardeşi olan öneri listesinden ev
                  Combobox'ına indi: native liste yalnız Chrome'da klavye
                  ile gezilebiliyordu, Firefox/Safari'de ok tuşları
                  listeyi açmıyordu bile. Serbest metin KORUNUYOR —
                  eşleşme yoksa yazılan değer olduğu gibi kalır. */}
              <LabeledCombobox label="Match SRE teams (comma-separated)">
                <Combobox value={matchSREs} onChange={setMatchSREs}
                  options={teamOptions}
                  placeholder="platform, sre-storefront" width="100%" />
              </LabeledCombobox>
              <LabeledCombobox label="Match owner teams (comma-separated)">
                <Combobox value={matchOwners} onChange={setMatchOwners}
                  options={teamOptions}
                  placeholder="payments, ml" width="100%" />
              </LabeledCombobox>
              <Field className="mb-3" label="Match k8s/openshift clusters (comma-separated)" value={matchClusters}
                placeholder="prod-eu-west, prod-eu-central" onChange={e => setMatchClusters(e.target.value)} />
              <div className="grid-2 mb-3 grid gap-2">
                <Field label="Quiet hours (HH:MM-HH:MM, may cross midnight)" value={matchQuietHours}
                  placeholder="22:00-07:00" onChange={e => setMatchQuietHours(e.target.value)} />
                <Field label="Quiet hours timezone (IANA, default UTC)" value={matchQuietHoursTz}
                  placeholder="Europe/Istanbul" onChange={e => setMatchQuietHoursTz(e.target.value)} />
              </div>
              {/* v0.9.828 — triyaj basamağı kapısı. minSeverity'nin
                  YANINDA, yerine değil: ciddiyet "ne kadar kötü",
                  öncelik "ne kadar acil" diyor. */}
              {/* v0.10.747 — olay türü süzgeci (operatör: "anomali, incident ve
                  problems ayrı ayrı gelsin"). Hiçbiri işaretli değilse hepsi.
                  Incident üreticisi v0.10.748. */}
              <div className="mb-3" role="group" aria-labelledby={kindsLabelId}>
                <div id={kindsLabelId} className={LABEL}>Olay türleri (boş = hepsi)</div>
                <div className="flex flex-wrap gap-3.5">
                  {NOTIFY_KIND_OPTIONS.map(o => (
                    <span key={o.value} title={o.hint}>
                      <CheckboxField checked={matchKinds.includes(o.value)}
                        onCheckedChange={() => setMatchKinds(k => toggleKind(k, o.value))}
                        label={o.label} />
                    </span>
                  ))}
                </div>
                <div className={NOTE}>
                  <b>Problem</b> = operatör kuralları (alert rule, builtin, SLO, DB,
                  runtime, watcher). <b>Anomali</b> = anomali motoru (metrik anomalisi,
                  service silent, dış tarayıcı, exception fırtınası / paylaşılan
                  bağımlılık). <b>Incident</b> = incident açılışı ve çözümü (otomatik
                  korelasyon + manuel; critical P1, warning P2). Türleri ayrı kanallara
                  ayırmak için her kanalda tek tür işaretleyin; hiçbiri işaretli değilse
                  kanal hepsini alır.
                </div>
              </div>
              <div className="mb-3">
                <SelectField label="En düşük öncelik (triyaj basamağı)" value={matchMinPriority}
                  onChange={e => setMatchMinPriority(e.target.value as '' | 'P1' | 'P2' | 'P3')}>
                  <option value="">Hepsi (süzgeç yok)</option>
                  <option value="P1">Yalnız P1 — şimdi</option>
                  <option value="P2">P2 ve üstü — bugün</option>
                  <option value="P3">P3 ve üstü — hepsi</option>
                </SelectField>
                <div className={NOTE}>
                  Kanal yalnız bu basamak <b>ve üstündeki</b> problemleri alır. Ciddiyet
                  süzgecinden ayrıdır: <b>ciddiyet</b> ne kadar kötü olduğunu,
                  <b> öncelik</b> ne kadar acil olduğunu söyler &mdash; bir critical
                  problem P2 olabilir, tamamen erişilemez bir monitör ise P1&apos;dir.
                  {/* DÜRÜSTLÜK NOTU — bu iki sınır gerçek ve operatör
                      kanal kurarken bilmeli; keşifte açıkça saptandı. */}
                  <div className="mt-1.5">
                    <b>Bilmeniz gereken iki sınır:</b>
                    <br />
                    &bull; Öncelik <b>bildirim anında</b> hesaplanır. Açılış anında bir
                    problem henüz 4 saattir açık olamayacağı için, P1 pratikte
                    &laquo;critical + eşiğin 2 katı&raquo; ya da &laquo;tamamen
                    kayıp&raquo; demektir.
                    <br />
                    &bull; Bir problem sonradan P2&apos;den P1&apos;e yükselirse
                    <b> yeniden bildirim gönderilmez</b>: tekrar yalnız <i>ciddiyet</i>
                    değiştiğinde tetiklenir. Yani P1 kanalı, açılışta P1 olmayan bir
                    problemi sonradan da almaz.
                  </div>
                </div>
              </div>
              <p className="mt-1.5 text-[length:var(--fs-xs)] text-(--text3)">
                Predicates AND together — every non-empty rule must match.
                e.g. services=<code>payments</code> +
                clusters=<code>prod-eu-west</code> +
                quietHours=<code>22:00-07:00</code> means "fire only on the
                payments service in eu-west, AND only outside the 10pm–7am
                window". Service catalog metadata is the source of truth for
                sreTeam / ownerTeam lookup; clusters come from the problem's
                enriched cluster list (k8s.cluster.name or
                openshift.cluster.name resource attrs).
              </p>
            </div>
          </details>

          <CheckboxField className="mt-1.5" checked={enabled} onCheckedChange={setEnabled} label="Enabled" />

          {error && <FlashBox kind="err">{error}</FlashBox>}
          <DialogFooter className="mt-[18px]">
            <Button variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={busy}>
              {busy ? 'Saving…' : initial ? 'Update' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
