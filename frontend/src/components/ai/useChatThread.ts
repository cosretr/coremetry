import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '@/lib/api';
import { appendChatBlock } from '@/lib/chatBlocks';
import type { AiConversation, PageContext } from '@/lib/types';
import type { ChatMessage, ChatTurn } from '@/lib/types';
import { capPageContext } from '@/lib/traceAiContext';
import { isAbortError, settleStoppedTurn, settleTruncatedTurn } from './chatAbort';
import { failedQuestion, dropFailedTail } from './chatRetry';
import { prevWikiRefs } from './chatWikiRefs';
import { parseChatInput, type ParsedChatInput } from './chatScope';
import { ChatRequestError } from '@/lib/api';
import { lastUserIndex, regenerateBase } from './chatRegenerate';
import {
  PERSIST_DEBOUNCE_MS, hasCompletedExchange, persistMessages, restoreTurns,
} from './chatPersist';

// useChatThread — sohbet turlarının state'i + gönderme döngüsü
// (v0.9.479). CopilotChat.tsx'in içinden çıkarıldı; AI çekmecesindeki
// sohbet AYNI çekirdeği kullanır (ikinci bir chat implementasyonu yok).
// Yüzeyler yalnız KABUKTA ayrışır: global pencere sağ-alt FAB + kendi
// drawer'ı, çekmece sohbeti ise açıklamanın altındaki bölüm.
//
// Her gönderimde tüm geçmiş /api/copilot/chat'e postlanır, SSE tüketilir.
//
// v0.9.1139 (Faz 4.1) — konuşma artık `persist: true` ile KALICI:
// tamamlanan her alışverişten sonra saved_views(page='ai-chat') satırına
// yazılır (POST /api/ai/conversations). Kimliği SUNUCU basar, istemci
// yanıttan devralır ve sonraki yazımlarda taşır.
//
// v0.10.55 (operatör ürün kararı) — kalıcılık artık AI çekmecesine de
// AÇIK: "Chat'te devam et" sohbeti de saved_views(page='ai-chat')
// satırına yazılıyor, aynı "🕘 Geçmiş" listesinde CoSRE penceresiyle
// yan yana görünür. v0.9.1139'daki "NPE fp-abc123 kabuğu" kaygısı iki
// yerde zaten karşılanıyor: (1) başlık ÖZNE kodeğinden değil, ekrandaki
// gerçek ilk kullanıcı mesajından türüyor (resolveChatTitle, backend) —
// çekmece çağrısı ayrıca AÇIK bir `title` de basıyor (AIDrawer.tsx,
// aiSubjectTitle+aiSubjectSubtitle) ki thread listede "Explain trace ·
// a1b2c3d4…" gibi tanınabilir dursun; (2) tek-mesajlık boş açıklama
// turları zaten arşive girmiyor — hasCompletedExchange en az bir takip
// sorusu + tamamlanmış cevap şart koşuyor.
//
// Kaydetme HİÇBİR ZAMAN gönderme yolunu bloklamaz: ateşle-ve-unut,
// hatası yutulur (sonraki tur zaten tüm geçmişi yeniden yazacak).

