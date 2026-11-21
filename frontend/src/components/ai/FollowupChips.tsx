// FollowupChips — v0.10.1291 (CoSRE follow-up D): cevap altındaki takip
// çipleri; CoSRE penceresi (CopilotChat) ve AI çekmecesi sohbeti
// (AIDrawerBody) AYNI bileşeni çizer.
//
// Öncelik: sunucunun kural-türevli `followups`'ı (araç sonuçlarından,
// internal/api/chat_followup_chips.go) → guided `suggestions` → yüzeyin statik
// listesi. Metin çipi tıklanınca metin yeni mesaj, `scope`/`command` yapısal
// kapsam olarak gider (ChatSendOpts.parsed — chatScope.ts). Aksiyon çipi
// (hipotezi doğrula) mesaj GÖNDERMEZ: mevcut verify çağrısı (editor+;
// sunucu viewer'a çipi hiç üretmez, uç ayrıca 403 döner).
//
// Öğrenme sinyali `followup_click` (cosrelearn, ağırlık 0.05): yalnız SUNUCU
// çiplerinde (followups / suggestions) — statik liste cevaba bağlı değildir.
// Sinyal yalnız ölçümdür; çalışma zamanı kararına okunmaz.
//
// v0.10.1515 (operatör: "Alttaki chipler çok çıkıyor, gerek yok servis servis
// yazmasına") — SEÇİCİ çipi (`pick`): takım cevabının iki genel çipi ("Bir
// servisin sağlığını analiz et", "Bir servisin exception'ları"). Tık mesaj
// GÖNDERMEZ; çiplerin altında satır içi sunucu-aramalı ServicePicker açılır
// (adaylar = cevabın servisleri, önce; arama tüm servislerde). Seçim şablonu
// doldurup cümleyi kapsam + seçilen servisle gönderir (followupPick.ts).
import { useState } from 'react';
import '@/styles/cosreChat.css';
import { Chip } from '@/components/ui';
import { ServicePicker } from '@/components/ServicePicker';
import { api } from '@/lib/api';
import type { ChatFollowup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';
import { followupSendOpts } from './followupSend';
import { fillPickTemplate, isServicePick, pickSendOpts } from './followupPick';
import { useSREHypothesisVerify } from './sreHypothesisVerify';
import { parseSREHypothesisAction, type SREHypothesisAction } from '@/lib/pageActions';
import { useNavigate } from 'react-router';
import { safeAppPath } from '@/lib/safeAppPath';
import { useChatLinkNewTab } from './chatLinkTarget';

export interface FollowupChipsProps {
  followups?: ChatFollowup[];
  suggestions?: string[];
  fallback: string[];
  /** son cevabın exchangeId'si (sinyal); yoksa sinyal gönderilmez */
  exchangeId?: string;
  disabled?: boolean;
  onAsk: (text: string, opts?: ChatSendOpts) => void;
}

function ActionChip({ action, disabled, onClicked }: { action: SREHypothesisAction; disabled?: boolean; onClicked: () => void }) {
  const { state, run } = useSREHypothesisVerify(action);
  return (
    <Chip pill disabled={disabled || state !== 'idle'} title="Doğrulama koşusunu kuyruğa al (problem sayfasındaki AI SRE kartı)"
      onClick={() => { onClicked(); void run(); }}>
      🔎 {state === 'done' ? 'Doğrulama kuyrukta' : action.label}
    </Chip>
  );
}

// LinkChip — v0.10.1331: link çipi ("<KOD> grubunu açıkla" → grubun Exceptions
// sayfası + AI açıklaması). Mesaj göndermez; yol isSafeAppPath'ten geçmiştir.
// /cosre (kromsuz) sayfada diğer sohbet linkleri gibi yeni sekmede açılır.
function LinkChip({ href, text, disabled, onClicked }: { href: string; text: string; disabled?: boolean; onClicked: () => void }) {
  const navigate = useNavigate();
  const newTab = useChatLinkNewTab();
  return (
    <Chip pill disabled={disabled} title={href}
      onClick={() => { onClicked(); if (newTab) window.open(href, '_blank', 'noopener,noreferrer'); else navigate(href); }}>
      ↗ {text}
    </Chip>
  );
}

// PickPanel — v0.10.1515: açık seçici çipinin satır içi servis seçicisi.
// Listeden seçim HEMEN gönderir; Enter'la yazılan ad aday listesinde ya da
// sunucuda BİREBİR varsa gönderir (örneklenmiş listeye karşı doğrulama yok,
// v0.8.265), yoksa kutuda kalır (liste açık: operatör seçer).
function PickPanel({ f, disabled, onPick }: { f: ChatFollowup; disabled?: boolean; onPick: (svc: string) => void }) {
  const [q, setQ] = useState('');
  const cands = f.pick?.services ?? [];
  const commit = (v?: string) => {
    const name = (v ?? q).trim();
    if (!name || disabled) return;
    if (v !== undefined || cands.includes(name)) { onPick(name); return; }
    api.serviceNames(name, 50)
      .then(r => { if (r.names.includes(name)) onPick(name); })
      .catch(() => undefined);
  };
  return (
    <div className="cm-followups__pick" data-fu-pick={f.kind}>
      <ServicePicker value={q} onChange={setQ} onEnter={commit} preferred={cands}
        placeholder="Servis ara…" width="100%" />
    </div>
  );
}

export function FollowupChips({ followups, suggestions, fallback, exchangeId, disabled, onAsk }: FollowupChipsProps) {
  const [openPick, setOpenPick] = useState<string | null>(null); // v0.10.1515 — açık seçici çipinin türü
  const signal = (detail: string) => { if (exchangeId) api.aiSignal(exchangeId, 'followup_click', detail); };
  const chip = (f: ChatFollowup) => {
    if (f.pick) {
      // v0.10.1515 — seçici çipi; tanınmayan yük → çip yok (etiketi soru değil, gönderilmez).
      if (!isServicePick(f)) return null;
      const open = openPick === f.kind;
      return (
        <Chip key={`p:${f.kind}`} pill disabled={disabled} data-fu-pick-chip={f.kind} aria-expanded={open}
          title="Servis seç — cümle o servisle gönderilir"
          onClick={() => setOpenPick(open ? null : f.kind)}>
          ↳ {f.label || f.text}
        </Chip>
      );
    }
    if (f.action) {
      // Aksiyon bloğuyla AYNI doğrulama (kapalı tür kümesi); bozuk yük → çip yok.
      const a = parseSREHypothesisAction(f.action);
      return a ? <ActionChip key={`a:${f.text}`} action={a} disabled={disabled} onClicked={() => signal(f.kind)} /> : null;
    }
    if (f.href !== undefined) {
      // v0.10.1331 — link çipi; güvensiz yol → çip yok (metin olarak da gönderilmez).
      const h = safeAppPath(f.href);
      return h ? <LinkChip key={`l:${f.text}`} href={h} text={f.text} disabled={disabled} onClicked={() => signal(f.kind)} /> : null;
    }
    // v0.10.1383 — kısa etiket (label) görünür, tam cümle (text) gönderilir + title'da.
    return (
      <Chip key={f.text} pill disabled={disabled} title={f.label ? f.text : undefined}
        onClick={() => { signal(f.kind); onAsk(f.text, followupSendOpts(f)); }}>
        {f.explainAsk ? '✨' : '↳'} {f.label || f.text}{/* v0.10.1400 — açıklama çipi sohbet içi tur */}
      </Chip>
    );
  };
  // v0.10.1406 — "Kodu da incele" (kind 'code') EK çiptir: açıklama turunun özne
  // çiplerinin ("Bu neden oluyor?" …) yerine geçmez, önlerine eklenir. Yalnız
  // kod çipi varsa aşağıdaki öneri/statik listesi de çizilir.
  const extra = followups?.filter(f => f.kind === 'code') ?? [];
  if (followups?.length && extra.length < followups.length) {
    const picked = followups.find(f => f.pick && f.kind === openPick && isServicePick(f));
    return (
      <>
        {followups.map(chip)}
        {picked && (
          <PickPanel key={picked.kind} f={picked} disabled={disabled} onPick={svc => {
            signal(picked.kind);
            setOpenPick(null);
            onAsk(fillPickTemplate(picked.pick!.template, svc), pickSendOpts(picked, svc));
          }} />
        )}
      </>
    );
  }
  const server = !!suggestions?.length;
  return (
    <>
      {extra.map(chip)}
      {(server ? suggestions ?? [] : fallback).map(q => (
        <Chip key={q} pill disabled={disabled} onClick={() => { if (server) signal('guided'); onAsk(q); }}>↳ {q}</Chip>
      ))}
    </>
  );
}
