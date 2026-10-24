package connmgr

import (
	"context"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeDriver struct {
	dbdriver.Driver
	password string
	closed   bool
}

func (d *fakeDriver) Close() { d.closed = true }

type fakeStore struct {
	mu sync.Mutex
	conns map[int64]*domain.Connection
	passwords map[int64]string
	loads int
}

func newFakeStore() *fakeStore {
	return &fakeStore{conns: map[int64]*domain.Connection{}, passwords: map[int64]string{}}
}

func (s *fakeStore) put(id int64, active bool, updated time.Time, pw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[id] = &domain.Connection{ID: id, IsActive: active, UpdatedAt: updated}
	s.passwords[id] = pw
}

func (s *fakeStore) Stamp(_ context.Context, id int64) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return time.Time{}, false, domain.ErrNotFound
	}
	return c.UpdatedAt, c.IsActive, nil
}

func (s *fakeStore) Load(_ context.Context, id int64) (domain.Connection, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	c, ok := s.conns[id]
	if !ok {
		return domain.Connection{}, "", domain.ErrNotFound
	}
	return *c, s.passwords[id], nil
}

type factoryRec struct {
	mu      sync.Mutex
	built   []*fakeDriver
	failing error
}

func (f *factoryRec) build(_ context.Context, _ domain.Connection, pw string) (dbdriver.Driver, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing != nil {
		return nil, f.failing
	}
	d := &fakeDriver{password: pw}
	f.built = append(f.built, d)
	return d, nil
}

var t0 = time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

func TestProvider_CachesDriver(t *testing.T) {
	st, f := newFakeStore(), &factoryRec{}
	st.put(1, true, t0, "pw")
	p := NewProvider(st, f.build)

	d1, err := p.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := p.Get(context.Background(), 1)
	if d1 != d2 || len(f.built) != 1 {
		t.Fatalf("driver harus di-cache: built=%d", len(f.built))
	}
	if st.loads != 1 {
		t.Errorf("Load dipanggil %d kali, mau 1", st.loads)
	}
	if f.built[0].password != "pw" {
		t.Errorf("factory harus menerima password terdekripsi")
	}
}

func TestProvider_RebuildsWhenUpdatedAtChanges(t *testing.T) {
	st, f := newFakeStore(), &factoryRec{}
	st.put(1, true, t0, "old")
	p := NewProvider(st, f.build)

	d1, _ := p.Get(context.Background(), 1)
	st.put(1, true, t0.Add(time.Minute), "new")
	d2, err := p.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 || len(f.built) != 2 {
		t.Fatalf("driver harus dibangun ulang: built=%d", len(f.built))
	}
	if !f.built[0].closed {
		t.Error("driver lama harus ditutup")
	}
	if f.built[1].closed || f.built[1].password != "new" {
		t.Error("driver baru harus aktif dengan password baru")
	}
}

func TestProvider_InactiveRejectedAndEvicted(t *testing.T) {
	st, f := newFakeStore(), &factoryRec{}
	st.put(1, true, t0, "pw")
	p := NewProvider(st, f.build)
	_, _ = p.Get(context.Background(), 1)

	st.put(1, false, t0, "pw")
	if _, err := p.Get(context.Background(), 1); !errors.Is(err, domain.ErrConnectionInactive) {
		t.Fatalf("mau ErrConnectionInactive, dapat %v", err)
	}
	if !f.built[0].closed {
		t.Error("driver koneksi nonaktif harus ditutup")
	}
	st.put(1, true, t0, "pw")
	if _, err := p.Get(context.Background(), 1); err != nil || len(f.built) != 2 {
		t.Fatalf("harus bangun ulang setelah aktif lagi: err=%v built=%d", err, len(f.built))
	}
}

func TestProvider_NotFound(t *testing.T) {
	st, f := newFakeStore(), &factoryRec{}
	p := NewProvider(st, f.build)
	if _, err := p.Get(context.Background(), 99); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("mau ErrNotFound, dapat %v", err)
	}
	if len(f.built) != 0 {
		t.Error("tidak boleh membangun driver")
	}
}

func TestProvider_FactoryError(t *testing.T) {
	st := newFakeStore()
	st.put(1, true, t0, "pw")
	f := &factoryRec{failing: domain.ErrConnectionFailed}
	p := NewProvider(st, f.build)
	if _, err := p.Get(context.Background(), 1); !errors.Is(err, domain.ErrConnectionFailed) {
		t.Fatalf("error factory harus diteruskan: %v", err)
	}
	f.failing = nil
	if _, err := p.Get(context.Background(), 1); err != nil {
		t.Fatalf("harus pulih: %v", err)
	}
}

func TestProvider_Close(t *testing.T) {
	st, f := newFakeStore(), &factoryRec{}
	st.put(1, true, t0, "a")
	st.put(2, true, t0, "b")
	p := NewProvider(st, f.build)
	_, _ = p.Get(context.Background(), 1)
	_, _ = p.Get(context.Background(), 2)
	p.Close()
	for i, d := range f.built {
		if !d.closed {
			t.Errorf("driver %d belum ditutup", i)
		}
	}
}

func TestProvider_ConcurrentGet(t *testing.T) {
	st, f := newFakeStore(), &factoryRec{}
	st.put(1, true, t0, "pw")
	p := NewProvider(st, f.build)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Get(context.Background(), 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(f.built) != 1 {
		t.Fatalf("driver harus dibangun sekali, dapat %d", len(f.built))
	}
}
