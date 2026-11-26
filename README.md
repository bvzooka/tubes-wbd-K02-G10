# Database Management Platform

Tugas Besar IF3110 Pengembangan Aplikasi Web, Milestone 1, kelompok K02-G10.

-Menjalankan

Yang perlu ada: Go 1.26 dan Docker.

```bash
cp .env.example .env
```

Isi dua nilai di `.env`:

- `APP_SECRET_KEY`: base64 dari tepat 32 byte acak untuk mengenkripsi password koneksi database dan menandatangani pesan flash.
  - Linux/macOS: `openssl rand -base64 32`
  - PowerShell: `$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Fill($b); [Convert]::ToBase64String($b)`
- `SEED_ADMIN_PASSWORD`: password untuk Platform Admin pertama. Username-nya `admin`, bisa diganti lewat `SEED_ADMIN_USERNAME`.

Lalu:

```bash
make up    # appdb (5432), targetdb (5433), testdb (5434)
make run   # http://localhost:8080
```

### Test

```bash
make test-short   # unit test saja, tidak butuh Docker
make test         # semua test, butuh `make up` (memakai testdb di port 5434)
```

### Menjalankan dengan Docker

Server, worker, dan SPA bisa berjalan di container tanpa Go atau Node di mesin lokal. `.env` tetap dibutuhkan untuk `APP_SECRET_KEY` dan `SEED_ADMIN_PASSWORD`.

```bash
make app-up     # build image lalu jalankan server (http://localhost:8080) dan worker
make app-down
```

Tanpa `make`: `docker compose --profile app up --build -d server worker`. Image dibangun bertahap: build SPA dengan Node, build `server` dan `worker` sebagai binary statis Go, lalu keduanya disalin ke image Alpine berukuran sekitar 75 MB yang berjalan sebagai user non-root.

Dari dalam container, koneksi ke database contoh memakai host `targetdb` port `5432`, bukan `localhost:5433`.

## Struktur kode

```
cmd/server/         titik masuk, perakitan modul (wire.go), test end-to-end
internal/auth/      login, password, session, CSRF, middleware
internal/access/    perhitungan akses efektif, semua keputusan "boleh atau tidak" ada di sini
internal/admin/     kelola user, group, keanggotaan, dan pemberian akses
internal/connmgr/   kelola koneksi database, enkripsi kredensial, cache driver
internal/dbdriver/  antarmuka driver, implementasi postgres/ dan mysql/
internal/viewer/    penjelajah schema, tabel, dan data
internal/migration/ editor, validasi, eksekusi, dan riwayat migration
internal/explain/   EXPLAIN / EXPLAIN ANALYZE (bonus)
internal/schemadiff/ perbandingan schema dan pembuatan migration (bonus)
internal/appdb/sql/ skema database internal aplikasi
web/                template dan aset statis, di-embed ke binary
```

## Data uji besar

`make seed-large` membuat tabel `public.events` berisi 3 juta baris di `sampledb` (sekitar 20 detik, 319 MB). Jumlah baris bisa diubah dengan `ROWS=500000`. Tanpa `make`:

```powershell
Get-Content docker/targetdb/large.sql -Raw | docker compose exec -T targetdb psql -U postgres -d sampledb -v ON_ERROR_STOP=1 -1 -f -
```

Hasil pengukuran di laptop pengembang: `UPDATE` seluruh tabel dalam satu transaction 25 sampai 29 detik, sedangkan satu batch 10.000 baris sekitar 55 ms. Tabel ini dipakai untuk menguji migration mode batch dan beban baca pada uji k6.

## Session

Setelah login, browser hanya menerima satu nilai acak 32 byte di cookie `wbd_session`. 

Semua parameter bisa diubah lewat `.env`:

| Parameter                     | Bawaan   | Alasan                                                                                                                                     |
| ----------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `SESSION_IDLE_TIMEOUT`      | 30 menit | Cukup panjang untuk membaca hasil query atau menyusun migration, tapi komputer yang ditinggal tidak terbuka seharian.                      |
| `SESSION_ABSOLUTE_TIMEOUT`  | 12 jam   | Kira-kira satu hari kerja. Cookie yang dicuri tetap kedaluwarsa walaupun terus dipakai.                                                    |
| `SESSION_ROTATION_INTERVAL` | 15 menit | Memperpendek umur satu nilai cookie tanpa terlalu sering menulis ke database.                                                              |
| `SESSION_GRACE_PERIOD`      | 30 detik | Cukup untuk request yang berjalan bersamaan saat rotasi terjadi. Harus lebih kecil dari interval rotasi, kalau tidak server menolak jalan. |

