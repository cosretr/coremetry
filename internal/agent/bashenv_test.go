package agent

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// v0.10.966 — runbook bash adımının ortamı SAF tablolarla sabitlenir: taban
// adlar geçer, bilinmeyenler düşer, COREMETRY_* HİÇBİR yoldan (taban, proxy,
// tam ad, önek) geçmez, önek eşleşmesi kimlik-benzeri adı ya da '@' taşıyan
// değeri asla süpürmez. Buradaki bir gerileme JWT secret'ı / CH parolasını
// izleyicinin okuyabildiği adım çıktısına sızdırır.

func TestBashEnv(t *testing.T) {
	cases := []struct {
		name   string
		parent []string
		pass   []string       // parseBashPassthrough'a giden knob girdileri
		raw    *bashEnvPolicy // doluysa parse atlanır (sert red'i doğrudan sınamak için)
		want   []string
	}{
		{
			name: "taban adlar geçer, sıralı",
			parent: []string{"USER=app", "TZ=UTC", "TMPDIR=/tmp", "SSL_CERT_FILE=/etc/ssl/ca.pem",
				"SSL_CERT_DIR=/etc/ssl", "PATH=/bin", "no_proxy=.svc", "NO_PROXY=.svc", "LC_ALL=C",
				"LANG=C.UTF-8", "KUBERNETES_SERVICE_PORT=443", "KUBERNETES_SERVICE_HOST=10.0.0.1",
				"KUBECONFIG=/k/config", "HOSTNAME=pod-1", "HOME=/home/app"},
			want: []string{"HOME=/home/app", "HOSTNAME=pod-1", "KUBECONFIG=/k/config",
				"KUBERNETES_SERVICE_HOST=10.0.0.1", "KUBERNETES_SERVICE_PORT=443", "LANG=C.UTF-8",
				"LC_ALL=C", "NO_PROXY=.svc", "PATH=/bin", "SSL_CERT_DIR=/etc/ssl",
				"SSL_CERT_FILE=/etc/ssl/ca.pem", "TMPDIR=/tmp", "TZ=UTC", "USER=app", "no_proxy=.svc"},
		},
		{
			name:   "bilinmeyen adlar düşer",
			parent: []string{"FOO=bar", "OPENAI_API_KEY=synthetic", "ES_PASSWORD=synthetic", "PATH=/bin"},
			want:   []string{"PATH=/bin"},
		},
		{
			name:   "nil ebeveyn: nil değil, yalnız PATH yedeği",
			parent: nil,
			want:   []string{"PATH=" + bashDefaultPath},
		},
		{
			name:   "boş PATH yedeğe döner",
			parent: []string{"PATH=  ", "HOME=/h"},
			want:   []string{"HOME=/h", "PATH=" + bashDefaultPath},
		},
		{
			name:   "değersiz PATH yedeğe döner",
			parent: []string{"PATH="},
			want:   []string{"PATH=" + bashDefaultPath},
		},
		{
			name: "COREMETRY_ tam ad ve CORE* önekiyle bile düşer",
			parent: []string{"COREMETRY_JWT_SECRET=s1", "COREMETRY_SECRET_X=s2", "coremetry_x=s3",
				"Coremetry_Y=s4", "COREMETRY_CH_PASSWORD=s5", "PATH=/bin"},
			raw: &bashEnvPolicy{
				exact:  map[string]bool{"COREMETRY_JWT_SECRET": true, "COREMETRY_SECRET_X": true, "coremetry_x": true, "Coremetry_Y": true},
				prefix: []string{"CORE", "core", "Core"},
			},
			want: []string{"PATH=/bin"},
		},
		{
			name:   "CORE* knob girdisi kabul edilse de COREMETRY_ geçmez",
			parent: []string{"COREMETRY_JWT_SECRET=s1", "CORE_REGION=eu", "PATH=/bin"},
			pass:   []string{"CORE*"},
			want:   []string{"CORE_REGION=eu", "PATH=/bin"},
		},
		{
			name:   "temiz proxy geçer",
			parent: []string{"HTTPS_PROXY=http://px:3128", "PATH=/bin"},
			want:   []string{"HTTPS_PROXY=http://px:3128", "PATH=/bin"},
		},
		{
			name:   "kimlikli proxy düşer",
			parent: []string{"HTTPS_PROXY=http://u:p@px:3128", "HTTP_PROXY=http://u:p@px:3128", "ALL_PROXY=socks5://u:p@px:1080", "PATH=/bin"},
			want:   []string{"PATH=/bin"},
		},
		{
			name:   "kimlikli proxy tam ad listelenince geçer",
			parent: []string{"HTTPS_PROXY=http://u:p@px:3128", "PATH=/bin"},
			pass:   []string{"HTTPS_PROXY"},
			want:   []string{"HTTPS_PROXY=http://u:p@px:3128", "PATH=/bin"},
		},
		{
			name: "küçük harfli proxy adları aynı kural",
			parent: []string{"https_proxy=http://u:p@px:3128", "http_proxy=http://px:3128",
				"all_proxy=socks5://u:p@px:1080", "ALL_PROXY=socks5://px:1080", "PATH=/bin"},
			want: []string{"ALL_PROXY=socks5://px:1080", "PATH=/bin", "http_proxy=http://px:3128"},
		},
		{
			name: "önek kimlik-benzeri adı ya da '@' değeri süpürmez",
			parent: []string{"PD_REGION=eu", "PD_TOKEN=t", "PD_API_KEY=k", "PD_PASSWORD=p",
				"PD_URL=https://u:p@h", "PD_SESSION_ID=x", "PATH=/bin"},
			pass: []string{"PD_*"},
			want: []string{"PATH=/bin", "PD_REGION=eu"},
		},
		{
			name:   "kimlik tam ad listelenince geçer",
			parent: []string{"PD_TOKEN=t", "PD_REGION=eu", "PATH=/bin"},
			pass:   []string{"PD_TOKEN"},
			want:   []string{"PATH=/bin", "PD_TOKEN=t"},
		},
		{
			name:   "tam ad büyük/küçük harfe duyarlı",
			parent: []string{"FOO=up", "foo=low", "PATH=/bin"},
			pass:   []string{"foo"},
			want:   []string{"PATH=/bin", "foo=low"},
		},
		{
			name:   "bozuk ebeveyn girdileri atlanır",
			parent: []string{"NOEQ", "=x", "PATH=/bin"},
			want:   []string{"PATH=/bin"},
		},
		{
			name:   "değerde '=' korunur",
			parent: []string{"LANG=a=b", "PATH=/bin"},
			want:   []string{"LANG=a=b", "PATH=/bin"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p bashEnvPolicy
			if c.raw != nil {
				p = *c.raw
			} else {
				p, _, _ = parseBashPassthrough(c.pass)
			}
			got := bashEnv(c.parent, p)
			if got == nil {
				t.Fatal("bashEnv nil döndü — cmd.Env=nil TÜM ortamı devralır")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("bashEnv =\n  %q\nwant\n  %q", got, c.want)
			}
		})
	}
}

