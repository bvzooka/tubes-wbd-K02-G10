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

// shapeScanner menyimpan keadaan saat memindai SQL: hasil sementara, kata yang sedang dibaca, dan posisi penting
type shapeScanner struct {
	rs          []rune
	shape       sqlShape
	word        strings.Builder
	inStatement bool // statement saat ini sudah punya token non-komentar
	lastEnd     int  // posisi setelah token non-komentar terakhir
	lastSemi    int  // posisi titik koma terakhir, -1 kalau belum ada
}

// scanSQL memindai SQL dengan memperhatikan komentar (-- dan /* */ bersarang), string literal ('...', E'...'), identifier ber-quote ("...") dan dollar-quoted string ($tag$...$tag$)
func scanSQL(sql string) sqlShape {
	sc := &shapeScanner{rs: []rune(sql), lastSemi: -1}
	n := len(sc.rs)
	for i := 0; i < n; i++ {
		i = sc.step(i)
	}
	sc.flushWord()
	if sc.lastEnd > n {
		sc.lastEnd = n
	}
	sc.shape.Body = strings.TrimSpace(string(sc.rs[:sc.lastEnd]))
	sc.shape.InnerSemicolon = sc.lastSemi >= 0 && sc.lastSemi < sc.lastEnd
	return sc.shape
}

// flushWord menutup kata yang sedang dibaca, kata pertama di SQL disimpan sebagai keyword
func (sc *shapeScanner) flushWord() {
	if sc.shape.FirstKeyword == "" && sc.word.Len() > 0 {
		sc.shape.FirstKeyword = strings.ToLower(sc.word.String())
	}
	sc.word.Reset()
}

// startToken menandai statement saat ini sudah berisi token, statement baru dihitung sekali saja
func (sc *shapeScanner) startToken() {
	if !sc.inStatement {
		sc.inStatement = true
		sc.shape.Statements++
	}
}

// step membaca satu token mulai dari posisi i dan mengembalikan posisi karakter terakhir yang sudah terbaca
func (sc *shapeScanner) step(i int) int {
	rs := sc.rs
	n := len(rs)
	c := rs[i]
	switch {
	case c == '-' && i+1 < n && rs[i+1] == '-':
		sc.flushWord()
		return skipLine(rs, i)
	case c == '/' && i+1 < n && rs[i+1] == '*': // komentar blok boleh bersarang
		sc.flushWord()
		return skipBlock(rs, i)
	case c == '\'':
		// E'..' mengenal backslash, string biasa tidak
		escapes := sc.word.Len() == 1 && (sc.word.String() == "E" || sc.word.String() == "e")
		return sc.quoted(i, '\'', escapes)
	case c == '"':
		return sc.quoted(i, '"', false)
	case c == '$' && sc.word.Len() == 0:
		return sc.dollar(i)
	case c == ';': // titik koma di luar string dan komentar memisahkan statement
		sc.flushWord()
		sc.inStatement = false
		sc.lastSemi = i
	case unicode.IsSpace(c): // spasi mengakhiri kata yang sedang dibaca
		sc.flushWord()
	default:
		sc.plain(c, i)
	}
	return i
}

// quoted membaca string atau identifier ber-quote sampai kutip penutupnya
func (sc *shapeScanner) quoted(i int, quote rune, escapes bool) int {
	sc.flushWord()
	sc.startToken()
	end := skipQuoted(sc.rs, i+1, quote, escapes)
	sc.lastEnd = end + 1
	return end
}

// dollar menangani $: pembuka dollar-quote ($$ atau $tag$) dilewati sampai penutup yang sama, selain itu simbol biasa
func (sc *shapeScanner) dollar(i int) int {
	sc.startToken()
	if tag, ok := dollarTag(sc.rs, i); ok {
		if end := indexRunes(sc.rs, i+len(tag), tag); end < 0 {
			i = len(sc.rs)
		} else {
			i = end + len(tag) - 1
		}
	}
	sc.lastEnd = i + 1
	return i
}

// plain mencatat token biasa, huruf dikumpulkan jadi kata untuk mencari keyword pertama
func (sc *shapeScanner) plain(c rune, i int) {
	sc.startToken()
	if unicode.IsLetter(c) || c == '_' || (sc.word.Len() > 0 && unicode.IsDigit(c)) {
		sc.word.WriteRune(c)
	} else {
		sc.flushWord()
	}
	sc.lastEnd = i + 1
}

// skipLine melewati komentar baris sampai akhir baris, mengembalikan posisi akhir baris itu
func skipLine(rs []rune, i int) int {
	for i < len(rs) && rs[i] != '\n' {
		i++
	}
	return i
}

// skipBlock melewati komentar blok yang bisa bersarang dengan menghitung kedalamannya, mengembalikan posisi penutup terluar
func skipBlock(rs []rune, i int) int {
	n := len(rs)
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
	return i - 1
}

// skipQuoted mencari kutip penutup mulai dari i, dua kutip berurutan berarti satu kutip di dalam isi, dan backslash melewati karakter berikutnya kalau escapes true
func skipQuoted(rs []rune, i int, quote rune, escapes bool) int {
	n := len(rs)
	for i < n {
		if escapes && rs[i] == '\\' {
			i += 2
			continue
		}
		if rs[i] == quote {
			if i+1 < n && rs[i+1] == quote {
				i += 2
				continue
			}
			break
		}
		i++
	}
	return i
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

// indexRunes mencari sub mulai dari posisi from, -1 kalau tidak ada
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