Setiap request ke halaman yang dilindungi diperiksa dengan urutan, session sudah dicabut, lewat batas absolut, terlalu lama diam, lalu user sudah dinonaktifkan. Kalau salah satu terpenuhi, session ditolak dan pengguna diarahkan ke halaman login. Waktu aktivitas terakhir hanya diperbarui paling sering sekali per menit. Akibatnya idle timeout bisa meleset sampai satu menit, sebagai ganti tidak menulis ke database di setiap klik.

**Rotasi.** Nilai cookie diganti saat login (selalu session baru, dan session lama dari cookie yang dibawa ikut dicabut), setiap 15 menit, dan ketika privilege seseorang naik: dijadikan admin, ditambahkan ke group, atau level akses group-nya dinaikkan. Nama cookie tetap, hanya nilainya yang berubah. Rotasi tidak memperpanjang batas 12 jam.

**Grace period.** Setelah rotasi, nilai lama masih diterima selama 30 detik, supaya request paralel atau response yang terlambat tidak membuat pengguna tiba-tiba keluar. Request yang datang dengan nilai lama tidak memicu rotasi lagi. Lewat 30 detik, nilai lama ditolak.

Ada satu batasan yang kami terima. Kalau response yang membawa cookie baru benar-benar hilang di jalan dan browser tidak mengirim request lain dalam 30 detik, pengguna harus login ulang. Memperpanjang grace period menutup celah ini, tapi juga memperpanjang umur cookie lama.

**Logout dan pencabutan** selalu terjadi di backend (kolom `revoked_at`), bukan sekadar menghapus cookie. Menonaktifkan user atau mengganti password-nya mencabut semua session milik user tersebut.

Semua form POST membawa token CSRF yang terikat ke session. Server juga memeriksa header `Origin` atau `Referer`. Form login punya token sendiri lewat cookie terpisah, karena saat itu session belum ada.

Password di-hash dengan bcrypt (cost 12). Password, cookie, dan isi form tidak pernah ditulis ke log. Pesan gagal login sama persis untuk password salah maupun username yang tidak ada.

## Akses ke database

- **View Only**: melihat schema, data, dan riwayat migration.
- **View + Edit**: semua yang di atas, ditambah membuat, mengubah, dan menjalankan migration.

Satu group bisa punya level berbeda di tiap database.

**Akses efektif adalah level tertinggi** dari semua group yang diikuti user untuk database tersebut. Kalau Budi ada di group `analis` (View Only) dan `backend` (View + Edit) yang sama-sama punya akses ke `sales`, maka Budi bisa mengedit `sales`. Kami memilih aturan ini karena tujuan memasukkan seseorang ke group adalah memberi kemampuan. Kalau ingin membatasi, keluarkan dia dari group yang lebih tinggi.

Akses dihitung ulang dari database pada setiap request, tanpa cache. Mengeluarkan user dari group atau mencabut akses group langsung berlaku di request berikutnya.

**Platform Admin bukan level akses database.** Admin mengelola user, group, koneksi, dan pemberian akses, tapi tidak otomatis bisa membuka isi database. Kalau admin juga perlu bekerja dengan sebuah database, masukkan akunnya ke group seperti user lain. Dengan begitu, siapa yang benar-benar membaca data selalu terlihat dari daftar group.

Koneksi yang dinonaktifkan tidak bisa diakses siapa pun sampai diaktifkan kembali.

## Migration

Setiap migration punya nomor versi yang naik berurutan per database (1, 2, 3, ...), SQL, nama, pembuat, pelaksana, waktu, dan status. Nomor versi tidak pernah dipakai ulang, sehingga riwayatnya linear.

Alurnya: dibuat sebagai **draft** (masih bisa diubah atau dibatalkan), lalu dijalankan. Selama berjalan statusnya **running**, kemudian menjadi **success** atau **failed**. Draft yang dibatalkan menjadi **cancelled**. Hanya draft yang bisa diubah, dijalankan, atau dibatalkan.

Eksekusi harus berurutan: sebuah versi hanya bisa dijalankan kalau tidak ada versi yang lebih kecil dan masih berstatus draft atau running. Versi yang lebih kecil harus dijalankan atau dibatalkan lebih dulu. Versi yang gagal tidak menghalangi versi berikutnya, tetapi tetap memakai nomornya dan tidak bisa diubah. Untuk memperbaikinya, tombol *Salin ke migration baru* di halaman detail membuat draft baru dengan SQL yang sama.

