// ProblemTitleText — v0.10.2487 (dilim 3): problemTitle çiftinin TEK çizimi
// (liste satırı, detay başlığı, standart blok). Özne mono, ayırıcı aynı.
import { OrigLang } from './OrigLang';
import { PROBLEM_TITLE_SEP, type ProblemTitle } from './problemTitle';

export function ProblemTitleText({ title }: { title: ProblemTitle }) {
  return (
    <>
      {title.subject && <><span className="std-subject">{title.subject}</span>{PROBLEM_TITLE_SEP}</>}
      {title.orig ? <OrigLang>{title.rest}</OrigLang> : title.rest}
    </>
  );
}
