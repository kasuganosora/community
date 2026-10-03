package models

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// ParseAccountPrivateKey supports the source PEM without rewriting its bytes.
func ParseAccountPrivateKey(encoded string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("invalid private key PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, key.Validate()
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("unsupported private key encoding")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("ActivityPub signing key must be RSA")
	}
	return key, key.Validate()
}
func (k *AccountKey) ValidateKeyPair() error {
	private, err := ParseAccountPrivateKey(k.PrivateKey)
	if err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(k.PublicKey))
	if block == nil {
		return errors.New("invalid public key PEM")
	}
	var public *rsa.PublicKey
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		public, _ = parsed.(*rsa.PublicKey)
	}
	if public == nil {
		public, _ = x509.ParsePKCS1PublicKey(block.Bytes)
	}
	if public == nil || public.E != private.E || public.N.Cmp(private.N) != 0 {
		return errors.New("public and private signing keys do not match")
	}
	return nil
}
func (k *AccountKey) SigningKeyID(actorURI string) string {
	if k.KeyID != "" {
		return k.KeyID
	}
	return actorURI + "#main-key"
}
