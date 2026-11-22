// Package explain menjalankan EXPLAIN / EXPLAIN ANALYZE dari editor migration (bonus).
package explain

import (
	"context"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"errors"
	"fmt"
	"strings"
	"time"
)

// batas ukuran SQL dan waktu tunggu satu EXPLAIN
const (
	maxSQLBytes    = 256 << 10
	explainTimeout = 15 * time.Second
)

// hanya statement DML yang boleh di-EXPLAIN, DDL ditolak
var allowedKeywords = map[string]bool{
	"select": true, "insert": true, "update": true, "delete": true, "with": true, "values": true,
}

// InputError adalah kesalahan dari input user yang aman ditampilkan apa adanya, Kind menentukan jenisnya
type InputError struct {
	Kind    error
	Message string
}

func (e *InputError) Error() string { return e.Message }
func (e *InputError) Unwrap() error { return e.Kind }

// hasil satu EXPLAIN
type Result struct {
	SQL     string
	Analyze bool
	Dialect domain.DBMS
	Plan    *Plan
	RawText string
}

// Service menjalankan EXPLAIN setelah memeriksa akses dan SQL-nya
type Service struct {
	authz   contract.Authorizer
	drivers contract.DriverProvider
}

// NewService dibuat dengan authorizer dan penyedia driver database target
func NewService(authz contract.Authorizer, drivers contract.DriverProvider) *Service {
	return &Service{authz: authz, drivers: drivers}
}

// memeriksa akses View + Edit, validasi SQL,  menjalankan EXPLAIN di transaction, ANALYZE eksekusi statement sblm dirollback
func (s *Service) Run(ctx context.Context, p domain.Principal, dbID int64, sql string, analyze bool) (*Result, error) {
	ok, err := s.authz.Can(ctx, p.UserID, dbID, domain.AccessViewEdit)
	if err != nil {
		return nil, fmt.Errorf("check access: %w", err)
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
		return nil, fmt.Errorf("get driver: %w", err)
	}

	rs, err := runExplain(ctx, drv, explainStatement(drv.Dialect(), body, analyze), analyze)
	if err != nil {
		return nil, err
	}
	res := &Result{SQL: body, Analyze: analyze, Dialect: drv.Dialect()}
	res.fill(drv.Dialect(), resultText(rs))
	return res, nil
}

// runExplain menjalankan perintah EXPLAIN di transaction yang selalu di-rollback, error dari statement user dijadikan InputError
func runExplain(ctx context.Context, drv dbdriver.Driver, stmt string, analyze bool) (*domain.ResultSet, error) {
	var rs *domain.ResultSet
	// tnpa ANALYZE statement tidak dieksekusi, jadi transaction READ ONLY aman
	opts := dbdriver.TxOptions{ReadOnly: !analyze, Timeout: explainTimeout}
	err := drv.WithTx(ctx, opts, func(tx dbdriver.Tx) error {
		var qerr error
		rs, qerr = tx.Query(ctx, stmt)
		if qerr != nil {
			return qerr
		}
		// selalu di-rollback supaya ANALYZE tidak meninggalkan perubahan data
		return dbdriver.ErrForceRollback
	})
	if err != nil {
		if errors.Is(err, domain.ErrConnectionFailed) || ctx.Err() != nil {
			return nil, fmt.Errorf("explain: %w", err)
		}
		// kesalahan dari statement user ditampilkan, pesan dri DBMS tidak memuat kredensial koneksi
		return nil, &InputError{Kind: domain.ErrValidation, Message: "Database menolak statement: " + truncate(err.Error(), maxErrorChars)}
	}
	return rs, nil
}

// fill mengisi hasil: Postgres menghasilkan JSON yang diparse jadi tabel plan, dialek lain atau JSON yang gagal diparse ditampilkan sebagai teks mentah
func (r *Result) fill(dialect domain.DBMS, text string) {
	if dialect == domain.DBMSPostgres {
		if plan, err := ParsePostgresPlan(text); err == nil {
			r.Plan = plan
			return
		}
	}
	r.RawText = text
}

// validateSQL memastikan SQL tidak kosong, tidak terlalu panjang, hanya satu statement, dan hanya DML
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

// explainStatement menyusun perintah EXPLAIN sesuai dialek karena sintaks tiap DBMS berbeda
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

// resultText menggabungkan semua sel jd teks
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

// batas panjang pesan error dari database yang ditampilkan ke user
const maxErrorChars = 1000

// truncate memotong berdasarkan jumlah karakter, bukan byte, supaya tidak memutus karakter di tengah
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// userMessage mengembalikan pesan utk InputError, atau "" untuk error lain
func userMessage(err error) string {
	var ie *InputError
	if errors.As(err, &ie) {
		return ie.Message
	}
	return ""
}
