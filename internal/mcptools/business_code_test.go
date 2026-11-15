package mcptools

// resolve_business_code — merdiven, belirsizlik, boş sonuç, önbellek anahtarı.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var bizNames = []BizName{
	{BizFunctionCode, "BQRZ001"}, {BizFunctionCode, "BQRZ002"}, {BizFunctionCode, "IQV0181"},
	{BizChannelCode, "060203"},
	{BizOperation, "KSK_MAIN_PIN_CHANGE"}, {BizOperation, "EXAMPLE_PAYMENT_XFR"},
	{BizEndpoint, "/get/v{version}/file/stream/s3"},
}

func TestRankBizNames(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string // kind:value sırası
		match      string
	}{
		{"fonksiyon kodu tam", "BQRZ001", []string{"function_code:BQRZ001"}, "exact"},
		{"cümle içinde kod", "BQRZ001 hangi serviste?", []string{"function_code:BQRZ001"}, "exact"},
		{"kanal kodu", "060203", []string{"channel_code:060203"}, "exact"},
		{"yazım hatalı operasyon", "KSK_MAIN_PIN_CHANGEE", []string{"operation:KSK_MAIN_PIN_CHANGE"}, "fuzzy"},
		{"endpoint parçası", "file/stream/s3", []string{"endpoint:/get/v{version}/file/stream/s3"}, "substring"},
		{"önek → iki aday", "BQRZ00", []string{"function_code:BQRZ001", "function_code:BQRZ002"}, "prefix"},
		{"eşleşme yok", "ZZZZ999", nil, ""},
	}
	for _, c := range cases {
		got := RankBizNames(c.text, bizNames, 5)
		var keys []string
		for _, g := range got {
			keys = append(keys, g.Kind+":"+g.Value)
		}
		if strings.Join(keys, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v want %v", c.name, keys, c.want)
			continue
		}
		if len(got) > 0 && got[0].Match != c.match {
			t.Errorf("%s: match %s want %s", c.name, got[0].Match, c.match)
		}
	}
}

func TestBizDecision(t *testing.T) {
	one := []BizCandidate{{Kind: BizFunctionCode, Value: "BQRZ001", Score: 100, Services: []BizServiceStat{{Service: "a"}}}}
	if amb, opts, why := BizDecision(one, "BQRZ001", bizKinds); amb || opts != nil || why != nil {
		t.Errorf("tek aday tek servis belirsiz değil: %v %v %v", amb, opts, why)
	}
	multiSvc := []BizCandidate{{Kind: BizFunctionCode, Value: "BQRZ001", Score: 100, Services: []BizServiceStat{{Service: "a"}, {Service: "b"}}}}
	if amb, opts, _ := BizDecision(multiSvc, "BQRZ001", bizKinds); !amb || len(opts) != 2 || opts[0] != "a" {
		t.Errorf("çok servis → belirsiz + servis seçenekleri: %v %v", amb, opts)
	}
	multiCand := []BizCandidate{{Kind: BizFunctionCode, Value: "BQRZ001", Score: 80}, {Kind: BizFunctionCode, Value: "BQRZ002", Score: 80}}
	if amb, opts, _ := BizDecision(multiCand, "BQRZ00", bizKinds); !amb || len(opts) != 2 {
		t.Errorf("eş güçte iki aday → belirsiz: %v %v", amb, opts)
	}
	if amb, _, why := BizDecision(nil, "ZZZ", bizKinds); amb || len(why) < 2 || !strings.Contains(why[0], "eşleşme yok") {
		t.Errorf("boş sonuç hata değil, gerekçeli: %v %v", amb, why)
	}
}

func TestBizIndexKeyHashesAllInputs(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	base := bizIndexKey(BizFunctionCode, t0, t0.Add(time.Hour))
	if base == bizIndexKey(BizChannelCode, t0, t0.Add(time.Hour)) {
		t.Error("tür anahtara girmeli")
	}
	if base == bizIndexKey(BizFunctionCode, t0.Add(-time.Hour), t0.Add(time.Hour)) {
		t.Error("pencere başı anahtara girmeli")
	}
	if base != bizIndexKey(BizFunctionCode, t0.Add(time.Second), t0.Add(time.Hour+time.Second)) {
		t.Error("5 dk kovası içindeki kayma aynı anahtar")
	}
}

func TestBizHelpers(t *testing.T) {
	st := bizStats([]chstore.CodeServiceStat{{Service: "a", Calls: 3, Errors: 1}, {Service: "b"}})
	if st[0].ErrorRatePct != 33.33 || st[1].ErrorRatePct != 0 {
		t.Errorf("%+v", st)
	}
	if f := bizFilter(BizFunctionCode, "BQRZ001"); f.Key != "FUNCTION_CODE" || f.Values[0] != "BQRZ001" {
		t.Errorf("%+v", f)
	}
	if f := bizFilter(BizEndpoint, "/x"); f.Key != "http.route" {
		t.Errorf("%+v", f)
	}
	if bizWindowLabel(86400) != "son 24 sa" || bizWindowLabel(1800) != "son 30 dk" {
		t.Error("pencere etiketi")
	}
}
