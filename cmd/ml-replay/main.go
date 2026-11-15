// ml-replay — v0.10.2252 (anomali kartı Dilim E, docs/ml/anomaly-card-audit.md
// §7.1, §10 E): dışa aktarılmış geçmiş ML olaylarını bugünkü kayıt
// sözleşmesinden ve gölge yayın kapılarından geçirir, dedektör başına rapor
// basar. Çevrimdışı: yalnız dosya okur, ClickHouse'a bağlanmaz.
//
//	go run ./cmd/ml-replay -dir internal/ml/testdata/fixtures
//	make eval-ml                                   # aynı, varsayılan dizin
//	make eval-ml ML_REPLAY_DIR=internal/ml/testdata/replay   # sentetik
//
// Çıkış kodu: 0 = beklentiler tuttu (ya da expect.json yok), 2 = en az bir
// olayda expect.json tutmadı, 1 = okuma hatası. Geliştirici aracı; release
// imajına girmez.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/cilcenk/coremetry/internal/ml"
)

func main() {
	dir := flag.String("dir", "internal/ml/testdata/fixtures", "fikstür kökü (olay başına dizin, ml_events.jsonl[.gz])")
	asJSON := flag.Bool("json", false, "raporu JSON bas")
	flag.Parse()
	rep, err := ml.ReplayDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ml-replay:", err)
		os.Exit(1)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(rep)
	} else {
		err = rep.WriteText(os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ml-replay:", err)
		os.Exit(1)
	}
	if rep.Mismatches > 0 {
		os.Exit(2)
	}
}
