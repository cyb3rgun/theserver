package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// hashDomain opens the hashed bytes, so the manifest hash cannot be taken for
// the hash of anything else.
const hashDomain = "cyb3rgun scenario manifest v1"

// Hash is the manifest hash (D-036): the SHA-256, in lower case hex, of the
// canonical form of m. The canonical form is the JSON encoding of the model
// under the names of the TOML file. It writes maps, the files table with the
// hash of every file among them, in the order of their keys, so neither the
// order nor the layout of the TOML file changes the hash, while any other
// change of the manifest or of a listed file does. Lists keep their order,
// which matters: the first zone wins where zones overlap.
func Hash(m Manifest) string {
	canonical, err := json.Marshal(m)
	if err != nil {
		// The model holds strings, numbers, booleans, lists and maps with
		// string keys, which always encode.
		panic("scenario: the manifest cannot be encoded: " + err.Error())
	}
	h := sha256.New()
	h.Write([]byte(hashDomain))
	h.Write([]byte{0})
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}
