package migration

import (
	"errors"
	"strings"
)

var ErrUnterminated = errors.New("unterminated quote, comment, or dollar-quote")

type Statement struct {
	Text    string
	Keyword string
	Second  string
}

type tokenTracker struct {
	count  int
	first  string
	second string
}

func (t *tokenTracker) note(word string) {
	t.count++
	switch {
	case t.count == 1:
		t.first = word
	case t.count == 2 && t.first != "":
		t.second = word
	}
}

func Split(sql string) ([]Statement, error) {
	var stmts []Statement
	var tokens tokenTracker
	start, i, n := 0, 0, len(sql)

	flush := func(end int) {
		if tokens.count > 0 {
			stmts = append(stmts, Statement{
				Text:    strings.TrimSpace(sql[start:end]),
				Keyword: tokens.first,
				Second:  tokens.second,
			})
		}
		tokens = tokenTracker{}
	}

	for i < n {
		c := sql[i]
		switch {
		case c == ';': // Titik koma di luar string dan komentar = akhir perintah
			flush(i)
			i++
			start = i
		case c == '-' && i+1 < n && sql[i+1] == '-':
			i = skipLineComment(sql, i)
		case c == '/' && i+1 < n && sql[i+1] == '*': // Komentar block boleh nested
			end, ok := skipBlockComment(sql, i)
			if !ok {
				return nil, ErrUnterminated
			}
			i = end
		case c == '\'': // Treat backslash secara khusus
			backslash := i > 0 && (sql[i-1] == 'e' || sql[i-1] == 'E') && (i < 2 || !isIdentChar(sql[i-2]))
			end, ok := skipQuoted(sql, i, '\'', backslash)
			if !ok {
				return nil, ErrUnterminated
			}
			tokens.note("")
			i = end
		case c == '"':
			end, ok := skipQuoted(sql, i, '"', false)
			if !ok {
				return nil, ErrUnterminated
			}
			tokens.note("")
			i = end
		case c == '$': // Bisa ada titik koma di dollar-quote
			delim, ok := dollarDelimiter(sql, i)
			if !ok {
				tokens.note("")
				i++
				continue
			}
			end, ok := skipDollarQuoted(sql, i, delim)
			if !ok {
				return nil, ErrUnterminated
			}
			tokens.note("")
			i = end
		case isSpace(c):
			i++
		case isIdentChar(c):
			j := i
			for j < n && isIdentChar(sql[j]) {
				j++
			}
			// Token awal angka bukan kata kunci
			if c >= '0' && c <= '9' {
				tokens.note("")
			} else {
				tokens.note(strings.ToUpper(sql[i:j]))
			}
			i = j
		default:
			tokens.note("")
			i++
		}
	}
	flush(n)
	return stmts, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isIdentChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c >= 0x80
}

func isTagChar(c byte) bool {
	return c != '$' && isIdentChar(c)
}

func skipLineComment(sql string, i int) int {
	j := strings.IndexByte(sql[i:], '\n')
	if j < 0 {
		return len(sql)
	}
	return i + j
}

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

func skipDollarQuoted(sql string, i int, delim string) (int, bool) {
	from := i + len(delim)
	j := strings.Index(sql[from:], delim)
	if j < 0 {
		return len(sql), false
	}
	return from + j + len(delim), true
}

func IsTransactionControl(s Statement) bool {
	switch s.Keyword {
	case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "END", "ABORT":
		return true
	case "START", "PREPARE":
		return s.Second == "TRANSACTION"
	}
	return false
}

func FirstTransactionControl(stmts []Statement) (Statement, bool) {
	for _, s := range stmts {
		if IsTransactionControl(s) {
			return s, true
		}
	}
	return Statement{}, false
}

func ContainsDDL(stmts []Statement) bool {
	for _, s := range stmts {
		switch s.Keyword {
		case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME":
			return true
		}
	}
	return false
}
