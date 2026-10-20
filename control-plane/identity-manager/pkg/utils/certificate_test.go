/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateSigningCertificate_Base64DER(t *testing.T) {
	t.Parallel()
	signed := createCertificate(t, true)

	require.NoError(t, ValidateSigningCertificate(base64.StdEncoding.EncodeToString(signed)))
}

func TestValidateSigningCertificate_PEM(t *testing.T) {
	t.Parallel()
	signed := createCertificate(t, true)
	encoded := pem.EncodeToMemory(&pem.Block{Type: CERTIFICATE, Bytes: signed})

	require.NoError(t, ValidateSigningCertificate(string(encoded)))
}

func TestValidateSigningCertificate_Missing(t *testing.T) {
	t.Parallel()

	err := ValidateSigningCertificate("  ")
	require.ErrorIs(t, err, ErrUnsignedCertificate)
	require.ErrorContains(t, err, "required")
}

func TestValidateSigningCertificate_NotACertificate(t *testing.T) {
	t.Parallel()

	err := ValidateSigningCertificate("not-a-certificate")
	require.ErrorIs(t, err, ErrUnsignedCertificate)
	require.ErrorContains(t, err, "invalid")
}

func TestValidateSigningCertificate_SignatureDoesNotVerify(t *testing.T) {
	t.Parallel()
	signed := createCertificate(t, true)
	tampered := append([]byte(nil), signed...)
	tampered[len(tampered)-2] ^= 0xff

	err := ValidateSigningCertificate(base64.StdEncoding.EncodeToString(tampered))
	require.ErrorIs(t, err, ErrUnsignedCertificate)
}

func TestValidateSigningCertificate_SignedByDifferentKey(t *testing.T) {
	t.Parallel()

	err := ValidateSigningCertificate(base64.StdEncoding.EncodeToString(createCertificate(t, false)))
	require.ErrorIs(t, err, ErrUnsignedCertificate)
	require.ErrorContains(t, err, "not signed")
}

func TestValidateSigningCertificate_LeafSignedByTrustedPublicCA(t *testing.T) {
	t.Parallel()
	leaf, roots := trustedPublicLeaf(t)

	err := validateSigningCertificate(base64.StdEncoding.EncodeToString(leaf), roots)
	require.NoError(t, err)
}

func TestValidateSigningCertificate_IntermediateIncludedWithLeaf(t *testing.T) {
	t.Parallel()
	caCert, caKey := trustedCA(t)
	intermediateDER, intermediateKey := issueCertificate(t, caCert, caKey, "public-intermediate", true)
	intermediate, err := x509.ParseCertificate(intermediateDER)
	require.NoError(t, err)
	leafDER, _ := issueCertificate(t, intermediate, intermediateKey, "idp.example.com", false)

	chain := pem.EncodeToMemory(&pem.Block{Type: CERTIFICATE, Bytes: leafDER})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: CERTIFICATE, Bytes: intermediateDER})...)
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	require.NoError(t, validateSigningCertificate(string(chain), roots))
}

func TestValidateSigningCertificate_SignedByUntrustedCA(t *testing.T) {
	t.Parallel()
	leaf, _ := trustedPublicLeaf(t)

	err := validateSigningCertificate(base64.StdEncoding.EncodeToString(leaf), x509.NewCertPool())
	require.ErrorIs(t, err, ErrUnsignedCertificate)
	require.ErrorContains(t, err, "not signed")
}

func trustedPublicLeaf(t *testing.T) ([]byte, *x509.CertPool) {
	caCert, caKey := trustedCA(t)
	leaf, _ := issueCertificate(t, caCert, caKey, "idp.example.com", false)
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return leaf, roots
}

func trustedCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	der, key := issueCertificate(t, nil, nil, "public-ca", true)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func issueCertificate(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, commonName string, isCA bool) ([]byte, *ecdsa.PrivateKey) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	if isCA {
		template.IsCA = true
		template.BasicConstraintsValid = true
		template.KeyUsage = x509.KeyUsageCertSign
	} else {
		template.KeyUsage = x509.KeyUsageDigitalSignature
	}
	signer := parent
	signerKey := parentKey
	if signer == nil {
		signer = template
		signerKey = key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	require.NoError(t, err)
	return der, key
}

func createCertificate(t *testing.T, selfSigned bool) []byte {
	subjectKey := newKey(t)
	issuerKey := subjectKey
	issuer := certificateTemplate("idp")
	subject := issuer
	if !selfSigned {
		issuerKey = newKey(t)
		issuer = certificateTemplate("ca")
		subject = certificateTemplate("idp")
	}
	der, err := x509.CreateCertificate(rand.Reader, subject, issuer, &subjectKey.PublicKey, issuerKey)
	require.NoError(t, err)
	return der
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

func certificateTemplate(commonName string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
}
