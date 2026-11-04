package filestore

import (
	"bytes"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// x/text implements Russian letter/diacritic weights but not CLDR script
// reordering. PostgreSQL's ru-u-ks-level2 puts Cyrillic before Latin and Greek.
// Move the whole Cyrillic primary-weight interval, at every position in the
// name, preserving punctuation, numbers, expansions and secondary weights.
// Key encoding belongs to the pinned x/text version; cross-backend integration
// tests exercise the resulting ordering against the actual PostgreSQL ICU.
var russianWeights = func() (out struct{ latin, first, last int }) {
	c := collate.New(language.Russian, collate.IgnoreCase)
	var buf collate.Buffer
	k := c.KeyFromString(&buf, "a")
	out.latin, out.first = int(k[0])<<8|int(k[1]), 0x7fff
	for _, r := range unicode.Cyrillic.R16 {
		for ch := int(r.Lo); ch <= int(r.Hi); ch += int(r.Stride) {
			if !unicode.IsLetter(rune(ch)) {
				continue
			}
			buf.Reset()
			k = c.KeyFromString(&buf, string(rune(ch)))
			// Three-byte weights are implicit weights for characters newer
			// than x/text's collation table, outside its script intervals.
			if len(k) < 2 || k[0] >= 0x80 || k[0] == 0 && k[1] == 0 {
				continue
			}
			p := int(k[0])<<8 | int(k[1])
			out.first, out.last = min(out.first, p), max(out.last, p)
		}
	}
	return
}()

type nameOrder struct {
	col  *collate.Collator
	keys map[string][]byte
}

func newNameOrder() *nameOrder {
	return &nameOrder{collate.New(language.Russian, collate.IgnoreCase), map[string][]byte{}}
}

func (o *nameOrder) key(s string) []byte {
	if key, ok := o.keys[s]; ok {
		return key
	}
	var buf collate.Buffer
	key := bytes.Clone(o.col.KeyFromString(&buf, s))
	for i := 0; i+1 < len(key); {
		if key[i] == 0 && key[i+1] == 0 {
			break // secondary and subsequent levels stay unchanged
		}
		if key[i] >= 0x80 {
			i += 3
			continue
		}
		p := int(key[i])<<8 | int(key[i+1])
		switch {
		case p >= russianWeights.first && p <= russianWeights.last:
			p += russianWeights.latin - russianWeights.first
		case p >= russianWeights.latin && p < russianWeights.first:
			p += russianWeights.last - russianWeights.first + 1
		}
		key[i], key[i+1] = byte(p>>8), byte(p)
		i += 2
	}
	o.keys[s] = key
	return key
}

func (o *nameOrder) compare(a, b string) int { return bytes.Compare(o.key(a), o.key(b)) }
