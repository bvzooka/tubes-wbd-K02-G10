package connmgr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/domain"
)

// sqlstateUniqueViolation adalah kode error PostgreSQL saat nilai unik (di sini nama koneksi) sudah dipakai
const sqlstateUniqueViolation = "23505"

// connColumns adalah kolom yang dibaca untuk Connection, sengaja tanpa db_password_enc
const connColumns = `id, name, dbms, host, port, database_name, db_username, ssl_mode, is_active, created_at, updated_at`

// Repo menyimpan koneksi di tabel connections, password dienkripsi dengan key sebelum disimpan
type Repo struct {
	pool *pgxpool.Pool
	key  []byte
}

var _ Store = (*Repo)(nil)

// NewRepo dibuat dengan pool database platform dan kunci enkripsi 32 byte
func NewRepo(pool *pgxpool.Pool, key []byte) *Repo { return &Repo{pool: pool, key: key} }

// Insert menyimpan koneksi baru, createdBy 0 berarti pembuatnya tidak dicatat
func (r *Repo) Insert(ctx context.Context, in domain.ConnectionInput, createdBy int64) (domain.Connection, error) {
	enc, err := Encrypt(r.key, []byte(in.Password))
	if err != nil {
		return domain.Connection{}, fmt.Errorf("encrypt password: %w", err)
	}
	// created_by bisa NULL, jadi dikirim sebagai pointer: nil menjadi NULL di database
	var by *int64
	if createdBy > 0 {
		by = &createdBy
	}
	c, err := scanConn(r.pool.QueryRow(ctx, `
		INSERT INTO connections (name, dbms, host, port, database_name, db_username, db_password_enc, ssl_mode, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+connColumns,
		in.Name, string(in.DBMS), in.Host, in.Port, in.DatabaseName, in.Username, enc, in.SSLMode, by))
	if err != nil {
		return domain.Connection{}, mapErr("insert connection", err)
	}
	return c, nil
}

// Update mengubah koneksi, password kosong berarti password lama dipertahankan
func (r *Repo) Update(ctx context.Context, id int64, in domain.ConnectionInput) (domain.Connection, error) {
	// enc nil dikirim sebagai NULL, lalu COALESCE di SQL memakai password lama
	var enc []byte
	if in.Password != "" {
		var err error
		if enc, err = Encrypt(r.key, []byte(in.Password)); err != nil {
			return domain.Connection{}, fmt.Errorf("encrypt password: %w", err)
		}
	}
	c, err := scanConn(r.pool.QueryRow(ctx, `
		UPDATE connections
		SET name = $2, dbms = $3, host = $4, port = $5, database_name = $6, db_username = $7,
		    db_password_enc = COALESCE($8, db_password_enc), ssl_mode = $9, updated_at = now()
		WHERE id = $1
		RETURNING `+connColumns,
		id, in.Name, string(in.DBMS), in.Host, in.Port, in.DatabaseName, in.Username, enc, in.SSLMode))
	if err != nil {
		return domain.Connection{}, mapErr(fmt.Sprintf("update connection %d", id), err)
	}
	return c, nil
}

// SetActive mengaktifkan atau menonaktifkan koneksi. updated_at ikut berubah supaya cache Provider membuang driver lamanya
func (r *Repo) SetActive(ctx context.Context, id int64, active bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE connections SET is_active = $2, updated_at = now() WHERE id = $1`, id, active)
	if err != nil {
		return fmt.Errorf("set active of connection %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Get mengembalikan satu koneksi tanpa password
func (r *Repo) Get(ctx context.Context, id int64) (domain.Connection, error) {
	c, err := scanConn(r.pool.QueryRow(ctx, `SELECT `+connColumns+` FROM connections WHERE id = $1`, id))
	if err != nil {
		return domain.Connection{}, mapErr(fmt.Sprintf("get connection %d", id), err)
	}
	return c, nil
}

// List mengembalikan semua koneksi, urut menurut nama tanpa membedakan huruf besar-kecil
func (r *Repo) List(ctx context.Context) ([]domain.Connection, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+connColumns+` FROM connections ORDER BY lower(name), id`)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()
	var out []domain.Connection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, fmt.Errorf("scan connection: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	return out, nil
}

// Stamp memenuhi Store: query ringan untuk mengecek apakah driver di cache Provider masih berlaku
func (r *Repo) Stamp(ctx context.Context, id int64) (time.Time, bool, error) {
	var updatedAt time.Time
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT updated_at, is_active FROM connections WHERE id = $1`, id).
		Scan(&updatedAt, &active)
	if err != nil {
		return time.Time{}, false, mapErr(fmt.Sprintf("stamp connection %d", id), err)
	}
	return updatedAt, active, nil
}

// Load memenuhi Store: baris penuh beserta password yang sudah didekripsi
func (r *Repo) Load(ctx context.Context, id int64) (domain.Connection, string, error) {
	var enc []byte
	var c domain.Connection
	var dbms string
	err := r.pool.QueryRow(ctx, `SELECT `+connColumns+`, db_password_enc FROM connections WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &dbms, &c.Host, &c.Port, &c.DatabaseName, &c.Username,
			&c.SSLMode, &c.IsActive, &c.CreatedAt, &c.UpdatedAt, &enc)
	if err != nil {
		return domain.Connection{}, "", mapErr(fmt.Sprintf("load connection %d", id), err)
	}
	c.DBMS = domain.DBMS(dbms)
	pw, err := Decrypt(r.key, enc)
	if err != nil {
		return domain.Connection{}, "", fmt.Errorf("decrypt password of connection %d: %w", id, err)
	}
	return c, string(pw), nil
}

// scanConn mengisi Connection dari satu baris hasil query
func scanConn(row pgx.Row) (domain.Connection, error) {
	var c domain.Connection
	var dbms string
	err := row.Scan(&c.ID, &c.Name, &dbms, &c.Host, &c.Port, &c.DatabaseName, &c.Username,
		&c.SSLMode, &c.IsActive, &c.CreatedAt, &c.UpdatedAt)
	c.DBMS = domain.DBMS(dbms)
	return c, err
}

// mapErr menerjemahkan error database platform ke error domain. Error lain dibungkus dengan nama operasinya
// supaya log menunjukkan langkah mana yang gagal.
func mapErr(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == sqlstateUniqueViolation {
		return &domain.ValidationError{Fields: map[string]string{"name": "Nama koneksi sudah dipakai."}}
	}
	return fmt.Errorf("%s: %w", op, err)
}