Seluruh SQL migration dijalankan dalam satu transaction di database target. Kalau satu statement gagal, semua di-rollback dan status menjadi failed beserta pesan error dari database. Karena itu SQL tidak boleh berisi `BEGIN`, `COMMIT`, `ROLLBACK`, atau sejenisnya, dan editor menolaknya sebelum disimpan. Batas eksekusi 60 detik. SQL maksimal 256 KB.

Dalam satu database hanya satu migration yang boleh berjalan pada satu waktu. Ini dijaga dengan advisory lock dan unique index di tabel riwayat.

**Batasan: dua database, dua transaction.** Riwayat disimpan di database aplikasi, sedangkan perubahannya terjadi di database target. Keduanya tidak bisa di-commit bersamaan secara atomik. Kalau server mati tepat setelah target meng-commit tetapi sebelum status dicatat, migration akan tertahan di status running. Saat server dinyalakan lagi, migration yang running lebih dari 5 menit ditandai failed dengan pesan bahwa kondisi target perlu diperiksa manual. Kami memilih memberi tahu dengan jujur daripada menebak hasilnya.

## Bonus

**Query execution plan.** Dari editor migration, tombol *Analisis plan* menjalankan `EXPLAIN` terhadap SQL yang sedang ditulis, atau `EXPLAIN ANALYZE` kalau kotaknya dicentang. Hasilnya ditampilkan sebagai pohon node: jenis scan, index yang dipakai, strategi join, kondisi, estimasi cost dan baris, serta untuk ANALYZE waktu aktual, baris aktual, loops, dan buffer. JSON mentahnya juga tersedia. Hanya untuk pengguna View + Edit.

