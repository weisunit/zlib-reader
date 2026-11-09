// s-library: AES-GCM 加解密与密钥解析单测（FR-1.3）。
package zlibrary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptUserKeyRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	plain := "remix-userkey-abcdef-0123456789"

	enc, err := EncryptUserKey(plain, key)
	if err != nil {
		t.Fatalf("EncryptUserKey: %v", err)
	}
	// 密文不应包含明文
	if strings.Contains(enc, plain) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := DecryptUserKey(enc, key)
	if err != nil {
		t.Fatalf("DecryptUserKey: %v", err)
	}
	if got != plain {
		t.Fatalf("roundtrip = %q, want %q", got, plain)
	}

	// 篡改密文应失败
	bad := []byte(enc)
	bad[len(bad)-1] ^= 0xFF
	if _, err := DecryptUserKey(string(bad), key); err == nil {
		t.Fatal("tampered ciphertext should fail")
	}
	// 错误密钥应失败
	if _, err := DecryptUserKey(enc, []byte(strings.Repeat("x", 32))); err == nil {
		t.Fatal("wrong key should fail")
	}
}

func TestLoadOrCreateSecretFromEnv(t *testing.T) {
	t.Setenv("SL_ZLIB_SECRET", "my-custom-secret-passphrase")
	key, err := LoadOrCreateSecret(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key len = %d, want 32", len(key))
	}
	// 相同 env 应得到相同密钥（稳定派生）
	key2, _ := LoadOrCreateSecret(t.TempDir())
	if string(key) != string(key2) {
		t.Fatal("env-derived key not stable")
	}
}

func TestLoadOrCreateSecretPersistsToFile(t *testing.T) {
	t.Setenv("SL_ZLIB_SECRET", "")
	dir := t.TempDir()

	key1, err := LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	// 文件已生成且权限 0600
	path := filepath.Join(dir, "zlib-secret.key")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("secret file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret file perm = %v, want 0600", info.Mode().Perm())
	}
	// 第二次读取同一目录应得到同一密钥
	key2, err := LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if string(key1) != string(key2) {
		t.Fatal("persisted key not stable")
	}
	// 不同目录应得到不同密钥
	key3, _ := LoadOrCreateSecret(t.TempDir())
	if string(key1) == string(key3) {
		t.Fatal("random keys should differ across data dirs")
	}
}

func TestLoadOrCreateSecretAcceptsBase64Key(t *testing.T) {
	// 32 字节全 0x07 的 base64
	b64 := "BwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	t.Setenv("SL_ZLIB_SECRET", b64)
	key, err := LoadOrCreateSecret(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	if key[0] != 0x07 || len(key) != 32 {
		t.Fatalf("key = %x", key)
	}
}
