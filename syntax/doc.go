// Package syntax reads and writes PDF objects as bytes: the lexical
// conventions and object syntax of ISO 32000-2 7.2 and 7.3. Most callers never
// import it, because pdf0.Read and pdf0.Document.Write drive it over a whole
// file, with its cross-reference sections and trailer. Reach for it to parse
// or emit individual objects — a dictionary from a fragment of a file, a value
// for a tool that writes PDF syntax itself.
//
// There are three layers, each over an in-memory byte slice:
//
//   - Lexer turns bytes into Tokens: numbers, names, literal and hex strings,
//     delimiters and keywords. NewLexerFromReaderAt reads an io.ReaderAt into
//     memory first (ReadSource).
//   - Parser turns tokens into object.Object values (ParseObject) and indirect
//     object definitions (ParseIndirectObject), including stream bodies.
//   - Serializer writes object values back, tracking the byte offset a caller
//     needs for cross-reference entries. What it writes, the parser reads back
//     as an equal object.
//
// For example:
//
//	obj, err := syntax.NewParser([]byte("<< /Type /Page /Rotate 90 >>")).ParseObject()
//	if err != nil {
//		return err
//	}
//	err = syntax.NewSerializer(w).WriteObject(obj)
//
// The decoding helpers (DecodeLiteralString, DecodeHexString, DecodeName and
// ParseNumber) are the module's one definition of those grammars, shared by
// the object parser and the content-stream reader. The input is untrusted:
// recursion in the parser and the serializer is depth-capped, the lexer's
// skipping of whitespace and comments is bounded, and Parser.Budget can meter
// the memory a parse materialises.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/architecture.md describes how
// Read and Write use this package.
package syntax
