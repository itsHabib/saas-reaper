package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// GenesisHash is the previous hash of every tenant's first entry.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Link computes the chained hash of one entry:
// hex(SHA-256(canonical(entry) || hex(previousHash))), where previousHash is the
// 64-character lowercase hexadecimal head before this entry (or GenesisHash).
func Link(previousHash string, entry Entry) (string, error) {
	if err := validateHash(previousHash); err != nil {
		return "", err
	}
	canonical, err := CanonicalEntry(entry)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	digest.Write(canonical)
	digest.Write([]byte(previousHash))
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// Verify recomputes a tenant chain from genesis and returns its head. Entries
// must arrive in sequence order starting at one; the first disagreement between a
// recorded hash or sequence and the recomputed chain is reported with its position.
func Verify(entries []Entry) (Head, error) {
	head := Head{Sequence: 0, Hash: GenesisHash}
	for _, entry := range entries {
		if entry.Sequence != head.Sequence+1 {
			return head, fmt.Errorf("%w: sequence %d follows %d", ErrBroken, entry.Sequence, head.Sequence)
		}
		if entry.PreviousHash != "" && entry.PreviousHash != head.Hash {
			return head, fmt.Errorf("%w: sequence %d records a foreign previous hash", ErrBroken, entry.Sequence)
		}
		expected, err := Link(head.Hash, entry)
		if err != nil {
			return head, fmt.Errorf("%w: sequence %d: %w", ErrBroken, entry.Sequence, err)
		}
		if expected != entry.Hash {
			return head, fmt.Errorf("%w: sequence %d hash mismatch", ErrBroken, entry.Sequence)
		}
		head = Head{Sequence: entry.Sequence, Hash: entry.Hash}
	}
	return head, nil
}

func validateHash(value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("previous hash %q is not 64 lowercase hex characters", value)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("previous hash %q is not hexadecimal: %w", value, err)
	}
	return nil
}
