package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const encPrefix = "ENC("
const encSuffix = ")"

// generateKey magic key'den 32 byte AES-256 key türetir
func generateKey(magicKey string) []byte {
	hash := sha256.Sum256([]byte(magicKey))
	return hash[:]
}

// Encrypt plaintext'i AES-GCM ile şifreler ve base64 olarak döner
func Encrypt(plaintext, magicKey string) (string, error) {
	key := generateKey(magicKey)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher oluşturulamadı: %v", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("GCM oluşturulamadı: %v", err)
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce oluşturulamadı: %v", err)
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt base64 encoded AES-GCM şifreli metni çözer
func Decrypt(encoded, magicKey string) (string, error) {
	key := generateKey(magicKey)

	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode hatası: %v", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher oluşturulamadı: %v", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("GCM oluşturulamadı: %v", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("şifreli metin çok kısa")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("şifre çözme hatası: %v", err)
	}

	return string(plaintext), nil
}

// IsEncrypted değerin ENC(...) ile sarılı olup olmadığını kontrol eder
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix) && strings.HasSuffix(value, encSuffix)
}

// DecryptIfEncrypted ENC(...) ile sarılıysa çözer, değilse olduğu gibi döner
func DecryptIfEncrypted(value, magicKey string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}

	// ENC(...) içindeki değeri çıkar
	encoded := value[len(encPrefix) : len(value)-len(encSuffix)]
	return Decrypt(encoded, magicKey)
}
