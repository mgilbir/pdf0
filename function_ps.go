package pdf0

import (
	"math"
	"strconv"
)

// This file evaluates type-4 (PostScript calculator) PDF functions. The decoded
// stream is a single { ... } program using a small arithmetic/boolean/stack
// operator set (ISO 32000-1 clause 7.10.5).

// psItem is one parsed program element: a number, an operator name or a nested
// procedure ({ ... } block).
type psItem struct {
	isNum  bool
	num    float64
	op     string
	isProc bool
	proc   []psItem
}

// psVal is a value on the operand stack: a number, a boolean or a procedure.
type psVal struct {
	num    float64
	bl     bool
	isBool bool
	proc   []psItem
	isProc bool
}

// evalType4 evaluates a PostScript calculator function.
func evalType4(d *Document, stream *Stream, dict *Dictionary, x []float64) ([]float64, bool) {
	prog, ok := psProgram(d, stream)
	if !ok {
		return nil, false
	}
	rng := floatArray(d, dict.Get("Range"))
	n := len(rng) / 2
	if n == 0 {
		return nil, false
	}
	st := make([]psVal, 0, 16)
	for _, v := range x {
		st = append(st, psVal{num: v})
	}
	budget := psBudget{max: d.lim().postScriptSteps}
	st, ok = psExec(prog, st, 0, &budget)
	if !ok || len(st) < n {
		return nil, false
	}
	// The top n numbers are the outputs, in stack order (bottom-to-top).
	out := make([]float64, n)
	base := len(st) - n
	for j := 0; j < n; j++ {
		v := st[base+j]
		if v.isProc {
			return nil, false
		}
		if v.isBool {
			if v.bl {
				out[j] = 1
			}
		} else {
			out[j] = v.num
		}
	}
	return out, true
}

// psProgEntry is a memoized psProgram result.
type psProgEntry struct {
	items []psItem
	ok    bool
}

// psProgram tokenizes and parses the decoded stream into the body of the outer
// { } procedure. The result is memoized in the per-run cache when one is
// installed: tint transforms evaluate once per image pixel, and re-decoding
// and re-parsing the program stream each time made a small image take minutes.
func psProgram(d *Document, stream *Stream) ([]psItem, bool) {
	if c := d.valCache; c != nil {
		if e, hit := c.image.psProgs[stream]; hit {
			return e.items, e.ok
		}
	}
	items, ok := parsePSProgram(d, stream)
	if c := d.valCache; c != nil {
		if c.image.psProgs == nil {
			c.image.psProgs = make(map[*Stream]psProgEntry)
		}
		c.image.psProgs[stream] = psProgEntry{items, ok}
	}
	return items, ok
}

func parsePSProgram(d *Document, stream *Stream) ([]psItem, bool) {
	data := decodeContentStream(d, stream)
	toks := psTokenize(data)
	items, rest, ok := psParseProc(toks)
	if !ok || len(rest) != 0 {
		// The whole program is expected to be a single procedure.
		return nil, false
	}
	return items, true
}

// psParseProc parses a { ... } procedure starting at toks[0] == "{" and returns
// its body items plus the remaining tokens.
func psParseProc(toks []string) (items []psItem, rest []string, ok bool) {
	if len(toks) == 0 || toks[0] != "{" {
		return nil, nil, false
	}
	toks = toks[1:]
	for len(toks) > 0 {
		t := toks[0]
		switch t {
		case "}":
			return items, toks[1:], true
		case "{":
			sub, r, k := psParseProc(toks)
			if !k {
				return nil, nil, false
			}
			items = append(items, psItem{isProc: true, proc: sub})
			toks = r
		default:
			if f, err := strconv.ParseFloat(t, 64); err == nil {
				items = append(items, psItem{isNum: true, num: f})
			} else {
				items = append(items, psItem{op: t})
			}
			toks = toks[1:]
		}
	}
	return nil, nil, false // unterminated procedure
}

// psTokenize splits a PostScript calculator program into tokens: braces are
// their own tokens, whitespace separates, and % begins a line comment.
func psTokenize(data []byte) []string {
	var toks []string
	i := 0
	for i < len(data) {
		c := data[i]
		switch {
		case c == '%':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case c == '{' || c == '}':
			toks = append(toks, string(c))
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0:
			i++
		default:
			start := i
			for i < len(data) {
				b := data[i]
				if b == '{' || b == '}' || b == '%' || b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0 {
					break
				}
				i++
			}
			toks = append(toks, string(data[start:i]))
		}
	}
	return toks
}

