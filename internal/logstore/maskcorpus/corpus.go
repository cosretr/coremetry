// Package maskcorpus — v0.10.1829 log kalıbı kimliği regresyon korpusu
// (yalnız veri; üretim kodu kullanmaz). İki kimlik — Logs › Desenler imzası
// (logstore.NormalizeSignature) ve Drain şablonu (templater.Mask) — AYNI
// satırlarla sınanır: logstore/variable_corpus_test.go ve
// templater/masker_variable_test.go.
//
// Operator-reported: "Sadece bazen loglarda GUID, UUID, id değerleri
// oluyor; bunları yeni log pattern'i algılamasın." Değerler kurgusal
// (acme, orders-api, ORD-88213); gerçek kurum / kişi / kimlik yok.
package maskcorpus

// Group — içindeki satırlar AYNI kimliği almalı.
type Group struct {
	Name  string
	Lines []string
}

// Pair — iki satır FARKLI kimlikte kalmalı. SignatureOnly: Drain'in yol
// kuralı (`/[\w./_-]+` → `<*>`, v0.5.x'ten beri) çifti zaten birleştirir;
// yalnız imza yolunda sınanır.
type Pair struct {
	Name          string
	A, B          string
	SignatureOnly bool
}

// Same — her grup içindeki satırlar AYNI kimliği almalı.
var Same = []Group{
	{"dashed uuid lower/upper", []string{
		"order 550e8400-e29b-41d4-a716-446655440000 accepted",
		"order 6BA7B810-9DAD-11D1-80B4-00C04FD430C8 accepted"}},
	{"32-hex guid", []string{
		"session 6ba7b8109dad11d180b400c04fd430c8 opened",
		"session 0f1e2d3c4b5a69788796a5b4c3d2e1f0 opened"}},
	{"braced guid = plain value", []string{
		"entity {550e8400-e29b-41d4-a716-446655440000} saved",
		"entity {6BA7B810-9DAD-11D1-80B4-00C04FD430C8} saved",
		"entity 54015241 saved"}},
	{"urn:uuid", []string{
		"message urn:uuid:550e8400-e29b-41d4-a716-446655440000 received",
		"message urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8 received"}},
	{"uuid glued to underscore", []string{
		"cache id_550e8400-e29b-41d4-a716-446655440000 evicted",
		"cache id_6ba7b810-9dad-11d1-80b4-00c04fd430c8 evicted"}},
	{"uuid after = : / #", []string{
		"lookup key=550e8400-e29b-41d4-a716-446655440000 /items/6ba7b810-9dad-11d1-80b4-00c04fd430c8 #0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		"lookup key=6ba7b810-9dad-11d1-80b4-00c04fd430c8 /items/550e8400-e29b-41d4-a716-446655440000 #11111111-2222-3333-4444-555555555555"}},
	{"numeric id short/long", []string{
		"Order 7 shipped", "Order 54015241 shipped", "Order 1234567890123 shipped"}},
	{"thousands separators + decimals", []string{
		"processed 26,916,627 rows", "processed 1,204 rows", "processed 87 rows",
		"processed 1.234.567 rows", "processed 1 234 567 rows", "processed 3,14 rows"}},
	{"format placeholder = value", []string{
		"Record not found: {0}.", "Record not found: {}.", "Record not found: %s.",
		"Record not found: 54015241.", "Record not found: 26,916,627.", "Record not found: %d."}},
	{"sql bind = value", []string{
		"SELECT name FROM orders WHERE id = ?", "SELECT name FROM orders WHERE id = 42"}},
	{"mixed alnum id", []string{
		"txn TX9F3K2 committed", "txn QZ7M4P1 committed", "txn 54015241 committed"}},
	{"prefixed business id", []string{
		"order ORD-88213 shipped", "order ORD-7 shipped"}},
	{"hex id", []string{
		"blob a07d33f9c40 stored", "blob 9be1c2d3f4a stored", "blob 7d9f6c stored"}},
	{"k8s pod suffix", []string{
		"pod orders-api-7d9f6-x2k4p ready", "pod orders-api-5c8b7d9f4-m2n7q ready",
		"pod orders-api-84f6d-wq2zx ready", "pod orders-api-x2k4p ready"}},
	{"e-mail", []string{
		"invite sent to someone@acme.example", "invite sent to j.doe+test@mail.acme.example"}},
	{"ipv6 / ipv4", []string{
		"client fe80::1ff:fe23:4567:890a connected", "client 2001:db8::8a2e:370:7334 connected",
		"client 10.0.0.12 connected", "client [::1]:8080 connected"}},
	{"java object hash", []string{
		"bean com.acme.OrderService@1a2b3c registered", "bean com.acme.OrderService@7f9e2d4c registered"}},
	{"duration units", []string{
		"query took 1500ms", "query took 30ms", "query took 5ms", "query took 2.5ms"}},
	{"size units", []string{
		"heap 18.4MB used", "heap 18.45MB used", "heap 512MB used"}},
	{"path id", []string{
		"GET /api/orders/ORD88213X/items", "GET /api/orders/QZ7M4P1/items", "GET /api/orders/54015241/items"}},
	{"thread suffix", []string{
		"worker-12 started", "worker-7 started"}},
	{"list", []string{
		"ids=[1, 2, 3] queued", "ids=[54015241] queued"}},
	{"quoted id", []string{
		`token 'TX9F3K2' rejected`, `token 'QZ7M4P1' rejected`}},
	{"json message", []string{
		`{"msg":"payment 550e8400-e29b-41d4-a716-446655440000 settled in 15ms","request_id":"TX9F3K2"}`,
		`{"msg":"payment 6ba7b810-9dad-11d1-80b4-00c04fd430c8 settled in 1500ms","request_id":"QZ7M4P1"}`}},
}

// Different — her çiftin iki satırı FARKLI kimlikte kalmalı.
var Different = []Pair{
	{"different verbs", "connection refused by upstream", "connection reset by upstream", false},
	{"different exception class", "java.lang.NullPointerException at OrderService.handle",
		"java.lang.IllegalStateException at OrderService.handle", false},
	{"named placeholder is text", "user {name} logged in", "user 42 logged in", false},
	{"http protocol version", "GET /health HTTP/1.1", "GET /health HTTP/2", true},
	{"digest names", "sha256 checksum mismatch", "md5 checksum mismatch", false},
	{"categorical words", "payment status OK", "payment status FAILED", false},
	{"dictionary token", "negotiated http2 with upstream", "negotiated h2c with upstream", false},
	{"ora message text", "ORA-00942: table or view does not exist", "ORA-01017: invalid username/password; logon denied", false},
}
