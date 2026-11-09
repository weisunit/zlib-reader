// s-library: remix_userkey 的 AES-GCM 加解密与密钥解析（FR-1.3）。
// 密钥优先取 SL_ZLIB_SECRET 环境变量；未设置时生成随机密钥并持久化到
// <dataDir>/zlib-secret.key。dataDir 由调用方（业务层）传入 config.DataDir()，
// 保证本适配包不依赖 config 包（架构 §2.2 依赖约束）。
package zlibrary

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const secretFileName = "zlib-secret.key"

// LoadOrCreateSecret 解析加密密钥（32 字节）：
//  1. SL_ZLIB_SECRET env —— 值为 base64(32 字节) 时直接使用；否则取其 SHA-256；
//  2. 未设置：读 <dataDir>/zlib-secret.key；不存在则生成随机 32 字节写入（0600）。
func LoadOrCreateSecret(dataDir string) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv("SL_ZLIB_SECRET")); v != "" {
		if raw, err := base64.StdEncoding.DecodeString(v); err == nil && len(raw) == 32 {
			return raw, nil
		}
		if raw, err := base64.RawURLEncoding.DecodeString(v); err == nil && len(raw) == 32 {
			return raw, nil
		}
		sum := sha256.Sum256([]byte(v))
		return sum[:], nil
	}

	path := filepath.Join(dataDir, secretFileName)
	if data, err := os.ReadFile(path); err == nil {
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); err == nil && len(raw) == 32 {
			return raw, nil
		}
		return nil, fmt.Errorf("invalid secret file %s", path)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("persist secret file: %w", err)
	}
	return raw, nil
}

// EncryptUserKey AES-GCM 加密，返回 base64(nonce + ciphertext)。
func EncryptUserKey(plaintext string, key []byte) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptUserKey 解密 EncryptUserKey 的产物。
func DecryptUserKey(encoded string, key []byte) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
