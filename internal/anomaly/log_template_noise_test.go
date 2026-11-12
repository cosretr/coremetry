package anomaly

// v0.10.2028 — Operator-reported: adı -batch ile biten bir servis 30
// dakikada milyonlarca satır yazarken, 3 kez görülen yeni bir log kalıbı
// "anomali" diye açılıyordu (24 saatte 133 olay). Yeni kalıp artık ancak
// anlamlı bir satır sayısına ya da servisin log hacmi içinde anlamlı bir
// paya ulaşınca olay sayılır; batch servislerde (ERROR ve üstü hariç) hiç
// sayılmaz. Fikstür nötr (orders-batch).

import "testing"

func TestNewTemplateIsEvent(t *testing.T) {
	cases := []struct {
		name string
		in   newTemplateNoiseInput
		want bool
	}{
		{"3 satır / 5,4 M → olay yok (operatör örneği)", newTemplateNoiseInput{Lines: 3, ServiceLines: 5_400_000}, false},
		{"20 satır → olay", newTemplateNoiseInput{Lines: 20}, true},
		{"5 satır / 400 (pay %1,25) → olay", newTemplateNoiseInput{Lines: 5, ServiceLines: 400}, true},
		{"5 satır / 10 000 (pay %0,05) → olay yok", newTemplateNoiseInput{Lines: 5, ServiceLines: 10_000}, false},
		{"batch + INFO 500 satır → olay yok", newTemplateNoiseInput{Lines: 500, Batch: true, Level: "INFO"}, false},
		{"batch + ERROR 25 satır → olay", newTemplateNoiseInput{Lines: 25, Batch: true, Level: "ERROR"}, true},
		{"batch + FATAL 25 satır → olay", newTemplateNoiseInput{Lines: 25, Batch: true, Level: "fatal"}, true},
		{"batch + ERROR 5 satır hacim bilinmiyor → olay yok (eşik aynı)", newTemplateNoiseInput{Lines: 5, Batch: true, Level: "ERROR"}, false},
		{"batch + seviye bilinmiyor 500 satır → olay yok", newTemplateNoiseInput{Lines: 500, Batch: true}, false},
		{"hacim bilinmiyor + 19 satır → olay yok", newTemplateNoiseInput{Lines: 19}, false},
		{"operatör örneği, batch + 3 satır → olay yok", newTemplateNoiseInput{Lines: 3, Batch: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := newTemplateIsEvent(c.in); got != c.want {
				t.Fatalf("newTemplateIsEvent(%+v) = %v, istenen %v", c.in, got, c.want)
			}
		})
	}
}

// Recorder adımı: dedektörün ürettiği adaylardan taban altındakiler düşer;
// şablon defteri bu adımdan etkilenmez (yalnız anomaly_events yazımı).
func TestFilterNewTemplateNoise(t *testing.T) {
	isBatch := func(s string) bool { return s == "orders-batch" }
	in := []LogTemplateAnomaly{
		{TemplateID: "a", Service: "orders-batch", TotalCount: 3},
		{TemplateID: "b", Service: "orders-batch", TotalCount: 900},
		{TemplateID: "c", Service: "checkout", TotalCount: 25},
		{TemplateID: "d", Service: "checkout", TotalCount: 7},
	}
	out, cut := filterNewTemplateNoise(in, isBatch, nil)
	if len(out) != 1 || out[0].TemplateID != "c" || cut != 3 {
		t.Fatalf("kalan %+v, kesilen %d", out, cut)
	}
}

// v0.10.2029 — kalıbın örneklem seviyesi karara girer: batch serviste
// çoğunluğu ERROR+ olan kalıp ≥ 20 satırla olay üretir; seviye bilinmiyor
// ya da "other" çoğunlukta ise üretmez. Batch olmayan serviste seviye
// sorulmaz (davranış aynı).
func TestFilterNewTemplateNoiseLevel(t *testing.T) {
	isBatch := func(s string) bool { return s == "orders-batch" }
	levels := map[string]string{"err": "ERROR", "other": ""}
	asked := map[string]bool{}
	levelOf := func(id string) string { asked[id] = true; return levels[id] }
	in := []LogTemplateAnomaly{
		{TemplateID: "err", Service: "orders-batch", TotalCount: 25},
		{TemplateID: "other", Service: "orders-batch", TotalCount: 900},
		{TemplateID: "plain", Service: "checkout", TotalCount: 25},
	}
	out, cut := filterNewTemplateNoise(in, isBatch, levelOf)
	if len(out) != 2 || out[0].TemplateID != "err" || out[1].TemplateID != "plain" || cut != 1 {
		t.Fatalf("kalan %+v, kesilen %d", out, cut)
	}
	if asked["plain"] {
		t.Error("batch olmayan serviste seviye sorulmamalı")
	}
}