// psBudget accumulates the operator count across a whole (recursive) type-4
// (PostScript calculator) evaluation and carries the ceiling it is checked
// against. The depth and stack caps alone do not stop an if/ifelse program that
// fans out to exponentially many operators while staying shallow and
// small-stacked; since a tint function runs once per pixel over an image, an
// unbounded program is a CPU DoS (audit C21). The ceiling defaults to
// defaultMaxPostScriptSteps; a caller can change it with
// WithMaxPostScriptSteps.
type psBudget struct {
	steps int
	max   int
}

// psExec executes program items against the operand stack.
func psExec(items []psItem, st []psVal, depth int, budget *psBudget) ([]psVal, bool) {
	if depth > maxFunctionDepth {
		return nil, false
	}
	const maxStack = 4096
	for _, it := range items {
		budget.steps++
		if budget.steps > budget.max {
			return nil, false
		}
		if len(st) > maxStack {
			return nil, false
		}
		switch {
		case it.isNum:
			st = append(st, psVal{num: it.num})
		case it.isProc:
			st = append(st, psVal{isProc: true, proc: it.proc})
		default:
			var ok bool
			st, ok = psApply(it.op, st, depth, budget)
			if !ok {
				return nil, false
			}
		}
	}
	return st, true
}

// psApply executes a single operator.
func psApply(op string, st []psVal, depth int, budget *psBudget) ([]psVal, bool) {
	// Helpers for popping numbers and booleans.
	popNum := func() (float64, bool) {
		if len(st) == 0 {
			return 0, false
		}
		v := st[len(st)-1]
		if v.isProc || v.isBool {
			return 0, false
		}
		st = st[:len(st)-1]
		return v.num, true
	}
	pushNum := func(f float64) { st = append(st, psVal{num: f}) }
	pushBool := func(b bool) { st = append(st, psVal{isBool: true, bl: b}) }

	switch op {
	// --- arithmetic ---
	case "add", "sub", "mul", "div", "idiv", "mod", "atan", "exp":
		b, ok1 := popNum()
		a, ok2 := popNum()
		if !ok1 || !ok2 {
			return nil, false
		}
		switch op {
		case "add":
			pushNum(a + b)
		case "sub":
			pushNum(a - b)
		case "mul":
			pushNum(a * b)
		case "div":
			if b == 0 {
				return nil, false
			}
			pushNum(a / b)
		case "idiv":
			if int64(b) == 0 {
				return nil, false
			}
			pushNum(float64(int64(a) / int64(b)))
		case "mod":
			if int64(b) == 0 {
				return nil, false
			}
			pushNum(float64(int64(a) % int64(b)))
		case "atan":
			ang := math.Atan2(a, b) * 180 / math.Pi
			if ang < 0 {
				ang += 360
			}
			pushNum(ang)
		case "exp":
			r := math.Pow(a, b)
			if math.IsNaN(r) || math.IsInf(r, 0) {
				return nil, false
			}
			pushNum(r)
		}
	case "neg", "abs", "sqrt", "sin", "cos", "ln", "log", "cvi", "cvr",
		"floor", "ceiling", "round", "truncate":
		a, ok := popNum()
		if !ok {
			return nil, false
		}
		switch op {
		case "neg":
			pushNum(-a)
		case "abs":
			pushNum(math.Abs(a))
		case "sqrt":
			if a < 0 {
				return nil, false
			}
			pushNum(math.Sqrt(a))
		case "sin":
			pushNum(math.Sin(a * math.Pi / 180))
		case "cos":
			pushNum(math.Cos(a * math.Pi / 180))
		case "ln":
			if a <= 0 {
				return nil, false
			}
			pushNum(math.Log(a))
		case "log":
			if a <= 0 {
				return nil, false
			}
			pushNum(math.Log10(a))
		case "cvi":
			pushNum(math.Trunc(a))
		case "cvr":
			pushNum(a)
		case "floor":
			pushNum(math.Floor(a))
		case "ceiling":
			pushNum(math.Ceil(a))
		case "round":
			pushNum(math.Round(a))
		case "truncate":
			pushNum(math.Trunc(a))
		}

	// --- comparison ---
	case "eq", "ne", "gt", "ge", "lt", "le":
		// Comparisons operate on numbers (booleans compare as numbers too).
		if len(st) < 2 {
			return nil, false
		}
		bv := st[len(st)-1]
		av := st[len(st)-2]
		if bv.isProc || av.isProc {
			return nil, false
		}
		st = st[:len(st)-2]
		a, b := valNum(av), valNum(bv)
		switch op {
		case "eq":
			pushBool(a == b)
		case "ne":
			pushBool(a != b)
		case "gt":
			pushBool(a > b)
		case "ge":
			pushBool(a >= b)
		case "lt":
			pushBool(a < b)
		case "le":
			pushBool(a <= b)
		}

	// --- boolean / bitwise ---
	case "and", "or", "xor":
		if len(st) < 2 {
			return nil, false
		}
		bv := st[len(st)-1]
		av := st[len(st)-2]
		st = st[:len(st)-2]
		if av.isBool && bv.isBool {
			switch op {
			case "and":
				pushBool(av.bl && bv.bl)
			case "or":
				pushBool(av.bl || bv.bl)
			case "xor":
				pushBool(av.bl != bv.bl)
			}
		} else if !av.isProc && !bv.isProc {
			ai, bi := int64(av.num), int64(bv.num)
			switch op {
			case "and":
				pushNum(float64(ai & bi))
			case "or":
				pushNum(float64(ai | bi))
			case "xor":
				pushNum(float64(ai ^ bi))
			}
		} else {
			return nil, false
		}
	case "not":
		if len(st) == 0 {
			return nil, false
		}
		v := st[len(st)-1]
		st = st[:len(st)-1]
		if v.isBool {
			pushBool(!v.bl)
		} else if !v.isProc {
			pushNum(float64(^int64(v.num)))
		} else {
			return nil, false
		}
	case "bitshift":
		shift, ok1 := popNum()
		val, ok2 := popNum()
		if !ok1 || !ok2 {
			return nil, false
		}
		s := int64(shift)
		iv := int64(val)
		if s >= 0 {
			pushNum(float64(iv << uint(s)))
		} else {
			pushNum(float64(iv >> uint(-s)))
		}
	case "true":
		pushBool(true)
	case "false":
		pushBool(false)

	// --- stack manipulation ---
	case "pop":
		if len(st) == 0 {
			return nil, false
		}
		st = st[:len(st)-1]
	case "exch":
		if len(st) < 2 {
			return nil, false
		}
		st[len(st)-1], st[len(st)-2] = st[len(st)-2], st[len(st)-1]
	case "dup":
		if len(st) == 0 {
			return nil, false
		}
		st = append(st, st[len(st)-1])
	case "copy":
		nf, ok := popNum()
		if !ok {
			return nil, false
		}
		n := int(nf)
		if n < 0 || n > len(st) {
			return nil, false
		}
		st = append(st, st[len(st)-n:]...)
	case "index":
		nf, ok := popNum()
		if !ok {
			return nil, false
		}
		n := int(nf)
		if n < 0 || n >= len(st) {
			return nil, false
		}
		st = append(st, st[len(st)-1-n])
	case "roll":
		jf, ok1 := popNum()
		nf, ok2 := popNum()
		if !ok1 || !ok2 {
			return nil, false
		}
		n := int(nf)
		j := int(jf)
		if n < 0 || n > len(st) {
			return nil, false
		}
		if n > 0 {
			j = ((j % n) + n) % n
			seg := st[len(st)-n:]
			rolled := make([]psVal, n)
			for i := 0; i < n; i++ {
				rolled[(i+j)%n] = seg[i]
			}
			copy(seg, rolled)
		}

	// --- control ---
	case "if":
		if len(st) < 2 {
			return nil, false
		}
		procV := st[len(st)-1]
		condV := st[len(st)-2]
		if !procV.isProc || !condV.isBool {
			return nil, false
		}
		st = st[:len(st)-2]
		if condV.bl {
			var ok bool
			st, ok = psExec(procV.proc, st, depth+1, budget)
			if !ok {
				return nil, false
			}
		}
	case "ifelse":
		if len(st) < 3 {
			return nil, false
		}
		proc2 := st[len(st)-1]
		proc1 := st[len(st)-2]
		condV := st[len(st)-3]
		if !proc2.isProc || !proc1.isProc || !condV.isBool {
			return nil, false
		}
		st = st[:len(st)-3]
		var ok bool
		if condV.bl {
			st, ok = psExec(proc1.proc, st, depth+1, budget)
		} else {
			st, ok = psExec(proc2.proc, st, depth+1, budget)
		}
		if !ok {
			return nil, false
		}

	default:
		return nil, false
	}
	return st, true
}

// valNum returns the numeric value of a stack value, treating booleans as 1/0.
func valNum(v psVal) float64 {
	if v.isBool {
		if v.bl {
			return 1
		}
		return 0
	}
	return v.num
}
