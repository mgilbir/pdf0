package render

// The user-agent stylesheet: what makes a <p> a block and a <b> bold before any
// author has said anything.
//
// It is written as CSS and parsed like any other stylesheet rather than being
// compiled into a table of defaults, and that is worth a sentence. A table would
// be faster to load and would put these rules outside the cascade — so an author
// writing "p { display: inline }" would be fighting something that is not a
// stylesheet, and the origin ordering that makes their rule win would have
// nothing to order. Keeping it as CSS means the defaults lose to the author for
// exactly the reason everything else does.
//
// It is deliberately smaller than a browser's. A browser's has to style
// elements this engine refuses — form controls, media, the interactive ones —
// and rules for those would be dead weight that reads as coverage.

// UserAgentCSS is the default stylesheet.
//
// The lengths are the ones every browser converged on, and the em-based ones
// are em on purpose: a document that sets a larger font gets proportionally
// larger spacing, which is what an author expects and what a fixed pixel margin
// quietly fails to do.
const UserAgentCSS = `
/* The elements that produce no box at all. */
head, title, meta, link, base, style { display: none }

/* Block-level structure. */
html, body, div, p, blockquote, figure, figcaption, address,
header, footer, nav, section, article, aside, main, hgroup,
h1, h2, h3, h4, h5, h6, ul, ol, dl, dt, dd, pre, hr { display: block }

li { display: list-item; counter-increment: list-item }
/* Each list creates its own counter, which is what makes a nested list start
   again at one while the list around it carries on. "list-item" is the name CSS
   Lists reserves for exactly this. */
ol, ul, menu, dir { counter-reset: list-item }

/* Tables. The display values are the ones the table algorithm keys off, so
   these are not decoration: a <tr> that were left inline would not be a row. */
table { display: table; border-collapse: separate; border-spacing: 2px }
caption { display: table-caption; text-align: center }
colgroup { display: table-column-group }
col { display: table-column }
thead { display: table-header-group }
tbody { display: table-row-group }
tfoot { display: table-footer-group }
tr { display: table-row }
td, th { display: table-cell; padding: 1px }
th { font-weight: bold; text-align: center }
/* A cell's vertical alignment comes down the table rather than from the
   property's own initial value, which is HTML's rendering section 15.3.8 and
   the reason a cell's content sits in the middle of a tall row by default:

       thead, tbody, tfoot, table > tr { vertical-align: middle }
       tr, td, th                      { vertical-align: inherit }

   The inherit is what carries an author's "vertical-align: top" on a row to
   the cells in it — vertical-align does not inherit on its own, so a UA rule
   naming the cells directly would win over the author's rule on the row and
   the declaration would do nothing. */
thead, tbody, tfoot, table > tr { vertical-align: middle }
tr, td, th { vertical-align: inherit }

/* Vertical rhythm. Margins are em so that spacing follows the type size. */
p, blockquote, figure, ul, ol, dl, pre { margin-top: 1em; margin-bottom: 1em }
blockquote { margin-left: 40px; margin-right: 40px }
figure { margin-left: 40px; margin-right: 40px }
ul, ol { padding-left: 40px }
dd { margin-left: 40px }

h1 { font-size: 2em; margin-top: 0.67em; margin-bottom: 0.67em; font-weight: bold }
h2 { font-size: 1.5em; margin-top: 0.83em; margin-bottom: 0.83em; font-weight: bold }
h3 { font-size: 1.17em; margin-top: 1em; margin-bottom: 1em; font-weight: bold }
h4 { font-size: 1em; margin-top: 1.33em; margin-bottom: 1.33em; font-weight: bold }
h5 { font-size: 0.83em; margin-top: 1.67em; margin-bottom: 1.67em; font-weight: bold }
h6 { font-size: 0.67em; margin-top: 2.33em; margin-bottom: 2.33em; font-weight: bold }

body { margin-top: 8px; margin-right: 8px; margin-bottom: 8px; margin-left: 8px }

hr {
  margin-top: 0.5em; margin-bottom: 0.5em;
  border-top-width: 1px; border-right-width: 1px;
  border-bottom-width: 1px; border-left-width: 1px;
  border-top-style: inset; border-right-style: inset;
  border-bottom-style: inset; border-left-style: inset;
}

/* Lists. */
ul { list-style-type: disc }
ol { list-style-type: decimal }

/* Text-level semantics. */
b, strong { font-weight: bold }
i, em, cite, var, dfn, address { font-style: italic }
code, kbd, samp, pre { font-family: monospace }
pre { white-space: pre }
small { font-size: 0.83em }
sub, sup { font-size: 0.83em }
sub { vertical-align: sub }
sup { vertical-align: super }
u, ins { text-decoration-line: underline }
s, del { text-decoration-line: line-through }
mark { background-color: yellow; color: black }
/* Links, and only links. HTML's rendering section writes this as ":link,
   :visited", and :link matches an <a> *that has an href* — an <a> without one
   is an anchor and not a link, and browsers leave it the colour of its
   surroundings. Writing it as a bare "a" was measurably wrong: the suite is full
   of empty <a> elements used as a place to hang a pseudo-element on, and every
   one of them came out blue and underlined against a reference that had no such
   thing. */
a[href] { text-decoration-line: underline; color: #0000ee }

/* Ruby annotations sit above their base text; the sizing is the only part of
   that this engine can express yet. */
rt { font-size: 0.5em; vertical-align: super }

/* Bidirectional overrides are the two elements whose whole purpose is to change
   the direction, so they say so rather than inheriting it. */
bdo { unicode-bidi: bidi-override }
bdi { unicode-bidi: isolate }

/* The dir attribute, which is how nearly every document in the world states a
   direction — a stylesheet saying so is the exception. HTML's own rendering
   section defines it as these declarations, and the isolation is part of the
   definition rather than an extra: an element that says which way it runs must
   not reorder the text around it.

   dir=auto is the first-strong rule over the element's own content, which is
   what unicode-bidi: plaintext is; it deliberately leaves direction alone,
   because the content decides. */
[dir="ltr"] { direction: ltr; unicode-bidi: isolate }
[dir="rtl"] { direction: rtl; unicode-bidi: isolate }
[dir="auto"] { unicode-bidi: plaintext }
bdo[dir] { unicode-bidi: isolate-override }

/* Form controls, as the static boxes a printed page has. control.go carries the
   half of this that CSS cannot say — an intrinsic size in characters and lines,
   and the text a control shows — and says what is approximated and reported.

   The display values and "white-space: pre-wrap" are HTML's rendering section.
   The chrome is not: the specification leaves a control's border, padding and
   colours to the user agent, and every one below is the shape desktop browsers
   converged on. It is here rather than left off because a text field with no
   border is invisible on paper, which is a page that has quietly lost a
   control rather than one that shows an approximate one. */
form, fieldset, legend, optgroup, option { display: block }
input, button, select, textarea { display: inline-block }
param { display: none }
input[type="hidden" i] { display: none }

/* A <textarea> is preserved white space that wraps, which is the one rule in
   here that changes what the text says rather than how it looks. */
textarea { white-space: pre-wrap; overflow-x: auto; overflow-y: auto }

/* The text-entry chrome. A <select> is drawn as a field rather than as a
   drop-down, because the arrow is a widget and this is paper. */
input, textarea, select {
  border-top: 1px solid #767676; border-right: 1px solid #767676;
  border-bottom: 1px solid #767676; border-left: 1px solid #767676;
  padding-top: 1px; padding-right: 2px; padding-bottom: 1px; padding-left: 2px;
  background-color: #ffffff;
}

/* The push buttons, whose label is centred in a raised box. */
button,
input[type="submit" i], input[type="reset" i], input[type="button" i] {
  text-align: center;
  padding-top: 1px; padding-right: 6px; padding-bottom: 1px; padding-left: 6px;
  border-top: 2px outset #c0c0c0; border-right: 2px outset #c0c0c0;
  border-bottom: 2px outset #c0c0c0; border-left: 2px outset #c0c0c0;
  background-color: #f0f0f0;
}

/* A checkbox and a radio are a small fixed square. HTML sizes these from the
   border box, which is why the size does not move when the border does. */
input[type="checkbox" i], input[type="radio" i] {
  box-sizing: border-box; width: 13px; height: 13px;
  margin-top: 3px; margin-right: 3px; margin-bottom: 0; margin-left: 4px;
  padding-top: 0; padding-right: 0; padding-bottom: 0; padding-left: 0;
}

/* An image button is a picture, so it carries none of the field's chrome. */
input[type="image" i] {
  border-top-style: none; border-right-style: none;
  border-bottom-style: none; border-left-style: none;
  padding-top: 0; padding-right: 0; padding-bottom: 0; padding-left: 0;
  background-color: transparent;
}

/* §15.3.9. "ThreeDFace" is a system colour this engine has no palette for, so
   the grey every desktop resolves it to is written out. */
fieldset {
  margin-left: 2px; margin-right: 2px;
  border-top: 2px groove #c0c0c0; border-right: 2px groove #c0c0c0;
  border-bottom: 2px groove #c0c0c0; border-left: 2px groove #c0c0c0;
  padding-top: 0.35em; padding-right: 0.75em;
  padding-bottom: 0.625em; padding-left: 0.75em;
}
legend { padding-left: 2px; padding-right: 2px }

/* <form> deliberately has no margin. HTML's rendering section gives it
   "margin-block-end: 1em" in *quirks mode* only, and this engine has one
   document mode; taking the quirk would indent every standards-mode document
   by an em nothing asked for. */
`
