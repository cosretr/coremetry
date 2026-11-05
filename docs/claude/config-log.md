# Claude Code yapılandırma günlüğü

Bir satır = CLAUDE.md / `.claude/` (skill, ajan, hook, plugin) değişikliği. En yeni satır en altta; satır değişiklikle aynı commit'te yazılır. (shadcn geçişinin kendi günlüğü `docs/frontend/migration-log.md`'dir; bu dosya Claude kurulumunu izler.)

| Tarih | Ne değişti | Dosyalar | Not |
|---|---|---|---|
| 2026-11-05 | Token audit Faz 1 (8 commit): CLAUDE.md 158→78 satır, 21→19 skill, aiops bölündü, gate.sh, large-files.md, verifier ajanı; artıklar: superpowers projede kapalı, eski skill adları yorumlarda | CLAUDE.md, .claude/skills/*, .claude/agents/verifier.md, scripts/gate.sh, docs/claude/* | docs/claude/token-audit.md |
| 2026-11-05 | Model kademeleri bölümü + `implementer` (Sonnet) ve `architect` (Fable) ajanları; repo-explorer Sonnet doğrulandı | CLAUDE.md, .claude/agents/{implementer,architect}.md | yeni ajanlar sonraki oturumda yüklenir |
