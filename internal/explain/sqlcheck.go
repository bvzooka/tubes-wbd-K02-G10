package explain

import (
	"strings"
	"unicode"
)

// sqlShape adalah hasil pemindaian ringan sebuah SQL.
type sqlShape struct {
	// FirstKeyword adalah kata pertama di luar komentar, huruf kecil.
	FirstKeyword string
	// Statements adalah jumlah statement tidak kosong (dipisah ";" di luar string/komentar).
	Statements int
	// Body adalah SQL tanpa ";" penutup, komentar penutup, dan whitespace di ujung.
	Body string
	// InnerSemicolon true jika ada ";" sebelum token terakhir (termasuk ";" kosong di awal).
	InnerSemicolon bool
}

// scanSQL memindai SQL dengan memperhatikan komentar (-- dan /* */ bersarang), string literal
// ('...', E'...'), identifier ber-quote ("...") dan dollar-quoted string ($tag$...$tag$).
func scanSQL(sql string) sqlShape {
	var (
		shape       sqlShape
		inStatement bool // statement saat ini sudah punya token non-komentar
		lastEnd     = 0  // posisi setelah token non-komentar terakhir
		lastSemi    = -1
		word        strings.Builder
	)
	flushWord := func() {
		if shape.FirstKeyword == "" && word.Len() > 0 {
			shape.FirstKeyword = strings.ToLower(word.String())
		}
		word.Reset()
	}
	startToken := func() {
		if !inStatement {
			inStatement = true
			shape.Statements++
		}
	}

	rs := []rune(sql)
	n := len(rs)
	for i := 0; i < n; i++ {
		c := rs[i]
		switch {
		case c == '-' && i+1 < n && rs[i+1] == '-':
			flushWord()
			for i < n && rs[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && rs[i+1] == '*':
			flushWord()
			depth := 1
			i += 2
			for i < n && depth > 0 {
				if rs[i] == '/' && i+1 < n && rs[i+1] == '*' {
					depth++
					i += 2
				} else if rs[i] == '*' && i+1 < n && rs[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			i--
		case c == '\'':
			escapes := word.Len() == 1 && (word.String() == "E" || word.String() == "e")
			flushWord()
			startToken()
			i++
			for i < n {
				if escapes && rs[i] == '\\' {
					i += 2
					continue
				}
				if rs[i] == '\'' {
					if i+1 < n && rs[i+1] == '\'' {
						i += 2
						continue
					}
					break
				}
				i++
			}
			lastEnd = i + 1
		case c == '"':
			flushWord()
			startToken()
			i++
			for i < n {
				if rs[i] == '"' {
					if i+1 < n && rs[i+1] == '"' {
						i += 2
						continue
					}
					break
				}
				i++
			}
			lastEnd = i + 1
		case c == '$' && word.Len() == 0:
			if tag, ok := dollarTag(rs, i); ok {
				startToken()
				end := indexRunes(rs, i+len(tag), tag)
				if end < 0 {
					i = n
				} else {
					i = end + len(tag) - 1
				}
				lastEnd = i + 1
				continue
			}
			startToken()
			lastEnd = i + 1
		case c == ';':
			flushWord()
			inStatement = false
			lastSemi = i
		case unicode.IsSpace(c):
			flushWord()
		default:
			startToken()
			if unicode.IsLetter(c) || c == '_' || (word.Len() > 0 && unicode.IsDigit(c)) {
				word.WriteRune(c)
			} else {
				flushWord()
			}
			lastEnd = i + 1
		}
	}
	flushWord()
	if lastEnd > n {
		lastEnd = n
	}
	shape.Body = strings.TrimSpace(string(rs[:lastEnd]))
	shape.InnerSemicolon = lastSemi >= 0 && lastSemi < lastEnd
	return shape
}

// dollarTag mengenali pembuka $tag$ atau $$ pada posisi i.
func dollarTag(rs []rune, i int) ([]rune, bool) {
	j := i + 1
	for j < len(rs) && (unicode.IsLetter(rs[j]) || rs[j] == '_' || (j > i+1 && unicode.IsDigit(rs[j]))) {
		j++
	}
	if j < len(rs) && rs[j] == '$' {
		return rs[i : j+1], true
	}
	return nil, false
}

func indexRunes(rs []rune, from int, sub []rune) int {
	for i := from; i+len(sub) <= len(rs); i++ {
		match := true
		for k := range sub {
			if rs[i+k] != sub[k] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
