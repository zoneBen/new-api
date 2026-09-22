package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

func GenerateHMACWithKey(key []byte, data string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GenerateHMAC(data string) string {
	h := hmac.New(sha256.New, []byte(CryptoSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func Password2Hash(password string) (string, error) {
	passwordBytes := []byte(password)
	hashedPassword, err := bcrypt.GenerateFromPassword(passwordBytes, bcrypt.DefaultCost)
	return string(hashedPassword), err
}

func ValidatePasswordAndHash(password string, hash string) bool {
	if strings.HasPrefix(hash, "$argon2id$") {
		return validateArgon2AccountPassword(password, hash)
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// placeholderAccountPassword is never accepted as a real credential: the hash
// derived from it is only ever passed as the "stored" side of a comparison.
const placeholderAccountPassword = "placeholder-account-password"

// Placeholder hashes are cached per algorithm because deriving one costs a full
// key derivation, and the app must not pay that on every failed login.
var (
	placeholderHashMutex sync.Mutex
	placeholderHashCache = map[string]string{}
)

func placeholderAccountPasswordHash(algorithm string) string {
	placeholderHashMutex.Lock()
	defer placeholderHashMutex.Unlock()
	if hash, ok := placeholderHashCache[algorithm]; ok {
		return hash
	}

	hash := ""
	if derived, err := hashAccountPassword(placeholderAccountPassword, algorithm); err != nil {
		SysError(fmt.Sprintf(
			"cannot derive a placeholder account password hash for ACCOUNT_PASSWORD_HASH_ALGORITHM=%q; login responses will not have a uniform cost: %v",
			algorithm,
			err,
		))
	} else {
		hash = derived
	}
	placeholderHashCache[algorithm] = hash
	return hash
}

// EqualizePasswordVerificationCost spends the same key-derivation work as a real
// verification against a placeholder hash. Authentication must call it on every
// miss (unknown account, or an account without a stored password) so response
// time cannot be used to enumerate accounts or to detect accounts that cannot
// log in with a password.
//
// The cost follows the configured storage algorithm, so it matches accounts
// stored in that format. During a rolling algorithm migration a legacy account
// still hashes with the other algorithm, and its timing remains distinguishable.
func EqualizePasswordVerificationCost(password string) {
	if hash := placeholderAccountPasswordHash(os.Getenv("ACCOUNT_PASSWORD_HASH_ALGORITHM")); hash != "" {
		ValidatePasswordAndHash(password, hash)
	}
}
