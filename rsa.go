package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
)

func GenerateKey(keyName string) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	// Save private key
	privateFile, err := os.Create(keyName)
	if err != nil {
		panic(err)
	}
	defer privateFile.Close()

	privateBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}

	if err := pem.Encode(privateFile, privateBlock); err != nil {
		panic(err)
	}

	// Save public key
	publicKey := &privateKey.PublicKey
	publicFile, err := os.Create(keyName + ".pub")
	if err != nil {
		panic(err)
	}
	defer publicFile.Close()

	publicBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		panic(err)
	}

	publicBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicBytes,
	}

	if err := pem.Encode(publicFile, publicBlock); err != nil {
		panic(err)
	}

	return privateKey, publicKey, nil
}
