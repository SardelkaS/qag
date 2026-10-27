package testfixture

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const LocalHBA = `local all all trust
hostnossl qag +qag_test_mtls 0.0.0.0/0 reject
hostnossl qag +qag_test_mtls ::/0 reject
hostssl qag +qag_test_mtls 0.0.0.0/0 cert
hostssl qag +qag_test_mtls ::/0 cert
host all all 0.0.0.0/0 scram-sha-256
host all all ::/0 scram-sha-256
`

func EnsureLocalPKI(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	required := []string{"ca.crt", "ca.key", "server.crt", "server.key"}
	present := 0
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			present++
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if present != 0 && present != len(required) {
		return fmt.Errorf("локальная PKI неполна; восстановите исходные файлы, не заменяя CA автоматически")
	}
	if present == 0 {
		rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		ca := &x509.Certificate{SerialNumber: newSerial(), Subject: pkix.Name{CommonName: "qag local test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, ca, ca, &rootKey.PublicKey, rootKey)
		if err != nil {
			return err
		}
		rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM, err := privatePEM(rootKey)
		if err != nil {
			return err
		}
		serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		server := &x509.Certificate{SerialNumber: newSerial(), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, NotBefore: ca.NotBefore, NotAfter: time.Now().AddDate(2, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, rootKey)
		if err != nil {
			return err
		}
		serverPEM, err := privatePEM(serverKey)
		if err != nil {
			return err
		}
		for name, data := range map[string][]byte{"ca.crt": rootPEM, "ca.key": keyPEM, "server.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}), "server.key": serverPEM} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				return err
			}
		}
	}
	ca, _, err := loadIssuer(dir)
	if err != nil {
		return err
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, "server.crt"))
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "server.key"))
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("пара сервера PKI повреждена")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost"}); err != nil || time.Until(leaf.NotAfter) < 24*time.Hour {
		return fmt.Errorf("PKI недействительна или истекает; подготовьте явное обновление сертификатов")
	}
	return os.WriteFile(filepath.Join(dir, "pg_hba.conf"), []byte(LocalHBA), 0600)
}
func newSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		panic(err)
	}
	return n
}
func privatePEM(k *ecdsa.PrivateKey) ([]byte, error) {
	raw, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), nil
}
func loadIssuer(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	key, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		return nil, nil, err
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, nil, fmt.Errorf("CA PKI повреждён")
	}
	ca, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !ca.IsCA || time.Until(ca.NotAfter) < 24*time.Hour {
		return nil, nil, fmt.Errorf("CA PKI недействителен или истёк")
	}
	k, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("неподдерживаемый ключ CA")
	}
	return ca, k, nil
}
func IssueLocalClient(dir, role string) (Certificates, error) {
	ca, key, err := loadIssuer(dir)
	if err != nil {
		return Certificates{}, err
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificates{}, err
	}
	client := &x509.Certificate{SerialNumber: newSerial(), Subject: pkix.Name{CommonName: role}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, client, ca, &clientKey.PublicKey, key)
	if err != nil {
		return Certificates{}, err
	}
	keyPEM, err := privatePEM(clientKey)
	if err != nil {
		return Certificates{}, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return Certificates{}, err
	}
	return Certificates{CA: string(caPEM), Client: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Key: string(keyPEM)}, nil
}
