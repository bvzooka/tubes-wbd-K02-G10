package mysql

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"
	"dbplatform/internal/domain"
)

type testCert struct {
	cert *x509.Certificate
	key *ecdsa.PrivateKey
}

func newTestCert(t *testing.T, tmpl *x509.Certificate, parent *testCert) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCert{cert: cert, key: key}
}

func newTestCA(t *testing.T, name string) testCert {
	return newTestCert(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour),
		IsCA: true,
		BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}, nil)
}

func newTestServerCert(t *testing.T, ca testCert, host string, notAfter time.Time) testCert {
	return newTestCert(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: host},
		DNSNames: []string{host},
		NotBefore: time.Now().Add(-2 * time.Hour),
		NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &ca)
}

func TestVerifyCAConfig(t *testing.T) {
	ca := newTestCA(t, "CA Uji")
	otherCA := newTestCA(t, "CA Lain")
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	good := newTestServerCert(t, ca, "server-lain.example", time.Now().Add(time.Hour))
	expired := newTestServerCert(t, ca, "server.example", time.Now().Add(-time.Minute))
	stranger := newTestServerCert(t, otherCA, "server.example", time.Now().Add(time.Hour))

	cfg := verifyCAConfig(roots)
	if !cfg.InsecureSkipVerify || cfg.VerifyConnection == nil || cfg.MinVersion < tls.VersionTLS12 {
		t.Fatalf("konfigurasi: %+v", cfg)
	}
	verify := func(c testCert) error {
		return cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{c.cert}, ServerName: "server.example"})
	}

	if err := verify(good); err != nil {
		t.Errorf("sertifikat dari CA tepercaya dengan nama host berbeda harus lolos pada verify-ca: %v", err)
	}
	if err := verify(expired); err == nil {
		t.Error("sertifikat kedaluwarsa harus ditolak")
	}
	if err := verify(stranger); err == nil {
		t.Error("sertifikat dari CA tak dikenal harus ditolak")
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Error("tanpa sertifikat harus ditolak")
	}

	full := &x509.VerifyOptions{Roots: roots, DNSName: "server.example"}
	if _, err := good.cert.Verify(*full); err == nil {
		t.Error("kontrol: verify-full akan menolak nama host yang berbeda")
	}
}

func TestClassifyError_CertificateProblems(t *testing.T) {
	cases := map[string]error{
		"otoritas tak dikenal": x509.UnknownAuthorityError{},
		"nama host salah":      x509.HostnameError{Host: "x", Certificate: &x509.Certificate{}},
		"sertifikat tak sah":   x509.CertificateInvalidError{Reason: x509.Expired},
		"gagal verifikasi tls": &tls.CertificateVerificationError{Err: errors.New("x509: bad")},
	}
	for name, err := range cases {
		got := ClassifyError(err)
		if !errors.Is(got, domain.ErrConnectionFailed) || got.Error() != "Sertifikat SSL server tidak dapat diverifikasi" {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestClassifyError_TLSHandshakeFailure(t *testing.T) {
	err := &net.OpError{Op: "remote error", Err: errors.New("remote error: tls: handshake failure")}
	got := ClassifyError(err)
	want := "Negosiasi SSL dengan server gagal (versi TLS atau cipher tidak cocok)"
	if !errors.Is(got, domain.ErrConnectionFailed) || got.Error() != want {
		t.Errorf("got %v", got)
	}
	refused := &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}
	if got := ClassifyError(refused); got.Error() != "Tidak dapat terhubung ke database (host tidak terjangkau)" {
		t.Errorf("koneksi ditolak biasa tidak boleh dianggap masalah TLS: %v", got)
	}
}
