package dht

import (
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"

	"inu/cid"
)

const keyLen = 32 // 32 * 8 = 256

type Key [keyLen]byte

func ParseCID(cid cid.CID) (Key, error) {
	k := Key{}
	return k, k.UnmarshalB32(string(cid))
}

func ParseUint64(n uint64) Key {
	// Encode the number to binary
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, n)

	// Copy this onto the key
	k := Key{}
	copy(k[keyLen-len(buf):], buf[:])
	return k
}

func (k Key) String() string {
	return k.MarshalB32()
}

func fromBigInt(n *big.Int) Key {
	k := Key{}
	buf := n.Bytes()
	copy(k[keyLen-len(buf):], buf[:])

	return k
}

func (k Key) xor(v Key) Key {
	r := Key{}
	for i := range r {
		r[i] = k[i] ^ v[i]
	}

	return r
}

func (k Key) cmp(v Key) int {
	for i := range k {
		if k[i] < v[i] {
			return -1
		} else if k[i] > v[i] {
			return 1
		}
	}

	return 0
}

func (k Key) MarshalB32() string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(k[:])
}

func (k *Key) UnmarshalB32(s string) error {
	if len(s) != 52 {
		return fmt.Errorf("invalid key length: %d", len(s))
	}

	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		return err
	}

	copy(k[:], b)
	return nil
}

func (k Key) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.MarshalB32())
}

func (k *Key) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	return k.UnmarshalB32(s)
}
