import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '@/lib/api';
import { appendChatBlock } from '@/lib/chatBlocks';
import { isSafeAppPath } from '@/lib/safeAppPath';
import type { AiConversation, ChatAttempt, ChatExplainAsk, ChatSendWindow, PageContext } from '@/lib/types';
import type { ChatMessage, ChatTurn } from '@/lib/types';
import { isAbortError, settleStoppedTurn, settleTruncatedTurn } from './chatAbort';
import { failedQuestion, dropFailedTail } from './chatRetry';
import { prevWikiRefs } from './chatWikiRefs';
import { applyChatDelta } from './phaseTrail';
import { withArrival } from './revealSchedule'; // v0.10.1384 — büyük parça varış damgası (yazma efekti)
import { hasDrawerContext, parseChatInput, pickedServices, scopeForRequest, type ParsedChatInput } from './chatScope';
import { ChatRequestError } from '@/lib/api';
import { lastUserIndex, regenerateBase } from './chatRegenerate';
import { withoutFresh } from './explainTurn'; // v0.10.1406 — fresh yalnız istekte
import { codeLockedOf } from './codeRead'; // v0.10.1465 — konuşmanın kod kilidi
import {
  PERSIST_DEBOUNCE_MS, hasCompletedExchange, persistMessages, restoreTurns,
} from './chatPersist';

// useChatThread — sohbet turlarının state'i + gönderme döngüsü
// (v0.9.479). CopilotChat.tsx'in içinden çıkarıldı; tek tüketicisi
// CopilotChat (v0.10.1407 çekmece özne kipini sildi — ikinci sohbet yok).
//
// Her gönderimde tüm geçmiş /api/copilot/chat'e postlanır, SSE tüketilir.
//
// v0.9.1139 (Faz 4.1) — konuşma artık `persist: true` ile KALICI:
// tamamlanan her alışverişten sonra saved_views(page='ai-chat') satırına
// yazılır (POST /api/ai/conversations). Kimliği SUNUCU basar, istemci
// yanıttan devralır ve sonraki yazımlarda taşır. Başlık ekrandaki ilk
// kullanıcı mesajından türer (resolveChatTitle, backend); tek-mesajlık boş
// turlar arşive girmez (hasCompletedExchange).
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
  // subject (v0.9.482) — açıklanan özne, `?ai=` kodeği biçiminde
  // (formatAiParam). Sunucu bundan ilgili explain'in HAM KANITINI
  // (trace span'leri + ilişkili loglar / exception paketi) yeniden kurar;
  // açıklamanın metni takip sorularına yetmiyordu (operatör raporu).
  // v0.10.1400'den beri yalnız resolveOpts (son açıklama turu) doldurur.
  subject?: string;
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
  // persist (v0.9.1139) — konuşma sunucuda saklansın mı (CoSRE penceresi: true).
  persist?: boolean;
  /** v0.10.183 — istek başına model profili; boş = sunucu varsayılanı / yüzey eşlemesi */
  profile?: string;
  /** v0.10.1150 — "Derin düşün" (composer hapı, deepThink.ts); açıkken context.deep: true. */
  deep?: boolean;
  /** v0.10.1461 — "Kod" hapı (codeRead.ts); açıkken context.code: true (bağımsız sohbet kod turu). */
  code?: boolean;
  /** v0.10.539 — sayfa bağlamı (lib/pageContext, her turda) ve sabitlenmiş bağlam. */
  page?: PageContext;
  pinnedPage?: PageContext;
  /**
   * v0.10.1138 — sunucu açık profil seçimini reddetti (400 profile_unknown /
   * 403 profile_forbidden). Kabuk seçimi varsayılana döndürür; tur hatası
   * "yeniden dene" ile varsayılan modelle tekrar gönderilir.
   */
  onProfileRejected?: (profile: string, code: string) => void;
  /**
   * v0.10.1400 — gönderim ANINDA turlardan çözülen ek bağlam (CoSRE penceresi:
   * son açıklama turunun takip bağlamı — explain / subject / trace / page).
   * Sonuç seçeneklerin ÜSTÜNE biner; yalnız istek gövdesine girer (kalıcı
   * satırın öznesi/başlığı değişmez). Yokken davranış bayt bayt eski.
   */
  resolveOpts?: (turns: readonly ChatTurn[]) => Partial<ChatThreadOpts>;
}

