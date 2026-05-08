package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters tuned for interactive login.
const (
	argonMemory  = 64 * 1024 // 64 MiB
	argonTime    = 3
	argonThreads = 2
	argonSaltLen = 16
	argonHashLen = 32
)

// HashPassword hashes plain using argon2id and returns a PHC-formatted string.
// Format: $argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<hash-b64>
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonHashLen)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// VerifyPassword checks plain against an encoded PHC-format argon2id hash.
// Returns (true, nil) on match, (false, nil) on mismatch, (false, err) on parse failure.
func VerifyPassword(plain, encoded string) (bool, error) {
	salt, hash, m, t, p, err := parseArgon2PHC(encoded)
	if err != nil {
		return false, fmt.Errorf("password: parse hash: %w", err)
	}

	candidate := argon2.IDKey([]byte(plain), salt, t, m, p, uint32(len(hash)))
	return subtle.ConstantTimeCompare(hash, candidate) == 1, nil
}

// parseArgon2PHC parses a PHC-encoded argon2id string.
func parseArgon2PHC(encoded string) (salt, hash []byte, m, t uint32, p uint8, err error) {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 {
		err = errors.New("invalid PHC format: expected 6 parts")
		return
	}
	if parts[1] != "argon2id" {
		err = fmt.Errorf("unsupported algorithm %q", parts[1])
		return
	}

	var version int
	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil {
		err = fmt.Errorf("parse version: %w", scanErr)
		return
	}

	var mVal, tVal, pVal int
	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mVal, &tVal, &pVal); scanErr != nil {
		err = fmt.Errorf("parse params: %w", scanErr)
		return
	}
	m = uint32(mVal)
	t = uint32(tVal)
	p = uint8(pVal)

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		err = fmt.Errorf("decode salt: %w", err)
		return
	}

	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		err = fmt.Errorf("decode hash: %w", err)
		return
	}

	return salt, hash, m, t, p, nil
}
