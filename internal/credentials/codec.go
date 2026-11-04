package credentials

import (
	"crypto/rand"
	"errors"
)

// Encrypt authenticates the record binding so ciphertext cannot be moved to
// another account row without detection. Production callers keep keys external.
func Encrypt(key []byte, binding string, plain []byte) ([]byte, error) {
	if len(plain) > maxSize {
		return nil, errors.New("凭据超过大小限制")
	}
	gcm, err := aead(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	data := append([]byte("FTY1"), nonce...)
	return gcm.Seal(data, nonce, plain, []byte(binding)), nil
}
func Decrypt(key []byte, binding string, data []byte) ([]byte, error) {
	gcm, err := aead(key)
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize+64 || len(data) < 4+gcm.NonceSize()+gcm.Overhead() || string(data[:4]) != "FTY1" {
		return nil, errors.New("凭据格式错误")
	}
	nonce := data[4 : 4+gcm.NonceSize()]
	plain, err := gcm.Open(nil, nonce, data[4+gcm.NonceSize():], []byte(binding))
	if err != nil {
		return nil, errors.New("凭据解密失败：密钥、账号或数据不匹配")
	}
	return plain, nil
}
