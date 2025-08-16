package fn

import (
	"encoding/hex"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// HashPassword 使用 bcrypt 对密码进行哈希处理
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func ScryptHash(password, salt string) (string, error) {
	dk, err := scrypt.Key([]byte(password), []byte(salt), 32768, 8, 1, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(dk), nil
}
