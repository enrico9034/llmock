package engine

import "strings"

const passage = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. " +
	"Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. " +
	"Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. " +
	"Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum."

var (
	words  = strings.Split(passage, " ")
	spaced = func() []string {
		s := make([]string, len(words))
		for i, w := range words {
			s[i] = " " + w
		}
		return s
	}()
)

// AppendTokens appends tokens [from, to) of the lorem ipsum stream to dst.
// Token 0 has no leading space; every later token has exactly one. Unseeded
// streams cycle through the passage in order; seeded streams pick each word
// from a splitmix64 hash of the seed and the token index.
func AppendTokens(dst []byte, from, to int, seed int64, seeded bool) []byte {
	for i := from; i < to; i++ {
		idx := i % len(words)
		if seeded {
			idx = int(splitmix64(uint64(seed)+uint64(i)*0x9E3779B97F4A7C15) % uint64(len(words)))
		}
		if i == 0 {
			dst = append(dst, words[idx]...)
		} else {
			dst = append(dst, spaced[idx]...)
		}
	}
	return dst
}

func splitmix64(x uint64) uint64 {
	z := x + 0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}