/** v0.10.1138 — send'in isteğe bağlı ikinci argümanı. */
export interface ChatSendOpts {
  /** composer tamamlamasından SEÇİLEN servis adları (anma kapsamı, chatScope.ts). */
  chosen?: readonly string[];
  /** hazır çözülmüş kapsam/komut (yeniden üret: AYNI kapsam). */
  parsed?: ParsedChatInput;
  /** yeni cevabın taşıyacağı önceki cevaplar (yeniden üret). */
  alternatives?: ChatTurn[];
  /** CoSRE öğrenme Faz 1 — sunucuya giden deneme beyanı (yeni soru = yok). */
  attempt?: ChatAttempt;
  /**
   * v0.10.1400 — yapısal açıklama niyeti (trace / exception ✨ sohbet içinde;
   * components/ai/explainTurn.ts). Kullanıcı turu niyeti taşır (yeniden dene /
   * üret aynısını yollar); bekleyen cevap baştan açıklama turu olarak çizilir.
   */
  explainAsk?: ChatExplainAsk;
  /**
   * v0.10.1405 — bu gönderimin SABİT penceresi (endpoint "CoSRE'ye sor":
   * düğmeye basılan pencere). rangeS/toMs'i yalnız bu istek için ezer;
   * kullanıcı turunda durur, yeniden dene / üret aynısını yollar.
   */
  window?: ChatSendWindow;
  /** v0.10.2466 — "daha uzun bekle" çipi: bu tur okumaları uzun bütçeyle (yalnız kullanıcı evet'i). */
  longWait?: boolean;
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
  // (genRef: load/clear her geçişte artırır) devralınır. Geçişte bekleyen
  // kayıt İPTAL değil, hemen yazılır (önceki konuşmanın son alışverişi).
  const genRef = useRef(0);
  // v0.10.1139 — strict: hata yutulmaz, reddedilir ("Tam sayfada aç" kayıt
  // başarısızken gezinmemeli). Arka plan kaydı (schedulePersist) yine sessiz.
  const saveNow = useCallback((strict = false): Promise<string | null> => {
    const snapshot = turnsRef.current;
    if (!hasCompletedExchange(snapshot)) return Promise.resolve(convIdRef.current);
    const gen = genRef.current;
    return api.saveAiConversation({
      id: convIdRef.current ?? undefined,
      messages: persistMessages(snapshot),
    }).then(c => {
      // Arada başka konuşmaya geçildiyse (load/clear) yeni kimlik korunur.
      if (gen !== genRef.current) return c.id;
      // Kimliği SUNUCU basar. Aynı thread'e yazmaya devam etmek için
      // yanıttan devralıyoruz — silinmiş bir thread'e yazım sunucuda
      // YENİ kimlikle açılır ve o kimlik de buradan devralınır.
      convIdRef.current = c.id;
      setConversationId(c.id);
      return c.id;
    }).catch((err: unknown) => {
      if (strict) throw err;
      return convIdRef.current; /* sessiz — sonraki tur yeniden dener */
    });
  }, []);