ANALYZE benar-benar menjalankan statement, jadi selalu dibungkus transaction yang di-rollback, dengan batas 15 detik. Untuk berjaga-jaga, hanya DML yang diterima (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH`, `VALUES`), satu statement per analisis. DDL ditolak karena di MySQL DDL tidak bisa di-rollback.

**MySQL dan MariaDB.** Koneksi bisa bertipe PostgreSQL, MySQL, atau MariaDB, dan viewer, migration, serta explain bekerja untuk ketiganya. Bedanya, MySQL melakukan *implicit commit* pada DDL (`CREATE`, `ALTER`, `DROP`). Akibatnya migration yang berisi DDL tidak bisa di-rollback penuh kalau gagal di tengah jalan. Aplikasi mendeteksi ini dan menampilkan peringatan di hasil migration. Database contoh MySQL tersedia lewat `docker compose --profile mysql up -d targetmysql` (port 3307, user `root`, password `root`, database `sampledb`).

**Schema diff.** Dari halaman migration, pengguna View + Edit bisa membandingkan struktur database tujuan dengan database PostgreSQL lain yang ia punya akses baca. Hasilnya daftar perbedaan beserta SQL-nya, schema dan tabel baru, kolom yang ditambah, diubah tipe, nullability, atau default-nya, serta constraint dan index. Pengguna memilih perubahan mana yang diambil, lalu aplikasi membuat draft migration yang masih bisa diedit sebelum dijalankan.

## Observability (bonus M2)

```
make obs-up     # OpenTelemetry Collector, Tempo, Loki, Prometheus, Grafana
make obs-down
```

Grafana di http://localhost:3000 langsung membuka dashboard **DB Platform**. Agar server dan worker mengirim trace dan log, isi di `.env`:

```
OTEL_TRACES_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318   # server di container profil app otomatis memakai http://otel-collector:4318
```

| Sinyal | Jalur | Penyimpanan |
|---|---|---|
| Trace | aplikasi → OTLP → Collector | Tempo; Collector juga membuat metrik durasi span (`spanmetrics`) |
| Log | stdout JSON tetap ada; salinannya lewat OTLP → Collector | Loki |
| Metrik | Prometheus men-scrape `/metrics` server (8080) dan worker (9091) | Prometheus |

Konfigurasi ada di `docker/observability/`: `otel-collector.yaml`, `tempo.yaml`, `prometheus.yml`, datasource dan dashboard Grafana yang di-provision sehingga bisa dijalankan ulang. Datasource saling terhubung: dari baris log di Loki ada tombol ke trace-nya di Tempo, dan dari span ada tombol ke log dengan trace yang sama.

Metrik yang tersedia:

- **Queue dan job:** `dbplatform_queue_jobs` (per state), `dbplatform_jobs_running`, `dbplatform_jobs_finished_total` (per hasil: success, failed, retry, ...), `dbplatform_jobs_reconciled_total`, `dbplatform_queue_wait_seconds`, `dbplatform_job_duration_seconds`.
- **Migration dan lock:** `dbplatform_migration_executions` (per status), `dbplatform_migration_rows_committed`, `dbplatform_connection_locks_active`.
- **Pool koneksi:** `dbplatform_db_pool_connections` (in_use, idle), `dbplatform_db_pool_max_connections`, `dbplatform_db_pools`, `dbplatform_db_pool_waits_total`, `dbplatform_db_pool_wait_seconds_total`. Label `pool` hanya `appdb` atau `target`.
- **HTTP:** `dbplatform_http_requests_total` dan `dbplatform_http_request_duration_seconds` per pola route.

Semua label berkardinalitas rendah: route memakai pola (`/executions/{execID}/run`), bukan path asli, dan tidak ada ID user, ID job, atau SQL di label.

### Contoh korelasi dari satu eksekusi

Satu migration mode batch (versi 299 di koneksi `k6-db-1`, 200.000 baris, 4 batch) dijalankan lewat API. Semua nilai di bawah berasal dari sistem yang berjalan.

**Log.** Request `run` di server dan seluruh pekerjaan job di worker berbagi `correlation_id` `1ed8cb15cb5f382d`. Baris asli dari `docker compose logs server worker`, hanya peristiwa utama (batch 2 dan 3 serta `sql.start`/`sql.result` tidak ditampilkan):

```json
{"time":"2026-11-26T17:18:54.170877904Z","level":"INFO","msg":"migration execution changed","event":"migration.queued","actor":"admin","connection_id":5,"migration_id":1488,"execution_id":738,"version":299,"kind":"apply","status":"approved","correlation_id":"1ed8cb15cb5f382d","trace_id":"f8226936b2609219c76b05ab149a4ce0","span_id":"b03def45cfbbcb17"}
{"time":"2026-11-26T17:18:54.171195168Z","level":"INFO","msg":"http request","event":"http.request","actor":"admin","method":"POST","path":"/api/v1/executions/738/run","status":202,"duration_ms":12,"correlation_id":"1ed8cb15cb5f382d","trace_id":"f8226936b2609219c76b05ab149a4ce0","span_id":"b03def45cfbbcb17"}
{"time":"2026-11-26T17:18:54.650843656Z","level":"INFO","msg":"job claimed","worker_id":"9077ffad9b4c-1","job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","execution_id":738,"attempt":1,"actor":"system","event":"job.claimed","fence":738,"correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:54.651027738Z","level":"INFO","msg":"connection lock acquired","worker_id":"9077ffad9b4c-1","job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","execution_id":738,"attempt":1,"actor":"system","event":"lock.acquired","connection_id":5,"correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:54.715157641Z","level":"INFO","msg":"migration started","worker_id":"9077ffad9b4c-1","event":"migration.started","actor":"admin","connection_id":5,"execution_id":738,"job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","attempt":1,"mode":"batch","correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:55.538401135Z","level":"INFO","msg":"batch committed","worker_id":"9077ffad9b4c-1","event":"migration.batch_committed","execution_id":738,"job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","attempt":1,"batch":1,"direction":"forward","lo":0,"hi":50000,"rows":50000,"correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:57.78779082Z","level":"INFO","msg":"batch committed","worker_id":"9077ffad9b4c-1","event":"migration.batch_committed","execution_id":738,"job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","attempt":1,"batch":4,"direction":"forward","lo":150000,"hi":200000,"rows":50000,"correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:57.796027924Z","level":"INFO","msg":"migration attempt finished","worker_id":"9077ffad9b4c-1","event":"migration.attempt","actor":"admin","connection_id":5,"migration_id":1488,"execution_id":738,"job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","version":299,"revision":1,"kind":"apply","attempt":1,"status":"success","committed":true,"failure_code":"","error":"","query":"UPDATE public.k6_items SET counter = counter + 1, updated_at = now() WHERE id > $1 AND id <= $2","correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:57.807241156Z","level":"INFO","msg":"job finished","worker_id":"9077ffad9b4c-1","job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","execution_id":738,"attempt":1,"actor":"system","event":"job.finished","status":"success","error":"","correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
{"time":"2026-11-26T17:18:57.807374664Z","level":"INFO","msg":"connection lock released","worker_id":"9077ffad9b4c-1","job_id":"c390f630-e1ac-4ac3-9c37-4956b1f66072","execution_id":738,"attempt":1,"actor":"system","event":"lock.released","connection_id":5,"correlation_id":"1ed8cb15cb5f382d","trace_id":"60636f821fd1d6c15bbd17cfee41c274","span_id":"7f1fa866e058b76c"}
```

Di Loki, `{service_name=~"dbplatform-.*"} | correlation_id="1ed8cb15cb5f382d"` mengembalikan 62 baris: 2 dari `dbplatform-api` dan 60 dari `dbplatform-worker`, termasuk `sql.start` dan `sql.result` untuk setiap query ke database target.

**Trace.** Job berjalan setelah request selesai, jadi worker membuat trace baru yang ditautkan (span link) ke trace request, bukan child di dalamnya. Ringkasan span dari API Tempo (`/api/traces/<trace_id>`), urut waktu mulai, dengan SQL dipotong:

```
trace f8226936b2609219c76b05ab149a4ce0 (1 span)
  dbplatform-api     POST        12 ms

trace 60636f821fd1d6c15bbd17cfee41c274 (26 span)
  dbplatform-worker  queue.job   3158 ms  link -> f8226936b2609219c76b05ab149a4ce0  execution.id=738
  dbplatform-worker  db.query      24 ms  SELECT pg_try_advisory_lock($1)
  dbplatform-worker  db.query     633 ms  UPDATE public.k6_items SET counter = counter + 1, updated_at = now() WHERE id > $1 AND id <= $2
  dbplatform-worker  db.query     290 ms  UPDATE public.k6_items ... (batch 2)
  dbplatform-worker  db.query     803 ms  UPDATE public.k6_items ... (batch 3)
  dbplatform-worker  db.query     548 ms  UPDATE public.k6_items ... (batch 4)
  ... ditambah span pencatatan batch di _dbplatform
```

**Metrik.** Nilai dari Prometheus setelah job itu (worker baru dinyalakan, jadi hanya berisi job ini):

```
dbplatform_jobs_finished_total{job="worker",outcome="success"}            1
dbplatform_queue_wait_seconds_sum{job="worker"}                           0.480487297
dbplatform_job_duration_seconds_sum{job="worker",outcome="success"}       3.156373933
traces_span_metrics_calls_total{service_name="dbplatform-worker",span_name="queue.job"}  1
```

Angkanya saling cocok: tunggu queue 0,48 detik sama dengan selisih `queued_at` (17:18:54.164) dan `job.claimed` (17:18:54.650), dan durasi job 3,156 detik sama dengan span `queue.job` 3.158 ms.

## Uji beban k6 (bonus M2)

Skrip di `loadtest/` menjalankan bersamaan: baca API pada tabel 3 juta baris dengan beban naik bertahap (5, 15, 30 VU) lalu turun untuk melihat pemulihan, serta job migration mode normal dan batch ke empat database berbeda dan ke satu database yang sama. Di database yang sama, permintaan yang kalah duluan ditolak 409 dan itu dihitung sebagai hasil yang diharapkan. Keberhasilan migration dihitung dari status eksekusi akhir, bukan dari respons run.

```
make loadtest-setup
make loadtest
```

Hasil pengujian terakhir: semua ambang lulus, 7.963 request tanpa error, 191 dari 191 migration berhasil, dan p95 baca kembali ke 846 ms setelah beban turun (815 ms sebelum beban naik). Pada 30 VU baca jenuh di sekitar 10 req/s karena pool koneksi ke database target (5 koneksi) terpakai penuh. Ambang, penjelasan skrip, serta p50, p95, throughput, error rate, waktu tunggu queue, dan durasi job per tingkat beban ada di [loadtest/README.md](loadtest/README.md).

## Pembagian tugas

| Anggota                               | Bagian                                                                                                                         |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Kurt Mikhael Purba - 13524065         | Login, password, session (timeout, rotasi, grace period), CSRF, middleware otorisasi, perhitungan akses efektif, bonus EXPLAIN |
| Reva Natania Sitohang - 13524098      | Kelola koneksi dan enkripsi kredensial, driver PostgreSQL, database viewer,  bonus MySQL/MariaDB                             |
| Amanda Aurellia Salsabilla - 13524131 | Kerangka aplikasi dan renderer, migration (editor, eksekusi, riwayat), halaman admin user dan group, bonus schema diff         |
