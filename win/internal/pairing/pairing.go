// Package pairing handles the agent's TLS identity and its shared secret with
// a paired Mac.
package pairing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	// SecretSize is the length of the pairing secret in bytes.
	SecretSize = 32
	// CodeDigits is the length of the human-typed pairing code.
	CodeDigits = 6

	hkdfInfo     = "porthmoss-v1-pairing"
	certLifetime = 10 * 365 * 24 * time.Hour
)

// Identity is the agent's long-lived TLS identity plus any paired secret.
type Identity struct {
	Certificate tls.Certificate
	// Fingerprint is the SHA-256 of the DER certificate, which the Mac pins.
	Fingerprint [32]byte

	dir    string
	secret []byte
}

type stateFile struct {
	SecretHex string `json:"secret,omitempty"`
	PeerName  string `json:"peer_name,omitempty"`
	PairedAt  string `json:"paired_at,omitempty"`
}

// Load reads the identity from dir, generating a certificate on first run.
func Load(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("pairing: state dir: %w", err)
	}
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		if err := generateCert(certPath, keyPath); err != nil {
			return nil, err
		}
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("pairing: load keypair: %w", err)
	}
	id := &Identity{
		Certificate: cert,
		Fingerprint: sha256.Sum256(cert.Certificate[0]),
		dir:         dir,
	}

	if raw, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		var st stateFile
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fmt.Errorf("pairing: parse state: %w", err)
		}
		if st.SecretHex != "" {
			if id.secret, err = hex.DecodeString(st.SecretHex); err != nil {
				return nil, fmt.Errorf("pairing: decode secret: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("pairing: read state: %w", err)
	}
	return id, nil
}

// Paired reports whether a Mac has already been paired with this agent.
func (id *Identity) Paired() bool { return len(id.secret) == SecretSize }

// FingerprintHex is the pinnable fingerprint, as the user sees it.
func (id *Identity) FingerprintHex() string { return hex.EncodeToString(id.Fingerprint[:]) }

// Save persists a newly agreed secret.
func (id *Identity) Save(secret []byte, peerName string) error {
	if len(secret) != SecretSize {
		return fmt.Errorf("pairing: secret must be %d bytes, got %d", SecretSize, len(secret))
	}
	st := stateFile{
		SecretHex: hex.EncodeToString(secret),
		PeerName:  peerName,
		PairedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(id.dir, "state.json"), raw, 0o600); err != nil {
		return fmt.Errorf("pairing: write state: %w", err)
	}
	id.secret = secret
	return nil
}

// Unpair forgets the paired Mac. The TLS identity is kept so a re-pair does not
// change the fingerprint the user already trusted.
func (id *Identity) Unpair() error {
	id.secret = nil
	err := os.Remove(filepath.Join(id.dir, "state.json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Secret returns the paired secret, or nil.
func (id *Identity) Secret() []byte { return id.secret }

// DeriveSecret turns a pairing code into the shared secret. Both ends run this
// with the same certificate fingerprint as the salt, so a code alone is useless
// against a different machine.
func DeriveSecret(code string, fingerprint [32]byte) ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(code), fingerprint[:], hkdfInfo, SecretSize)
}

// Sign produces the AUTH proof for a challenge nonce.
func Sign(secret, nonce []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(nonce)
	return mac.Sum(nil)
}

// Verify checks an AUTH proof in constant time.
func Verify(secret, nonce, proof []byte) bool {
	return hmac.Equal(Sign(secret, nonce), proof)
}

// NewCode returns a uniformly random pairing code.
func NewCode() (string, error) {
	max := big.NewInt(1)
	for range CodeDigits {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", CodeDigits, n), nil
}

// NewNonce returns a fresh challenge nonce.
func NewNonce() ([]byte, error) {
	nonce := make([]byte, 32)
	_, err := rand.Read(nonce)
	return nonce, err
}

func generateCert(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("pairing: generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "porthmoss-agent"
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"Porthmoss"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("pairing: create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return writePEM(keyPath, "PRIVATE KEY", keyDER, 0o600)
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("pairing: write %s: %w", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return err
	}
	return f.Close()
}