export interface ChatThreadOpts {
  /** v0.10.434 (D7b) — sunucu `open` verdiğinde (uygulama-içi href) çağrılır. */
  onOpen?: (href: string) => void;
  // Context-awareness (v0.9.164/184) — bulunulan sayfanın servisi ve
  // seçili operasyonu; sunucudaki guided router varsayılan alır.
  service?: string;
  operation?: string;
  // explain (v0.9.479) — AI çekmecesindeki açıklamanın metni. Sunucu
  // narration bloğuna katar; boşken tüm davranış global chat'inkiyle
  // aynıdır (internal/api/copilot_drawer.go).
  explain?: string;
  // subject (v0.9.482) — çekmecenin öznesi, `?ai=` kodeği biçiminde
  // (formatAiParam). Sunucu bundan ilgili explain'in HAM KANITINI
  // (trace span'leri + ilişkili loglar / exception paketi) yeniden kurar;
  // açıklamanın metni takip sorularına yetmiyordu (operatör raporu).
  // Global sohbette YOKTUR — orada guided/tool yolları veriyi kendi çeker.
  subject?: string;
  // seed (v0.9.479) — tele giden ama EKRANDA ÇİZİLMEYEN ön turlar.
  // Çekmece, öznesinin doğal sorusunu buraya koyar: sunucudaki
  // takip-devralma (v0.9.410) onu "önceki soru" olarak görür, böylece
  // "peki hata logları?" gibi takipler guided router'da servise oturur.
  seed?: ChatMessage[];
  // rangeS (v0.9.529) — EKRANDAKİ zaman aralığı, saniye. Soru açık bir
  // pencere taşımıyorsa sunucu sabit 30dk yerine bunu kullanır; soru
  // pencere taşıyorsa ("son 24 saatte…") soru kazanır.
  rangeS?: number;
  // toMs (v0.10.33) — MUTLAK pencerenin bitiş anı; yalnız custom/zoom
  // aralıkta dolu. Göreli aralıkta boş bırakılıyor ki sunucu şimdiye
  // çapalasın — sabitlemek cevabı dondururdu.
  toMs?: number;
  // trace (v0.9.537) — EKRANDAKİ trace ID'si (/trace?id=). "bu trace
  // neden yavaş" gibi ID'siz sorular sunucuda buna oturur.
  trace?: string;
  // env (v0.9.1259) — Topbar'daki global env seçimi (rangeS aynası).
  env?: string;
  // persist (v0.9.1139) — konuşma sunucuda saklansın mı. Global CoSRE
  // penceresi ve (v0.10.55'ten beri) AI çekmecesi ikisi de true.
  persist?: boolean;
  // title (v0.10.55) — kalıcı satır için AÇIK başlık. Boşsa backend ilk
  // kullanıcı mesajından türetir (global CoSRE'nin bugüne kadarki
  // davranışı); çekmece bunu ÖZNEYE göre basıyor (aiSubjectTitle +
  // aiSubjectSubtitle) ki "Geçmiş" listesinde hangi trace/span/problem
  // olduğu takip sorusunun lafından değil, açıklamanın öznesinden okunsun.
  title?: string;
  /** v0.10.183 — istek başına model profili; boş = sunucu varsayılanı / yüzey eşlemesi */
  profile?: string;
  /** v0.10.539 — sayfa bağlamı (lib/pageContext, her turda) ve sabitlenmiş bağlam. */
  page?: PageContext;
  pinnedPage?: PageContext;
  // persistContext (v0.10.944, CoSRE Faz A) — konuşmayla SAKLANAN bağlam
  // anlık görüntüsü (≤2 KB, capPageContext). Çekmece trace öznesinde
  // trace/span/servis/env/cluster/namespace/pod + pencereyi koyar; geçmişten
  // yeniden açılınca şerit bunu gösterir. Boşsa sunucu satırdaki mevcut
  // görüntüyü KORUR (ai_conversations.go) — gönderilmemesi silmek değildir.
  persistContext?: PageContext;
  /**
   * v0.10.1138 — sunucu açık profil seçimini reddetti (400 profile_unknown /
   * 403 profile_forbidden). Kabuk seçimi varsayılana döndürür; tur hatası
   * "yeniden dene" ile varsayılan modelle tekrar gönderilir.
   */
  onProfileRejected?: (profile: string, code: string) => void;
}

/** v0.10.1138 — send'in isteğe bağlı ikinci argümanı. */
export interface ChatSendOpts {
  /** composer tamamlamasından SEÇİLEN servis adları (anma kapsamı, chatScope.ts). */
  chosen?: readonly string[];
  /** hazır çözülmüş kapsam/komut (yeniden üret: AYNI kapsam). */
  parsed?: ParsedChatInput;
  /** yeni cevabın taşıyacağı önceki cevaplar (yeniden üret). */
  alternatives?: ChatTurn[];
}

