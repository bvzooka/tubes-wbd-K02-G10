// Package explain menjalankan EXPLAIN / EXPLAIN ANALYZE dari editor migration (bonus).
package explain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

const (
	maxSQLBytes    = 256 << 10
	explainTimeout = 15 * time.Second
)

var allowedKeywords = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true, "with": true, "values": true,
}

// InputError adalah kesalahan input yang aman ditampilkan ke user.
// errors.Is(err, domain.ErrUnsupported) atau domain.ErrValidation bernilai true sesuai Kind.
type InputError struct {
	Kind    error
	Message string // Bahasa Indonesia
}

func (e *InputError) Error() string { return e.Message }
func (e *InputError) Unwrap() error { return e.Kind }

// Result adalah hasil satu EXPLAIN.
type Result struct {
	SQL     string
	Analyze bool
	Dialect domain.DBMS
	// Plan terisi untuk PostgreSQL. RawText terisi untuk dialek lain atau jika JSON gagal di-parse.
	Plan    *Plan
	RawText string
}

type Service struct {
	authz   contract.Authorizer
	drivers contract.DriverProvider
}

func NewService(authz contract.Authorizer, drivers contract.DriverProvider) *Service {
	return &Service{authz: authz, drivers: drivers}
}

// Run memeriksa akses View + Edit, memvalidasi SQL, lalu menjalankan EXPLAIN di transaction
// yang selalu di-rollback. ANALYZE benar-benar mengeksekusi statement sebelum di-rollback.
func (s *Service) Run(ctx context.Context, p domain.Principal, dbID int64, sql string, analyze bool) (*Result, error) {
	ok, err := s.authz.Can(ctx, p.UserID, dbID, domain.AccessViewEdit)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrForbidden
	}

	body, err := validateSQL(sql)
	if err != nil {
		return nil, err
	}

	drv, err := s.drivers.Get(ctx, dbID)
	if err != nil {
		return nil, err
	}

	res := &Result{SQL: body, Analyze: analyze, Dialect: drv.Dialect()}
	stmt := explainStatement(drv.Dialect(), body, analyze)
	var rs *domain.ResultSet
	// Tanpa ANALYZE statement tidak dieksekusi, jadi transaction READ ONLY aman sebagai lapisan tambahan.
	opts := dbdriver.TxOptions{ReadOnly: !analyze, Timeout: explainTimeout}
	err = drv.WithTx(ctx, opts, func(tx dbdriver.Tx) error {
		var qerr error
		rs, qerr = tx.Query(ctx, stmt)
		if qerr != nil {
			return qerr
		}
		return dbdriver.ErrForceRollback
	})
	if err != nil {
		if errors.Is(err, domain.ErrConnectionFailed) || ctx.Err() != nil {
			return nil, fmt.Errorf("explain: %w", err)
		}
		// Kesalahan dari statement user (sintaks, tabel tidak ada, timeout) ditampilkan apa adanya;
		// pesan dari DBMS tidak memuat kredensial koneksi.
		return nil, &InputError{Kind: domain.ErrValidation, Message: "Database menolak statement: " + truncate(err.Error(), maxErrorChars)}
	}

	text := resultText(rs)
	if drv.Dialect() == domain.DBMSPostgres {
		if plan, perr := ParsePostgresPlan(text); perr == nil {
			res.Plan = plan
			return res, nil
		}
	}
	res.RawText = text
	return res, nil
}

func validateSQL(sql string) (string, error) {
	if strings.TrimSpace(sql) == "" {
		return "", &InputError{Kind: domain.ErrValidation, Message: "SQL tidak boleh kosong."}
	}
	if len(sql) > maxSQLBytes {
		return "", &InputError{Kind: domain.ErrValidation, Message: "SQL terlalu panjang (maksimum 256 KB)."}
	}
	shape := scanSQL(sql)
	if shape.Statements == 0 {
		return "", &InputError{Kind: domain.ErrValidation, Message: "SQL tidak boleh kosong."}
	}
	if shape.Statements > 1 || shape.InnerSemicolon {
		return "", &InputError{Kind: domain.ErrValidation, Message: "EXPLAIN hanya dapat menganalisis satu statement."}
	}
	if !allowedKeywords[shape.FirstKeyword] {
		return "", &InputError{Kind: domain.ErrUnsupported, Message: "EXPLAIN hanya mendukung statement DML (SELECT, INSERT, UPDATE, DELETE, WITH, VALUES)."}
	}
	return shape.Body, nil
}

func explainStatement(dialect domain.DBMS, body string, analyze bool) string {
	switch dialect {
	case domain.DBMSMySQL:
		if analyze {
			return "EXPLAIN ANALYZE " + body
		}
		return "EXPLAIN FORMAT=TREE " + body
	case domain.DBMSMariaDB:
		if analyze {
			return "ANALYZE FORMAT=JSON " + body
		}
		return "EXPLAIN FORMAT=JSON " + body
	default:
		if analyze {
			return "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + body
		}
		return "EXPLAIN (FORMAT JSON) " + body
	}
}

// resultText menggabungkan semua sel menjadi teks (output EXPLAIN bisa satu sel JSON atau banyak baris teks).
func resultText(rs *domain.ResultSet) string {
	if rs == nil {
		return ""
	}
	var lines []string
	for _, row := range rs.Rows {
		var cells []string
		for _, c := range row {
			if !c.Null {
				cells = append(cells, c.Value)
			}
		}
		lines = append(lines, strings.Join(cells, "\t"))
	}
	return strings.Join(lines, "\n")
}

const maxErrorChars = 1000

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// userMessage mengembalikan pesan aman untuk InputError, atau "" untuk error lain.
func userMessage(err error) string {
	var ie *InputError
	if errors.As(err, &ie) {
		return ie.Message
	}
	return ""
}