func TestBashEnvZeroPolicy(t *testing.T) {
	// Sıfır politika (ConfigureBashEnv hiç çağrılmadı) yalnız tabanı geçirir.
	got := bashEnv([]string{"HOME=/h", "PD_REGION=eu"}, bashEnvPolicy{})
	want := []string{"HOME=/h", "PATH=" + bashDefaultPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sıfır politika = %q, want %q", got, want)
	}
}

func TestParseBashPassthrough(t *testing.T) {
	cases := []struct {
		name         string
		in           []string
		wantAccepted []string
		wantRejected []string
		wantExact    []string
		wantPrefix   []string
	}{
		{
			name:         "tam ad ve önek kabul",
			in:           []string{"FOO", " K8S_* ", ""},
			wantAccepted: []string{"FOO", "K8S_*"},
			wantExact:    []string{"FOO"},
			wantPrefix:   []string{"K8S_"},
		},
		{
			name:         "tekrar atılır, sıralanır",
			in:           []string{"ZED", "FOO", "K8S_*", "FOO", "K8S_*"},
			wantAccepted: []string{"FOO", "K8S_*", "ZED"},
			wantExact:    []string{"FOO", "ZED"},
			wantPrefix:   []string{"K8S_"},
		},
		{
			name:         "büyük/küçük harf ayrı tam adlar",
			in:           []string{"foo", "FOO"},
			wantAccepted: []string{"FOO", "foo"},
			wantExact:    []string{"FOO", "foo"},
		},
		{
			name:         "kimlik-benzeri tam ad açıklamalı kabul",
			in:           []string{"PD_TOKEN", "FOO"},
			wantAccepted: []string{"FOO", `"PD_TOKEN" (credential-like name, passed because listed exactly)`},
			wantExact:    []string{"FOO", "PD_TOKEN"},
		},
		{
			name: "kısa önek ve çıplak yıldız reddedilir",
			in:   []string{"*", "X*", "AB*", "ABC*"},
			wantRejected: []string{`#1 "*" (prefix must be >=3 chars)`, `#2 "X*" (prefix must be >=3 chars)`,
				`#3 "AB*" (prefix must be >=3 chars)`},
			wantAccepted: []string{"ABC*"},
			wantPrefix:   []string{"ABC"},
		},
		{
			name: "COREMETRY_ hiçbir yazımla geçmez",
			in:   []string{"COREMETRY_FOO", "COREMETRY_*", "coremetry_x", "Coremetry_JWT_SECRET"},
			wantRejected: []string{`#1 "COREMETRY_FOO" (COREMETRY_ prefix is never passed)`,
				`#2 "COREMETRY_*" (COREMETRY_ prefix is never passed)`,
				`#3 "coremetry_x" (COREMETRY_ prefix is never passed)`,
				`#4 "Coremetry_JWT_SECRET" (COREMETRY_ prefix is never passed)`},
		},
		{
			name: "geçersiz adlar yalnız sırayla, içerik yankılanmadan",
			in:   []string{"1BAD", "A-B", "*FOO", "FO*O", "FOO=bar", "OK"},
			wantRejected: []string{"#1 (invalid entry, not shown)", "#2 (invalid entry, not shown)",
				"#3 (invalid entry, not shown)", "#4 (invalid entry, not shown)", "#5 (invalid entry, not shown)"},
			wantAccepted: []string{"OK"},
			wantExact:    []string{"OK"},
		},
		{
			name: "boş girdi",
			in:   nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, acc, rej := parseBashPassthrough(c.in)
			if !reflect.DeepEqual(acc, c.wantAccepted) {
				t.Errorf("accepted = %q, want %q", acc, c.wantAccepted)
			}
			if !reflect.DeepEqual(rej, c.wantRejected) {
				t.Errorf("rejected = %q, want %q", rej, c.wantRejected)
			}
			var exact []string
			for k := range p.exact {
				exact = append(exact, k)
			}
			sort.Strings(exact)
			if !reflect.DeepEqual(exact, c.wantExact) {
				t.Errorf("exact = %q, want %q", exact, c.wantExact)
			}
			if !reflect.DeepEqual(p.prefix, c.wantPrefix) {
				t.Errorf("prefix = %q, want %q", p.prefix, c.wantPrefix)
			}
		})
	}
}

