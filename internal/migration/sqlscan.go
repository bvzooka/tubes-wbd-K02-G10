package migration

import (
	"errors"
	"strings"
)

// ErrUnterminated dikembalikan kalau ada kutip, komentar, atau dollar-quote yang tidak ditutup
var ErrUnterminated = errors.New("unterminated quote, comment, or dollar-quote")

// Statement adalah satu perintah SQL beserta dua kata pertamanya, dipakai untuk mengenali jenis perintah
type Statement struct {
	Text    string
	Keyword string
	Second  string
}

// tokenTracker mencatat jumlah token dan dua kata pertama dari statement yang sedang dibaca
type tokenTracker struct {
	count  int
	first  string
	second string
}

// note dipanggil untuk tiap token, kata kosong berarti token itu bukan kata kunci (angka, string, simbol)
func (t *tokenTracker) note(word string) {
	t.count++
	switch {
	case t.count == 1:
		t.first = word
	case t.count == 2 && t.first != "":
		t.second = word
	}
}

// splitter menyimpan keadaan saat memindai SQL: hasil sementara, token statement yang sedang dibaca, dan awal statement itu
type splitter struct {
	sql    string
	stmts  []Statement
	tokens tokenTracker
	start  int
}

// Split memecah SQL jadi statement dengan membaca karakter satu per satu, supaya titik koma di dalam string, komentar, atau dollar-quote tidak ikut memecah
func Split(sql string) ([]Statement, error) {
	sp := &splitter{sql: sql}
	for i := 0; i < len(sql); {
		next, err := sp.step(i)
		if err != nil {
			return nil, err
		}
		i = next
	}
	sp.flush(len(sql))
	return sp.stmts, nil
}

// flush menutup statement yang sedang dibaca dan mulai pencatatan baru
func (sp *splitter) flush(end int) {
	if sp.tokens.count > 0 {
		sp.stmts = append(sp.stmts, Statement{
			Text:    strings.TrimSpace(sp.sql[sp.start:end]),
			Keyword: sp.tokens.first,
			Second:  sp.tokens.second,
		})
	}
	sp.tokens = tokenTracker{}
}

// step membaca satu token mulai dari posisi i dan mengembalikan posisi berikutnya
func (sp *splitter) step(i int) (int, error) {
	sql, n := sp.sql, len(sp.sql)
	c := sql[i]
	switch {
	case c == ';': // Titik koma di luar string dan komentar = akhir perintah
		sp.flush(i)
		sp.start = i + 1
		return i + 1, nil
	case c == '-' && i+1 < n && sql[i+1] == '-':
		return skipLineComment(sql, i), nil
	case c == '/' && i+1 < n && sql[i+1] == '*': // Komentar block boleh nested
		return closed(skipBlockComment(sql, i))
	case c == '\'': // Treat backslash secara khusus
		return sp.noted(skipQuoted(sql, i, '\'', isEscapeString(sql, i)))
	case c == '"':
		return sp.noted(skipQuoted(sql, i, '"', false))
	case c == '$': // Bisa ada titik koma di dollar-quote
		return sp.dollar(i)
	case isSpace(c):
		return i + 1, nil
	case isIdentChar(c):
		return sp.word(i), nil
	default:
		sp.tokens.note("")
		return i + 1, nil
	}
}

// closed mengubah hasil skip menjadi posisi berikutnya, atau ErrUnterminated kalau penutupnya tidak ketemu
func closed(end int, ok bool) (int, error) {
	if !ok {
		return 0, ErrUnterminated
	}
	return end, nil
}

// noted sama dengan closed, ditambah mencatat bahwa ada satu token (string atau identifier) di statement ini
func (sp *splitter) noted(end int, ok bool) (int, error) {
	end, err := closed(end, ok)
	if err != nil {
		return 0, err
	}
	sp.tokens.note("")
	return end, nil
}

// isEscapeString true untuk string gaya E'..' yang mengenal backslash
func isEscapeString(sql string, i int) bool {
	return i > 0 && (sql[i-1] == 'e' || sql[i-1] == 'E') && (i < 2 || !isIdentChar(sql[i-2]))
}

