package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "test_key")

	privKey, pubKey, err := GenerateKey(keyPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if privKey == nil {
		t.Fatal("private key is nil")
	}
	if pubKey == nil {
		t.Fatal("public key is nil")
	}

	// Verify private key file
	privBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read private key file: %v", err)
	}
	privBlock, _ := pem.Decode(privBytes)
	if privBlock == nil {
		t.Fatal("failed to decode PEM block from private key file")
	}
	if privBlock.Type != "RSA PRIVATE KEY" {
		t.Errorf("private key PEM type = %q, want %q", privBlock.Type, "RSA PRIVATE KEY")
	}
	_, err = x509.ParsePKCS1PrivateKey(privBlock.Bytes)
	if err != nil {
		t.Errorf("failed to parse private key: %v", err)
	}

	// Verify public key file
	pubBytes, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("failed to read public key file: %v", err)
	}
	pubBlock, _ := pem.Decode(pubBytes)
	if pubBlock == nil {
		t.Fatal("failed to decode PEM block from public key file")
	}
	if pubBlock.Type != "PUBLIC KEY" {
		t.Errorf("public key PEM type = %q, want %q", pubBlock.Type, "PUBLIC KEY")
	}
	_, err = x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		t.Errorf("failed to parse public key: %v", err)
	}
}
