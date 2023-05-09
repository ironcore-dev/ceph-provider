// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
)

type Encryptor interface {
	Encrypt(key []byte) ([]byte, error)
	Decrypt(encryptedKey []byte) ([]byte, error)
}

func NewAesGcmEncryptor(kekPath string) (Encryptor, error) {
	kek, err := os.ReadFile(kekPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read kek file: %w", err)
	}

	if len(kek) != 32 {
		return nil, fmt.Errorf("invalid key size: key encryption key (%d) is not 32 bit", len(kek))
	}

	cipherBlock, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(cipherBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to aes-gcm: %w", err)
	}

	return &encryptor{gcm: gcm}, nil
}

type encryptor struct {
	gcm cipher.AEAD
}

func (e *encryptor) Encrypt(key []byte) ([]byte, error) {
	// init random initialization vector
	iv := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("failed to create initialization vector: %w", err)
	}

	return e.gcm.Seal(iv, iv, key, nil), nil
}

func (e *encryptor) Decrypt(encryptedKey []byte) ([]byte, error) {
	ivSize := e.gcm.NonceSize()
	if len(encryptedKey) < ivSize {
		return nil, fmt.Errorf("encrypted key length (%d) must be longer than initialization vector (%d)", len(encryptedKey), ivSize)
	}

	iv, ek := encryptedKey[:ivSize], encryptedKey[ivSize:]
	key, err := e.gcm.Open(nil, iv, ek, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key: %w", err)
	}

	return key, nil
}
