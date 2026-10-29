package auth

import (
	"errors"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
)

var ErrPassword = errors.New("password must contain at least 15 characters and at most 1024 UTF-8 bytes")

var passwordParams = &argon2id.Params{
	Memory:      19 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

func HashPassword(password string) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 || len(password) > 1024 {
		return "", ErrPassword
	}
	return argon2id.CreateHash(password, passwordParams)
}

func verifyPassword(password, hash string) (bool, error) {
	if len(password) > 1024 {
		return false, nil
	}
	return argon2id.ComparePasswordAndHash(password, hash)
}
