// Server-sourced text shown in the app: controls, bidi overrides and
// isolates, zero-width characters, and line separators become a visible
// escape (backslash + lowercase "u" + 4 hex digits), so one name cannot
// pose as another.
const RANGES: [number, number][] = [
  [0x0000, 0x001f], [0x007f, 0x009f], // C0/C1 controls
  [0x202a, 0x202e], // bidi overrides
  [0x2066, 0x2069], // bidi isolates
  [0x200b, 0x200f], [0x2060, 0x2064], [0xfeff, 0xfeff], // zero-width
  [0x2028, 0x2029], // line/paragraph separator
]
const HIDDEN = new RegExp('[' + RANGES.map(([a, b]) => String.fromCharCode(a) + '-' + String.fromCharCode(b)).join('') + ']', 'g')

export const displayText = (s: string): string =>
  s.replace(HIDDEN, (c) => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'))