// Değer taşıyan geçersiz girdi (FOO=bar) boot loguna değeri YAZMAMALI.
func TestParseBashPassthroughNeverEchoesValues(t *testing.T) {
	_, acc, rej := parseBashPassthrough([]string{"FOO=bar", "X=bar*", "bar baz", "A-B=bar", "=bar"})
	if len(acc) != 0 {
		t.Fatalf("accepted = %q, want none", acc)
	}
	if len(rej) != 5 {
		t.Fatalf("rejected = %q, want 5 entries", rej)
	}
	for _, r := range rej {
		if strings.Contains(r, "bar") || strings.Contains(r, "=") {
			t.Fatalf("rejected entry echoes content: %q", r)
		}
	}
}

func TestConfigureBashEnv(t *testing.T) {
	t.Cleanup(func() { ConfigureBashEnv(nil) })
	if p := currentBashPolicy(); len(p.exact) != 0 || len(p.prefix) != 0 {
		t.Fatalf("başlangıç politikası boş olmalı: %+v", p)
	}
	acc, rej := ConfigureBashEnv([]string{"PD_REGION", "COREMETRY_JWT_SECRET"})
	if !reflect.DeepEqual(acc, []string{"PD_REGION"}) || len(rej) != 1 {
		t.Fatalf("acc=%q rej=%q", acc, rej)
	}
	if !currentBashPolicy().exact["PD_REGION"] {
		t.Fatal("ConfigureBashEnv politikayı saklamadı")
	}
	ConfigureBashEnv(nil)
	if p := currentBashPolicy(); len(p.exact) != 0 || len(p.prefix) != 0 {
		t.Fatalf("ConfigureBashEnv(nil) sıfırlamadı: %+v", p)
	}
}

func TestBashBaseEnvNames(t *testing.T) {
	names := BashBaseEnvNames()
	joined := "," + strings.Join(names, ",") + ","
	for _, want := range []string{"PATH", "HOME", "HTTPS_PROXY", "no_proxy", "https_proxy"} {
		if !strings.Contains(joined, ","+want+",") {
			t.Fatalf("BashBaseEnvNames %q içermiyor: %q", want, names)
		}
	}
	for _, n := range names {
		if bashEnvDenied(n) {
			t.Fatalf("taban listede COREMETRY_ adı: %q", n)
		}
	}
	// Kopya döner: çağıranın değiştirmesi paket durumunu bozmaz.
	names[0] = "MUTATED"
	if BashBaseEnvNames()[0] == "MUTATED" {
		t.Fatal("BashBaseEnvNames paket dilimini sızdırıyor")
	}
}
