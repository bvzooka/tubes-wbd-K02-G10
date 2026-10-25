# Database Management Platform

Tugas Besar IF3110 Pengembangan Aplikasi Web, Milestone 1, kelompok K02-G10.

Membuat sebuah sistem yang mengatur akses ke database bersama melalui user dan group, memungkinkan pengguna menjelajahi schema dan data dari browser, serta mencatat setiap perubahan struktur sebagai migration yang dijalankan dalam transaction dan tersimpan riwayatnya.

## Menjalankan

Yang perlu ada: Go 1.26 dan Docker.

```bash
cp .env.example .env
```

Isi dua nilai di `.env`:

- `APP_SECRET_KEY`: base64 dari tepat 32 byte acak untuk mengenkripsi password koneksi database dan menandatangani pesan flash.
  - Linux/macOS: `openssl rand -base64 32`
  - PowerShell: `$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Fill($b); [Convert]::ToBase64String($b)`
- `SEED_ADMIN_PASSWORD`: password untuk Platform Admin pertama.

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

Struktur kode

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

## Session

Setelah login, browser hanya menerima satu nilai acak 32 byte di cookie `wbd_session`. Nilai itu tidak berisi informasi apa pun. Tabel `sessions` menyimpan hash SHA-256-nya, bukan nilai aslinya, jadi isi database yang bocor tidak bisa dipakai untuk masuk. Cookie diberi `HttpOnly` dan `SameSite=Lax`.

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

Seluruh SQL migration dijalankan dalam satu transaction di database target. Kalau satu statement gagal, semua di-rollback dan status menjadi failed beserta pesan error dari database. Karena itu SQL tidak boleh berisi `BEGIN`, `COMMIT`, `ROLLBACK`, atau sejenisnya, dan editor menolaknya sebelum disimpan. Batas eksekusi 60 detik. SQL maksimal 256 KB.

Dalam satu database hanya satu migration yang boleh berjalan pada satu waktu. Ini dijaga dengan advisory lock dan unique index di tabel riwayat.

**Batasan: dua database, dua transaction.** Riwayat disimpan di database aplikasi, sedangkan perubahannya terjadi di database target. Keduanya tidak bisa di-commit bersamaan secara atomik. Kalau server mati tepat setelah target meng-commit tetapi sebelum status dicatat, migration akan tertahan di status running. Saat server dinyalakan lagi, migration yang running lebih dari 5 menit ditandai failed dengan pesan bahwa kondisi target perlu diperiksa manual. Kami memilih memberi tahu dengan jujur daripada menebak hasilnya.

## Bonus

**Query execution plan.** Dari editor migration, tombol *Analisis plan* menjalankan `EXPLAIN` terhadap SQL yang sedang ditulis, atau `EXPLAIN ANALYZE` kalau kotaknya dicentang. Hasilnya ditampilkan sebagai pohon node: jenis scan, index yang dipakai, strategi join, kondisi, estimasi cost dan baris, serta untuk ANALYZE waktu aktual, baris aktual, loops, dan buffer. JSON mentahnya juga tersedia. Hanya untuk pengguna View + Edit.

ANALYZE benar-benar menjalankan statement, jadi selalu dibungkus transaction yang di-rollback, dengan batas 15 detik. Untuk berjaga-jaga, hanya DML yang diterima (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH`, `VALUES`), satu statement per analisis. DDL ditolak karena di MySQL DDL tidak bisa di-rollback.

**MySQL dan MariaDB.** Koneksi bisa bertipe PostgreSQL, MySQL, atau MariaDB, dan viewer, migration, serta explain bekerja untuk ketiganya. Bedanya, MySQL melakukan *implicit commit* pada DDL (`CREATE`, `ALTER`, `DROP`). Akibatnya migration yang berisi DDL tidak bisa di-rollback penuh kalau gagal di tengah jalan. Aplikasi mendeteksi ini dan menampilkan peringatan di hasil migration. Database contoh MySQL tersedia lewat `docker compose --profile mysql up -d targetmysql` (port 3307, user `root`, password `root`, database `sampledb`).

**Schema diff.** Dari halaman migration, pengguna View + Edit bisa membandingkan struktur database tujuan dengan database PostgreSQL lain yang ia punya akses baca. Hasilnya daftar perbedaan beserta SQL-nya, schema dan tabel baru, kolom yang ditambah, diubah tipe, nullability, atau default-nya, serta constraint dan index. Pengguna memilih perubahan mana yang diambil, lalu aplikasi membuat draft migration yang masih bisa diedit sebelum dijalankan.

| Anggota                          | Bagian                                                                                                                         |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Kurt Mikhael Purba - 13524065    | Login, password, session (timeout, rotasi, grace period), CSRF, middleware otorisasi, perhitungan akses efektif, bonus EXPLAIN |
| Reva Natania Sitohang - 13524098 | Kelola koneksi dan enkripsi kredensial, driver PostgreSQL, database viewer,  bonus MySQL/MariaDB                             |
| Amanda Aurellia Salsabila        | Kerangka aplikasi dan renderer, migration (editor, eksekusi, riwayat), halaman admin user dan group, bonus schema diff         |