export function useChatThread(opts: ChatThreadOpts = {}) {
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [busy, setBusy] = useState(false);
  // send stabil kimlikli (useCallback []) — güncel seçenekleri/turları
  // ref üstünden okur, böylece her render'da yeniden kurulmaz ve
  // CopilotChat'in `coremetry:ai-ask` köprüsü bayat closure çağırmaz.
  const optsRef = useRef(opts);
  optsRef.current = opts;
  const turnsRef = useRef(turns);
  turnsRef.current = turns;
  const busyRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);
  // v0.10.664 — akarken gelen soru: mevcut akış durdurulur, soru kuyruğa
  // alınır, finally gönderir ("durdur ve gönder"; operatörün yeni sorusu
  // her zaman kazanır). sendRef: finally içinden güncel send'e ulaşmak için.
  const queuedRef = useRef<{ q: string; so?: ChatSendOpts } | null>(null);
  const sendRef = useRef<(text: string, so?: ChatSendOpts) => Promise<void>>(async () => {});
  // Kalıcılık (v0.9.1139). conversationId state OLARAK da tutuluyor ki
  // kabuk "kayıtlı thread" bilgisini çizebilsin; yazım yolu ref'i okur
  // (bayat closure yok).
  const [conversationId, setConversationId] = useState<string | null>(null);
  const convIdRef = useRef<string | null>(null);
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => {
    abortRef.current?.abort();
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
  }, []);

  // schedulePersist — ATEŞLE VE UNUT. `send` bunu await ETMEZ ve
  // etmemeli: kaydetme, cevabın gösterilmesiyle ya da sıradaki soruyla
  // aynı yolda değil. Hata sessiz yutuluyor — sonraki tur tüm geçmişi
  // yeniden yazacağı için kayıp kendini onarır; bir toast, çalışan
  // sohbetin ortasında yanlış alarm olurdu.
  //
  // v0.10.944 — kaydetme gövdesi `saveNow`a ayrıldı: anlık görüntü + kimlik
  // ÇAĞRI ANINDA okunur, yanıtın kimliği yalnız arada konuşma DEĞİŞMEDİYSE
  // (genRef: adopt/clear her geçişte artırır) devralınır. Eskiden adopt
  // bekleyen zamanlayıcıyı yalnız iptal ediyordu: çekmecenin devralma
  // effect'i akış bitince (busy=false) koşup send'in finally'de kurduğu
  // 600 ms'lik kaydı siliyordu → önceki konuşmanın SON alışverişi hiç
  // yazılmıyordu. Artık geçişte bekleyen kayıt İPTAL değil, hemen yazılır.
  const genRef = useRef(0);
  const saveNow = useCallback((): Promise<string | null> => {
    const snapshot = turnsRef.current;
    if (!hasCompletedExchange(snapshot)) return Promise.resolve(convIdRef.current);
    const gen = genRef.current;
    return api.saveAiConversation({
      id: convIdRef.current ?? undefined,
      title: optsRef.current.title || undefined,
      subject: optsRef.current.subject || undefined,
      context: capPageContext(optsRef.current.persistContext) ?? undefined, // v0.10.944
      messages: persistMessages(snapshot),
    }).then(c => {
      // Arada başka konuşmaya geçildiyse (adopt/clear) yeni kimlik korunur.
      if (gen !== genRef.current) return c.id;
      // Kimliği SUNUCU basar. Aynı thread'e yazmaya devam etmek için
      // yanıttan devralıyoruz — silinmiş bir thread'e yazım sunucuda
      // YENİ kimlikle açılır ve o kimlik de buradan devralınır.
      convIdRef.current = c.id;
      setConversationId(c.id);
      return c.id;
    }).catch(() => convIdRef.current /* sessiz — sonraki tur yeniden dener */);
  }, []);

  // v0.10.1138 — "Tam sayfada aç": bekleyen (debounce) kayıt hemen yazılır ve
  // konuşma kimliği döner; tamamlanmış alışveriş yoksa mevcut kimlik (ya da null).
  const flushSave = useCallback((): Promise<string | null> => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    if (!optsRef.current.persist) return Promise.resolve(convIdRef.current);
    return saveNow();
  }, [saveNow]);

  const schedulePersist = useCallback(() => {
    if (!optsRef.current.persist) return;
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    saveTimerRef.current = setTimeout(() => { saveTimerRef.current = null; void saveNow(); }, PERSIST_DEBOUNCE_MS);
  }, [saveNow]);

  const send = useCallback(async (text: string, so?: ChatSendOpts) => {
    const q = text.trim();
    if (!q) return;
    if (busyRef.current) {
      // v0.10.664 — sessizce düşürme YOK: durdur ve kuyruğa al.
      queuedRef.current = { q, so };
      abortRef.current?.abort();
      return;
    }
    const o = optsRef.current;
    // v0.10.1138 — @-anmalar / komut → yapısal kapsam. Yoksa ikisi de undefined
    // ve istek gövdesi bayt bayt eski (chatScope.ts).
    const parsed = so?.parsed ?? parseChatInput(q, so?.chosen ?? []);
    // v0.10.1134 — önceki cevabın wiki sayfaları (geçmiş yalnız {role,text}).
    const wikiRefs = prevWikiRefs(turnsRef.current);
    const history: ChatMessage[] = [
      ...(o.seed ?? []),
      // v0.10.63 — DURDURULAN TUR YARIM OLDUĞUNU SÖYLER.
      //
      // ⚠ `stopped` bayrağı burada DÜŞÜYORDU: yalnız {role,text} geçiyor.
      // Yani operatörün yarıda kestiği cevap modele TAMAMLANMIŞ kendi
      // cevabı gibi geri gidiyor ve model kendi yarım cümlesinin üstüne
      // inşa ediyordu — "bir önceki cevabımda dediğim gibi…" diye devam
      // ettiği şey hiç söylenmemiş olabiliyor.
      //
      // Tur ELENMİYOR: operatör ona atıfta bulunabilir ("az önceki listeyi
      // tamamla"). Elenirse o atıf bağlamsız kalırdı. Yarımlık METNE
      // yazılıyor, ki model neye baktığını bilsin.
      ...turnsRef.current.filter(t => !t.error).map(t => ({
        role: t.role,
        text: t.stopped ? `${t.text}\n\n[Bu cevap operatör tarafından YARIDA DURDURULDU — tamamlanmadı.]` : t.text,
      })),
      { role: 'user', text: q },
    ];
    setTurns(prev => [
      ...prev,
      { role: 'user', text: q, ...(parsed.scope ? { scope: parsed.scope } : {}), ...(parsed.command ? { command: parsed.command } : {}) },
      { role: 'assistant', text: '', steps: [], pending: true, ...(so?.alternatives?.length ? { alternatives: so.alternatives } : {}) },
    ]);
    busyRef.current = true;
    setBusy(true);
    const ac = new AbortController();
    abortRef.current = ac;

    const patchLast = (fn: (t: ChatTurn) => ChatTurn) =>
      setTurns(prev => prev.map((t, i) => (i === prev.length - 1 ? fn(t) : t)));

    try {
      await api.copilotChat(history, (e) => {
        if (e.kind === 'step') {
          // v0.9.1181 (Faz 4.3) — etiket ile detay AYRI birikiyor. Detay
          // burada `preview`siz açılıyor: çip tool çalışmadan ÖNCE
          // görünmeli (ilerleme geri bildirimi), veri sonra doluyor.
          patchLast(t => ({
            ...t,
            // v0.10.161 — etiket adımı (tool yok) çipte etiketiyle görünür;
            // detayda tool '' kalır → şeffaflık paneli o satırı çizmez/saymaz.
            steps: [...(t.steps ?? []), e.tool ?? e.label ?? ''],
            stepDetails: e.i == null
              ? t.stepDetails
              : [...(t.stepDetails ?? []), { i: e.i, tool: e.tool ?? '', label: e.label, args: e.args, origin: e.origin }],
          }));
        } else if (e.kind === 'step-result') {
          // Sonuç, `i` ile kendi çipine yazılır. Eşleşme bulunamazsa
          // SESSİZCE düşer — eski bir sunucuya karşı akan bir FE'de
          // (rolling deploy) `step` olayı `i` taşımaz ve o çip detaysız,
          // yani tıklanamaz kalır: eksik affordance, kırık affordance'tan
          // iyidir.
          patchLast(t => ({
            ...t,
            stepDetails: (t.stepDetails ?? []).map(d =>
              d.i === e.i
                ? { ...d, ok: e.ok, preview: e.preview, truncated: e.truncated, bytes: e.bytes, href: e.href, durationMs: e.durationMs, skipped: e.skipped || undefined, sources: e.sources } // v0.10.944 — tam çıktıdan kaynak durumu
                : d),
          }));
        } else if (e.kind === 'block') {
          // v0.10.541 — tipli blok (chart/link/…): metinden bağımsız biriktirilir.
          patchLast(t => ({ ...t, blocks: appendChatBlock(t.blocks, { id: e.id, type: e.type, seq: e.seq, final: e.final, payload: e.payload }) }));
        } else if (e.kind === 'delta') {
          patchLast(t => ({ ...t, text: (t.text ?? '') + e.text }));
        } else if (e.kind === 'answer') {
          patchLast(t => ({ ...t, text: e.text, exchangeId: e.exchangeId, sources: e.sources, suggestions: e.suggestions, links: e.links, allowedLinks: e.allowedLinks, pending: false })); // v0.10.1137 — link doğrulama listesi
          // v0.10.434 (D7b) — yalnız uygulama-içi (kök-göreli) href; dış adres asla.
          if (e.open && e.open.startsWith('/') && !e.open.startsWith('//')) o.onOpen?.(e.open);
        } else if (e.kind === 'error') {
          patchLast(t => ({ ...t, error: e.error, pending: false }));
        } else if (e.kind === 'done') {
          patchLast(t => ({ ...t, pending: false }));
        }
      }, ac.signal, o.service || undefined, o.operation || undefined, o.explain || undefined,
        o.subject || undefined, o.rangeS || undefined, o.trace || undefined, o.env || undefined,
        o.toMs || undefined, o.profile || undefined, // v0.10.183 — model profili
        convIdRef.current || undefined, // v0.10.478 — konuşma kimliği (sunucu bağlam state'i)
        o.page || undefined, o.pinnedPage || undefined, // v0.10.539 — sayfa bağlamı + pin
        wikiRefs.length > 0 ? wikiRefs : undefined, // v0.10.1134 — wiki takip sorusu
        parsed.scope, parsed.command); // v0.10.1138 — yapısal @-kapsam + /komut
      // v0.10.648 — terminal olaysız EOF: tur asılı kalmasın (chatAbort.ts).
      patchLast(settleTruncatedTurn);
    } catch (err) {
      // v0.10.23 — İPTAL ARIZA DEĞİL. Durdurulan bir fetch AbortError
      // fırlatıyor; ayırmazsak operatörün kasıtlı eylemi kırmızı bir
      // "⚠ signal is aborted" balonuna dönüşür, yani düzeltme yeni bir
      // kusur üretir. Akan metin KORUNUYOR (chatAbort.ts).
      if (isAbortError(err)) {
        patchLast(settleStoppedTurn);
      } else if (err instanceof ChatRequestError && o.profile && (err.code === 'profile_forbidden' || err.code === 'profile_unknown')) {
        // v0.10.1138 — seçili profil reddedildi: kabuk seçimi varsayılana
        // döndürür; "Yeniden dene" aynı soruyu varsayılan modelle gönderir.
        o.onProfileRejected?.(o.profile, err.code);
        patchLast(t => ({
          ...t, pending: false,
          error: err.code === 'profile_forbidden'
            ? 'Seçili model bu rol için kapalı — varsayılan modele dönüldü. "Yeniden dene" ile tekrar sor.'
            : 'Seçili model profili artık yok — varsayılan modele dönüldü. "Yeniden dene" ile tekrar sor.',
        }));
      } else {
        patchLast(t => ({ ...t, error: err instanceof Error ? err.message : String(err), pending: false }));
      }
    } finally {
      busyRef.current = false;
      setBusy(false);
      abortRef.current = null;
      // TEK tetikleyici: bir alışveriş bitti (cevap ya da hata). Hata
      // turu persistMessages'ta düşer, yani hatalı bir tur arşivi
      // kirletmez ama ondan ÖNCEKİ turlar korunur.
      schedulePersist();
      // v0.10.664 — kuyruktaki soru (akarken gelen) şimdi gider.
      const next = queuedRef.current;
      if (next) {
        queuedRef.current = null;
        void sendRef.current(next.q, next.so);
      }
    }
  }, [schedulePersist]);
  sendRef.current = send;

  // clear = YENİ KONUŞMA (v0.9.1139'da anlamı genişledi). Turların
  // yanında kalıcı kimlik de düşer: aksi hâlde "Temizle" sonrası ilk
  // yazım, arşivdeki dolu thread'in ÜSTÜNE boş/yeni bir gövde yazardı.
  // Bekleyen bir kaydetme de iptal edilir (o zamanlayıcı silinmiş
  // turları yazmak üzereydi). v0.10.944 — kuşak da artar: uçuştaki bir
  // kayıt "Temizle" sonrası eski kimliği geri takmasın.
  const clear = useCallback(() => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    genRef.current++;
    setTurns([]);
    convIdRef.current = null;
    setConversationId(null);
  }, []);

  // load — arşivden bir thread'i ekrana getirir. Akış sürerken
  // yüklemiyoruz: yarı yazılmış bir cevabın üstüne başka bir konuşmayı
  // basmak, gelen SSE parçalarının yanlış thread'e yapışması demekti.
  //
  // v0.10.944 — iki yarıya ayrıldı: `adopt` elde OLAN bir konuşmayı ekrana
  // koyar (çekmece, geçmişten açılan özneli thread'i kabuktan hazır alır —
  // ikinci okuma yok); `load` okur + adopt eder ve konuşmayı DÖNDÜRÜR ki
  // çağıran öznesini/bağlamını da kullanabilsin. Akış sürerken ikisi de
  // reddeder (false/null).
  // v0.10.944 — geçişte bekleyen kayıt İPTAL edilmez, önceki konuşmaya
  // HEMEN yazılır (saveNow kimliği/turları şimdi okur); sonra kuşak artar ki
  // o kaydın yanıtı yeni konuşmanın kimliğini ezmesin. load ayrıca iptal
  // etmiyor: adopt'un boşaltması tek yol.
  const adopt = useCallback((c: AiConversation): boolean => {
    if (busyRef.current) return false;
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
      void saveNow();
    }
    genRef.current++;
    const restored = restoreTurns(c.messages);
    turnsRef.current = restored;
    setTurns(restored);
    convIdRef.current = c.id;
    setConversationId(c.id);
    return true;
  }, [saveNow]);

  const load = useCallback(async (id: string): Promise<AiConversation | null> => {
    if (busyRef.current) return null;
    const c = await api.aiConversation(id);
    return adopt(c) ? c : null;
  }, [adopt]);

  // Takip çipleri yalnız son tur TAMAMLANMIŞ bir asistan cevabıysa görünür.
  const last = turns[turns.length - 1];
  const showFollowups = !busy && !!last && last.role === 'assistant' && !last.pending && !last.error && !!last.text;

  // v0.10.23 — İPTAL AFFORDANCE'I. AbortController zaten kuruluydu ama
  // yalnız unmount'ta ateşleniyordu ve CopilotChat AppShell'de KALICI
  // monte, yani çekmeceyi kapatmak bile akışı durdurmuyordu. Tek GPU'da
  // istenmeyen bir 5-turlu döngü, sıradaki meşru soruyu dakikalarca
  // tıkıyordu.
  // v0.10.650 — hata sonrası yeniden dene: başarısız kuyruk (soru + hatalı
  // cevap) hem state'ten hem turnsRef'ten düşer, sonra aynı soru gider.
  // turnsRef senkron kırpılmazsa send geçmişi soruyu iki kez taşırdı.
  const retry = useCallback(() => {
    const q = failedQuestion(turnsRef.current);
    if (!q || busyRef.current) return;
    const trimmed = dropFailedTail(turnsRef.current);
    turnsRef.current = trimmed;
    setTurns(trimmed);
    void send(q);
  }, [send]);

  const stop = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  // v0.10.1138 — "↻ Yeniden üret": SON asistan cevabı aynı kullanıcı turuyla
  // (aynı geçmiş, aynı kapsam/komut) yeniden istenir ve yerine geçer; eski
  // cevap `alternatives`te kalır ("önceki cevap (1/2)"). Yeni istek yeni
  // exchangeId alır — geri bildirim her cevabın kendi kimliğine yazılır.
  // Akarken çalışmaz. Kalıcı arşiv send'in finally'sindeki kayıtla görünen
  // cevabı yazar (upsert tam transkripti değiştirir).
  const regenerate = useCallback(() => {
    if (busyRef.current) return;
    const r = regenerateBase(turnsRef.current);
    if (!r) return;
    turnsRef.current = r.base;
    setTurns(r.base);
    void send(r.question, { parsed: r.parsed, alternatives: r.alternatives });
  }, [send]);

  // v0.10.1138 — son kullanıcı mesajını düzenle: o mesaj ve SONRASI düşer,
  // düzenlenmiş metin aynı yoldan yeniden gönderilir. Arşiv bir sonraki
  // kayıtta kırpılmış + yeniden koşulmuş transkripti yazar (upsert tam
  // mesaj listesini DEĞİŞTİRİR, eklemez — ai_conversations.go).
  const editLast = useCallback((text: string, chosen?: readonly string[]): boolean => {
    const q = text.trim();
    if (!q || busyRef.current) return false;
    const ts = turnsRef.current;
    const i = lastUserIndex(ts);
    if (i < 0) return false;
    const base = ts.slice(0, i);
    turnsRef.current = base;
    setTurns(base);
    void send(q, { chosen: [...(ts[i].scope?.services ?? []), ...(chosen ?? [])] });
    return true;
  }, [send]);

  return { turns, busy, send, retry, regenerate, editLast, stop, clear, load, adopt, flushSave, conversationId, last, showFollowups };
}
