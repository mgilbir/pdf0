package pdf0

import (
	"fmt"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
)

// TestImportLongReferenceChain: a page whose graph is a chain of 200000
// indirect objects, each naming the next. A copier that recursed from one
// indirect object into the next needed stack frames per link and died of a
// fatal stack overflow; the importer copies indirect objects from a work list,
// so its depth is bounded by the nesting of one object, which the parser caps.
func TestImportLongReferenceChain(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 1 << 30, Timeout: 2 * time.Minute, MaxStack: 16 << 20}, func(t *testing.T) {
		const links = 200000
		objs := flatTreeObjects(1)
		objs[100] = `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 200 0 R /Resources << /Font << /F1 20 0 R >> >> /PieceInfo 1000 0 R >>`
		for i := 0; i < links; i++ {
			objs[1000+i] = fmt.Sprintf("<< /N %d 0 R >>", 1001+i)
		}
		objs[1000+links] = "<< >>"
		src := readAssembled(t, objs)
		out, _, err := src.ExtractPages([]int{0})
		if err != nil {
			t.Fatal(err)
		}
		// Every link came along: walk the copy's chain to its end.
		node := out.ResolveDict(out.PageList()[0].Get("PieceInfo"))
		n := 0
		for node != nil && node.Get("N") != nil {
			node = out.ResolveDict(node.Get("N"))
			n++
		}
		if n != links {
			t.Fatalf("the copied chain has %d links, want %d", n, links)
		}
	})
}
