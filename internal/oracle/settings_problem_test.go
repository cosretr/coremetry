package oracle

import (
	"strings"
	"testing"
)

// v0.10.897 (Aşama 3 dilim D) — kaynak başına Problem ayarları: kip boş/
// bilinmeyen → shadow, off/live korunur; kod listeleri trim+upper+tekrarsız,
// tavan 50 / 64 karakter hata; jenerik varsayılanı ERR_020; ignore kümesi.
func TestNormalizeProblemSettings(t *testing.T) {
	base := SourceConfig{Name: "o", Host: "h", ServiceName: "s", User: "u", Password: "p", Schema: "S", Table: "T"}
	src := base
	src.ProblemMode, src.GenericCodes, src.IgnoreCodes = " LIVE ", []string{" bsa_020", "BSA_020", "", "err_020"}, []string{"x"}
	out, err := Normalize(Settings{Sources: []SourceConfig{src}}, Settings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := out.Sources[0]
	if got.ProblemMode != ProblemModeLive || strings.Join(got.GenericCodes, ",") != "BSA_020,ERR_020" || strings.Join(got.IgnoreCodes, ",") != "X" {
		t.Fatalf("normalize: %+v", got)
	}
	src2 := base
	src2.ProblemMode = "garbage"
	out2, _ := Normalize(Settings{Sources: []SourceConfig{src2}}, Settings{}, nil)
	if ProblemModeOf(out2.Sources[0]) != ProblemModeShadow || ProblemModeOf(SourceConfig{}) != ProblemModeShadow {
		t.Fatal("bilinmeyen/boş kip shadow")
	}
	if !GenericSet(SourceConfig{})["ERR_020"] || !IgnoreSet(got)["X"] || IgnoreSet(SourceConfig{})["X"] {
		t.Fatal("kümeler")
	}
	src3 := base
	src3.IgnoreCodes = []string{strings.Repeat("A", 65)}
	if _, err := Normalize(Settings{Sources: []SourceConfig{src3}}, Settings{}, nil); err == nil {
		t.Fatal("64 karakter tavanı")
	}
	src4 := base
	for i := 0; i < 51; i++ {
		src4.GenericCodes = append(src4.GenericCodes, "C"+itoa(i))
	}
	if _, err := Normalize(Settings{Sources: []SourceConfig{src4}}, Settings{}, nil); err == nil {
		t.Fatal("50 girdi tavanı")
	}
}