  // v0.10.1138 — "Tam sayfada aç": bekleyen (debounce) kayıt hemen yazılır ve
  // konuşma kimliği döner; tamamlanmış alışveriş yoksa mevcut kimlik (ya da null).
  // v0.10.1139 — kayıt hatası REDDEDİLİR (çağıran gezinmez, hatayı gösterir).
  const flushSave = useCallback((): Promise<string | null> => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    if (!optsRef.current.persist) return Promise.resolve(convIdRef.current);
    return saveNow(true);
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
    const o0 = optsRef.current;
    const o1: ChatThreadOpts = o0.resolveOpts ? { ...o0, ...o0.resolveOpts(turnsRef.current) } : o0; // v0.10.1400
    const o: ChatThreadOpts = so?.window ? { ...o1, rangeS: so.window.rangeS, toMs: so.window.toMs } : o1; // v0.10.1405
    // v0.10.1138 — @-anmalar / komut → yapısal kapsam. Yoksa ikisi de undefined
    // ve istek gövdesi bayt bayt eski (chatScope.ts).
    // v0.10.1139 — çekmece bağlamında (explain/subject/trace/sayfa traceId)
    // @-kapsamı düşer, açık /komut kalır (chatScope.scopeForRequest).
    const parsed = scopeForRequest(so?.parsed ?? parseChatInput(q, so?.chosen ?? []), hasDrawerContext(o));
    // v0.10.1134 — önceki cevabın wiki sayfaları (geçmiş yalnız {role,text}).
    const wikiRefs = prevWikiRefs(turnsRef.current);
    const history: ChatMessage[] = [
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
    // v0.10.1465 — kod okunmuş konuşma kilitli: hap ne olursa olsun kod turu + ipucu.
    const codeLocked = codeLockedOf(turnsRef.current);
    // v0.10.1406 — kullanıcı turu niyeti `fresh`SİZ saklar (yalnız bu istek taze).
    const keptAsk = so?.explainAsk ? withoutFresh(so.explainAsk) : undefined;
    setTurns(prev => [
      ...prev,
      { role: 'user', text: q, ...(parsed.scope ? { scope: parsed.scope } : {}), ...(parsed.command ? { command: parsed.command } : {}), ...(keptAsk ? { explainAsk: keptAsk } : {}), ...(so?.window ? { window: so.window } : {}) },
      // v0.10.1400 — açıklama turu: geçici künye (tür + kimlik) → balon baştan ExplainBody; answer gerçeğiyle değiştirir.
      { role: 'assistant', text: '', steps: [], pending: true, ...(so?.alternatives?.length ? { alternatives: so.alternatives } : {}), ...(o.deep ? { deep: true } : {}), ...(so?.explainAsk ? { explain: { kind: so.explainAsk.kind, id: so.explainAsk.id } } : {}) },
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
              : [...(t.stepDetails ?? []), { i: e.i, tool: e.tool ?? '', label: e.label, args: e.args, origin: e.origin, ...(e.phase ? { phase: e.phase } : {}) }], // v0.10.1273 — evre
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
                ? { ...d, ok: e.ok, preview: e.preview, truncated: e.truncated, bytes: e.bytes, href: e.href, durationMs: e.durationMs, skipped: e.skipped || undefined, sources: e.sources, evidence: e.evidence, evidenceList: e.evidenceList, ctxApplied: e.ctxApplied } // v0.10.1274 — ekran kapsamı künyesi; v0.10.944 — tam çıktıdan kaynak durumu; v0.10.1272 — kanıt çipi; v0.10.1275 — hop kanıt listesi
                : d),
          }));
        } else if (e.kind === 'block') {
          // v0.10.541 — tipli blok (chart/link/…): metinden bağımsız biriktirilir.
          patchLast(t => ({ ...t, blocks: appendChatBlock(t.blocks, { id: e.id, type: e.type, seq: e.seq, final: e.final, payload: e.payload }) }));
        } else if (e.kind === 'delta') {
          // v0.10.1273 — `reset`: sunucu o ana dek akanı geri aldı (tool'lu turun
          // önsözü / düşünce bloğu); metin yeniden başlar. `answer` yine otoriter.
          // v0.10.1384 — ≥240 karakterlik tek delta varış damgası alır (yazma efekti); token akışı almaz.
          patchLast(t => withArrival(t, applyChatDelta(t.text, e), false));
        } else if (e.kind === 'answer') {
          // v0.10.1384 — metin withArrival'dan: öncesinde akış yoksa tüm cevap varış damgası alır.
          patchLast(t => ({ ...withArrival(t, e.text, true), exchangeId: e.exchangeId, turnId: e.turnId, sources: e.sources, suggestions: e.suggestions, followups: e.followups, links: e.links, allowedLinks: e.allowedLinks, evidence: e.evidence, explain: e.explain, pending: false })); // v0.10.1444 — turnId (arşiv kimliği); v0.10.1400 — açıklama künyesi (yoksa düz cevap); v0.10.1137 — link doğrulama listesi; v0.10.1272 — kanıt çipleri; v0.10.1291 — kural-türevli takip çipleri
          // v0.10.434 (D7b) — yalnız uygulama-içi (kök-göreli) href; dış adres asla.
          // v0.10.1314 — kapı isSafeAppPath ("/\evil.com" react-router v6'da dışarı açılırdı).
          if (e.open && isSafeAppPath(e.open)) o.onOpen?.(e.open);
        } else if (e.kind === 'code-lock') {
          // v0.10.1465 — konuşma kilitlendi (kod okundu); arşive asistan turuyla gider.
          if (e.locked) patchLast(t => ({ ...t, codeRead: true }));
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
        parsed.scope, parsed.command, // v0.10.1138 — yapısal @-kapsam + /komut
        o.deep || undefined, // v0.10.1150 — Derin düşün (kapalıyken gövdede yok)
        so?.attempt, // Faz 1 — retry/regenerate/edit beyanı
        so?.explainAsk, // v0.10.1400 — yapısal açıklama niyeti
        (o.code || codeLocked) || undefined, // v0.10.1461 — Kod hapı (kapalıyken gövdede yok); v0.10.1465 — kilitli konuşmada açık
        codeLocked || undefined, // v0.10.1465 — kod kilidi ipucu (yalnız sıkılaştırır)
        so?.longWait || undefined); // v0.10.2466 — "daha uzun bekle" evet'i
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
  // basmak, gelen SSE parçalarının yanlış thread'e yapışması demekti
  // (okuma sırasında akış başladıysa da yüklenmez).
  // v0.10.944 — geçişte bekleyen kayıt İPTAL edilmez, önceki konuşmaya
  // HEMEN yazılır (saveNow kimliği/turları şimdi okur); sonra kuşak artar ki
  // o kaydın yanıtı yeni konuşmanın kimliğini ezmesin.
  const load = useCallback(async (id: string): Promise<boolean> => {
    if (busyRef.current) return false;
    const c: AiConversation = await api.aiConversation(id);
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
    const prevAsk = turnsRef.current[turnsRef.current.length - 2]?.explainAsk; // v0.10.1400 — aynı niyet
    const ask = prevAsk ? withoutFresh(prevAsk) : undefined; // v0.10.1406 — yeniden dene önbelleği atlamaz
    const win = turnsRef.current[turnsRef.current.length - 2]?.window; // v0.10.1405 — aynı pencere
    const trimmed = dropFailedTail(turnsRef.current);
    turnsRef.current = trimmed;
    setTurns(trimmed);
    void send(q, { attempt: 'retry', ...(ask ? { explainAsk: ask } : {}), ...(win ? { window: win } : {}) });
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
    // v0.10.1400 — açıklama turu aynı niyetle. v0.10.1406 — `fresh: true`: önbellek
    // okunmaz, taze cevap üretilir ve satıra yazılır (eskiden ↻ önbellek isabeti
    // veriyordu — aynı cevabı "yeniden üretmek"). Yeniden dene taze DEĞİL.
    const prevAsk = turnsRef.current[turnsRef.current.length - 2]?.explainAsk;
    const ask = prevAsk ? { ...prevAsk, fresh: true } : undefined;
    const win = turnsRef.current[turnsRef.current.length - 2]?.window; // v0.10.1405 — aynı pencere
    turnsRef.current = r.base;
    setTurns(r.base);
    void send(r.question, { parsed: r.parsed, alternatives: r.alternatives, attempt: 'regenerate', ...(ask ? { explainAsk: ask } : {}), ...(win ? { window: win } : {}) });
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
    // v0.10.1139 — yalnız AÇIKÇA seçilenler devralınır; biçim-eşleşmesi yine biçim-eşleşmesi.
    void send(q, { chosen: [...pickedServices(ts[i].scope), ...(chosen ?? [])], attempt: 'edit' });
    return true;
  }, [send]);

  const codeLocked = codeLockedOf(turns); // v0.10.1465 — composer hapının kilidi
  return { turns, busy, send, retry, regenerate, editLast, stop, clear, load, flushSave, conversationId, last, showFollowups, codeLocked };
}
