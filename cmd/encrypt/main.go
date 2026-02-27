package main

import (
	"fmt"
	"log-server/crypto"
	"os"
)

// Kod içinde gömülü magic key ile aynı olmalı (config/config.go)
const magicKey = "!n0h0m-53cr3t-K3y-L0g-53rv3r"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Kullanım: go run cmd/encrypt/main.go <şifrelenecek_değer>")
		fmt.Println("Örnek:    go run cmd/encrypt/main.go password")
		os.Exit(1)
	}

	plaintext := os.Args[1]

	encrypted, err := crypto.Encrypt(plaintext, magicKey)
	if err != nil {
		fmt.Printf("Şifreleme hatası: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Plain:     %s\n", plaintext)
	fmt.Printf("Encrypted: ENC(%s)\n", encrypted)
	fmt.Println("\nBu değeri config.yaml'a yapıştırabilirsiniz.")
}