// dollar menangani $: bisa pembuka dollar-quote ($$ atau $tag$), bisa juga hanya simbol biasa
func (sp *splitter) dollar(i int) (int, error) {
	delim, ok := dollarDelimiter(sp.sql, i)
	if !ok {
		sp.tokens.note("")
		return i + 1, nil
	}
	return sp.noted(skipDollarQuoted(sp.sql, i, delim))
}

// word membaca satu kata atau angka dan mencatatnya, hanya kata yang bisa jadi kata kunci
func (sp *splitter) word(i int) int {
	j := i
	for j < len(sp.sql) && isIdentChar(sp.sql[j]) {
		j++
	}
	// Token awal angka bukan kata kunci
	if c := sp.sql[i]; c >= '0' && c <= '9' {
		sp.tokens.note("")
	} else {
		sp.tokens.note(strings.ToUpper(sp.sql[i:j]))
	}
	return j
}

// isSpace true untuk karakter spasi, tab, dan baris baru
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// isIdentChar true untuk karakter yang boleh ada di nama: huruf, angka, garis bawah, dolar, dan karakter non-ASCII
func isIdentChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c >= 0x80
}

// isTagChar sama dengan isIdentChar tapi tanpa dolar, karena dolar membatasi tag dollar-quote
func isTagChar(c byte) bool {
	return c != '$' && isIdentChar(c)
}

// skipLineComment mengembalikan posisi akhir baris, isi komentarnya dilewati
func skipLineComment(sql string, i int) int {
	j := strings.IndexByte(sql[i:], '\n')
	if j < 0 {
		return len(sql)
	}
	return i + j
}

// skipBlockComment melewati komentar blok yang bisa bersarang, false kalau tidak ditutup
func skipBlockComment(sql string, i int) (int, bool) {
	depth := 1
	j := i + 2
	for j < len(sql) {
		switch {
		case strings.HasPrefix(sql[j:], "/*"):
			depth++
			j += 2
		case strings.HasPrefix(sql[j:], "*/"):
			depth--
			j += 2
			if depth == 0 {
				return j, true
			}
		default:
			j++
		}
	}
	return len(sql), false
}

// skipQuoted melewati string atau identifier ber-quote, dua kutip berurutan berarti kutip itu sendiri
func skipQuoted(sql string, i int, quote byte, backslash bool) (int, bool) {
	j := i + 1
	for j < len(sql) {
		c := sql[j]
		switch {
		case backslash && c == '\\':
			j += 2
		case c == quote && j+1 < len(sql) && sql[j+1] == quote:
			j += 2
		case c == quote:
			return j + 1, true
		default:
			j++
		}
	}
	return len(sql), false
}

// Biar $ dilihat sebagai bagian dari parameter
func dollarDelimiter(sql string, i int) (string, bool) {
	if i > 0 && isIdentChar(sql[i-1]) {
		return "", false
	}
	j := i + 1
	if j < len(sql) && sql[j] == '$' {
		return "$$", true
	}
	if j >= len(sql) || (sql[j] >= '0' && sql[j] <= '9') || !isTagChar(sql[j]) {
		return "", false
	}
	for j < len(sql) && isTagChar(sql[j]) {
		j++
	}
	if j < len(sql) && sql[j] == '$' {
		return sql[i : j+1], true
	}
	return "", false
}

// skipDollarQuoted mencari penutup yang sama persis dengan pembukanya
func skipDollarQuoted(sql string, i int, delim string) (int, bool) {
	from := i + len(delim)
	j := strings.Index(sql[from:], delim)
	if j < 0 {
		return len(sql), false
	}
	return from + j + len(delim), true
}

// IsTransactionControl true untuk perintah pengatur transaction, dilarang karena semua migration sudah satu transaction
func IsTransactionControl(s Statement) bool {
	switch s.Keyword {
	case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "END", "ABORT":
		return true
	case "START", "PREPARE":
		return s.Second == "TRANSACTION"
	}
	return false
}

// FirstTransactionControl mengembalikan perintah transaction pertama kalau ada
func FirstTransactionControl(stmts []Statement) (Statement, bool) {
	for _, s := range stmts {
		if IsTransactionControl(s) {
			return s, true
		}
	}
	return Statement{}, false
}
