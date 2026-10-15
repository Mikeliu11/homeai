package records

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ContentHash is a stable hash of a value's JSON form. Go's encoder emits
// struct fields in declaration order and map keys sorted, so equal values
// hash equal.
func ContentHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NewID returns a random 12-byte hex id.
func NewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
