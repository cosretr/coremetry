// Package evalset — v0.10.940 (Settings › AI › Değerlendirme paneli): donmuş
// replay vakaları BINARY'YE GÖMÜLÜ.
//
// Neden gömülü: panel koşuyu sunucuda başlatır, ama çalışma imajı yalnız
// binary + config.yaml taşır (Dockerfile) — diskten okumak imkânsız. JSON'lar
// derleme bağlamında zaten var (.dockerignore yalnız *.md'yi dışlar), yani
// gömmek imaja ek adım istemez; ~44 KB.
//
// Neden bu dizinde ayrı bir paket (copilot paketinde `//go:embed evalset/*.json`
// DEĞİL): go:embed üst dizine uzanamaz, internal/api buraya erişemez; copilot
// paketine koymak fikstürü prompt kodunun derleme birimine bağlardı. Tek
// tüketici internal/api (ai_evalset_core.go loadEvalsetCases) — hem panel hem
// `-tags evalset` CLI koşumu aynı gömülü kümeyi okur, yani TestEvalsetFixturesValid
// artık GEMİYE BİNENİ doğrular.
package evalset

import "embed"

// FS — bu dizindeki *.json fikstürleri (README.md hariç).
//
//go:embed *.json
var FS embed.FS
