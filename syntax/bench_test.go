package syntax

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// largeDictSource renders a dictionary with n distinct keys K00..K(n-1).
func largeDictSource(n int) []byte {
	var b bytes.Buffer
	b.WriteString("<<")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " /K%02d %d", i, i)
	}
	b.WriteString(" >>")
	return b.Bytes()
}

// BenchmarkParseLargeDict parses a dictionary of n distinct keys and then
// looks every key up once: the parser's build path plus the read path the
// validators take. Both must stay linear in n.
func BenchmarkParseLargeDict(b *testing.B) {
	for _, n := range []int{16, 1_000, 100_000} {
		src := largeDictSource(n)
		keys := make([]object.Name, n)
		for i := range keys {
			keys[i] = object.Name(fmt.Sprintf("K%02d", i))
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))
			for b.Loop() {
				obj, err := NewParser(src).ParseObject()
				if err != nil {
					b.Fatal(err)
				}
				d := obj.(*object.Dictionary)
				for _, k := range keys {
					if d.Get(k) == nil {
						b.Fatal("missing key")
					}
				}
			}
		})
	}
}
