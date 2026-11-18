// parts/strings — v0.10.2363 (S5). Kanıt kartlarının TÜRKÇE metni; ../strings'in
// yanında ama ayrı modül: kartlarla birlikte tembel chunk'a iner, v2 sohbet
// chunk'ını büyütmez.
/** v0.10.2363 (investigation-design S5) — kanıt kartları (parts/); yalnız yükün sayıları. */
export const EVIDENCE_PART_STRINGS = {
  exceptionCount: (n: string) => `${n} kez`,
  firstSeen: 'ilk',
  lastSeen: 'son',
  framesLabel: 'İlk çerçeveler',
  sampleTrace: 'Örnek trace',
  open: 'Sayfada aç ↗',
  traceSpans: (n: string) => `${n} span`,
  traceErrorSpan: 'Hatalı span',
  logsTotal: (n: string) => `${n} kayıt`,
  logsShown: (n: number) => `ilk ${n} satır`,
  logsTrace: 'trace',
  anomalyObserved: 'gözlenen',
  anomalyBaseline: 'taban',
  anomalyStarted: 'başladı',
  anomalyTraces: 'Örnek trace’ler',
} as const;
