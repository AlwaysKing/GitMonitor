package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"strings"
)

const sshEd25519KeyType = "ssh-ed25519"

func GenerateSSHKeyPair(comment string) (string, string, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 key: %w", err)
	}

	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateDER,
	})

	publicKey, err := marshalAuthorizedEd25519PublicKey(privateKey.Public().(ed25519.PublicKey), comment)
	if err != nil {
		return "", "", err
	}
	return string(privatePEM), publicKey, nil
}

func FingerprintSSHPublicKey(publicKey string) string {
	parts := strings.Fields(strings.TrimSpace(publicKey))
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(sum[:])
}

func marshalAuthorizedEd25519PublicKey(publicKey ed25519.PublicKey, comment string) (string, error) {
	payload := make([]byte, 0, 4+len(sshEd25519KeyType)+4+len(publicKey))
	payload = appendSSHString(payload, []byte(sshEd25519KeyType))
	payload = appendSSHString(payload, publicKey)

	encoded := base64.StdEncoding.EncodeToString(payload)
	authorizedKey := sshEd25519KeyType + " " + encoded
	if strings.TrimSpace(comment) != "" {
		authorizedKey += " " + strings.TrimSpace(comment)
	}
	return authorizedKey, nil
}

func appendSSHString(dst, value []byte) []byte {
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(value)))
	dst = append(dst, size...)
	dst = append(dst, value...)
	return dst
}
