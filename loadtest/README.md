# Uji beban k6

Skrip `load.js` menjalankan tiga beban sekaligus selama 7 tahap (bawaan 30 detik per tahap):

| Skenario | Yang dilakukan |
|---|---|
| `api_read` | Membaca halaman acak tabel `public.events` (3 juta baris) lewat viewer. Jumlah VU naik bertahap: normal 5, tinggi 15, puncak 30, lalu turun lagi ke 5 untuk melihat pemulihan. |
| `jobs_db_1` sampai `jobs_db_4` | Satu VU per database berbeda (`k6_db_1..4`). Setiap iterasi mengajukan apply untuk versi ready terkecil, menekan run, lalu memantau job sampai selesai. |
| `jobs_same_db` | Empat VU berebut database yang sama (`k6_shared`). Untuk satu versi hanya satu permintaan yang diterima; sisanya **409 dan itu hasil yang diharapkan**, bukan error. |

Migration berganti-ganti mode normal (versi ganjil) dan batch (versi genap, 50.000 baris per batch). Semua menaikkan kolom `counter` di 200.000 baris `k6_items`.

Keberhasilan migration hanya dihitung dari status eksekusi akhir (`success`) setelah job selesai, bukan dari respons 202 saat run ditekan.

## Menjalankan

Syarat: profil `app` berjalan (`make app-up`) dan `make seed-large` sudah pernah dijalankan.

```
make loadtest-setup   # buat ulang database target k6
make loadtest         # jalankan k6 dari image grafana/k6, ringkasan di loadtest/results/summary.json
```

Tanpa `make` (PowerShell):

```
Get-Content loadtest/targets.sql -Raw | docker compose exec -T targetdb psql -U postgres -d sampledb -q -f -
docker compose --profile loadtest run --rm k6
```

Pengaturan lewat `.env` atau variabel lingkungan:

| Variabel | Bawaan | Fungsi |
|---|---|---|
| `K6_STAGE_SECONDS` | 30 | lama setiap tahap; total 7 tahap |
| `K6_BASE_URL` | `http://server:8080` | server yang diuji; untuk `make run` di host pakai `http://host.docker.internal:8080` |
| `K6_TARGET_HOST`, `K6_TARGET_PORT` | `targetdb`, `5432` | alamat database target dilihat dari server; untuk `make run` pakai `localhost`, `5433` |

`setup()` menyiapkan semuanya lewat API dengan akun admin: koneksi `k6-*`, group `k6-loadtest` (admin sebagai leader sehingga permintaan langsung disetujui), dan migration ready untuk seluruh durasi. Sisa migration dari pengujian sebelumnya dibatalkan dulu, jadi pengujian bisa diulang tanpa reset.

## Ambang lulus

| Ambang | Nilai |
|---|---|
| Error rate request (semua skenario, semua tingkat beban) | < 1% |
| p95 baca pada beban normal dan setelah pulih | < 2 detik |
| Migration yang berhasil | > 99% |
| Semua check | > 99% |

Satu halaman acak dari 3 juta baris memakai OFFSET dalam; mediannya sekitar 0,3–0,4 detik walau beban rendah, sehingga batas 2 detik memberi ruang sekitar 5 kali. Pada beban tinggi dan puncak latensi boleh naik, asalkan tidak ada error dan kembali turun setelah beban dikurangi.

`teardown()` memeriksa kebenaran hasil, bukan hanya kecepatan:

- database yang sama: setiap versi hanya punya satu eksekusi;
- isi tabel: `counter` di setiap database naik tepat sebanyak migration yang berhasil.

## Hasil (27 November 2026, laptop pengembang, semua container di satu mesin)

Semua ambang lulus. 7.963 request, 0 error. 191 dari 191 migration berhasil (126 di database berbeda, 65 di database yang sama). 822 permintaan di database yang sama ditolak 409 sesuai harapan.

**Baca API per tingkat beban**

| Tingkat | VU | p50 | p95 | Throughput | Error rate |
|---|---|---|---|---|---|
| normal | 5 | 359 ms | 815 ms | 4,0 req/s | 0% |
| tinggi | 15 | 636 ms | 1,55 s | 10,0 req/s | 0% |
| puncak | 30 | 1,64 s | 4,92 s | 9,4 req/s | 0% |
| turun | 30 → 5 | 1,36 s | 3,20 s | 8,9 req/s | 0% |
| pemulihan | 5 | 418 ms | 846 ms | 5,5 req/s | 0% |

**Job migration per tingkat beban** (tingkat dihitung saat job diajukan)

| Tingkat | Job selesai | Throughput | Tunggu queue p50 / p95 | Selesai (run sampai hasil) p50 / p95 |
|---|---|---|---|---|
| normal | 57 | 0,95 job/s | 767 ms / 2,19 s | 3,84 s / 4,98 s |
| tinggi | 53 | 0,88 job/s | 691 ms / 983 ms | 3,34 s / 4,65 s |
| puncak | 24 | 0,80 job/s | 766 ms / 1,11 s | 4,34 s / 6,70 s |
| turun | 28 | 0,93 job/s | 497 ms / 963 ms | 2,92 s / 3,64 s |
| pemulihan | 29 | 0,97 job/s | 697 ms / 1,00 s | 2,86 s / 3,62 s |

Durasi eksekusi di worker p50 / p95: mode normal 2,37 s / 3,63 s, mode batch 2,71 s / 4,19 s (200.000 baris, 4 batch).

**Analisis**

- Throughput baca naik dari 4,0 ke 10,0 req/s saat VU naik 3 kali, tetapi di 30 VU justru turun sedikit (9,4 req/s) sementara p95 naik ke 4,92 s. Sistem jenuh di sekitar 10 req/s untuk baca halaman acak. Penyebabnya terlihat di Prometheus: pool koneksi server ke database target berisi 5 koneksi dan terpakai penuh (5 dari 5), dengan sekitar 1.150 kali peminjam harus menunggu koneksi, rata-rata 0,84 s per tunggu. Request mengantre di pool, bukan gagal.
- Setelah beban kembali normal, p95 baca turun ke 846 ms, hampir sama dengan sebelum beban naik (815 ms), dan tidak ada error di seluruh pengujian. Sistem pulih tanpa intervensi.
- Job tetap berjalan selama puncak baca. Waktu selesai p95 naik dari sekitar 5 s ke 6,7 s dan throughput job turun sekitar 16%, terutama karena eksekusi SQL melambat (worker dan pembacaan berbagi server PostgreSQL yang sama), bukan karena antrean: tunggu queue p95 di puncak tetap sekitar 1 s. Tunggu queue p95 tertinggi (2,19 s) justru di awal tahap normal, saat semua VU job mengirim job pertamanya bersamaan.
- Database berbeda berjalan paralel (4 database, 126 job). Di database yang sama, job berjalan satu per satu sesuai aturan urutan; VU yang kalah mendapat 409 dan mencoba versi berikutnya, dan teardown membuktikan tidak ada versi yang dieksekusi dua kali.
- Ambang hanya bermakna untuk durasi penuh (`K6_STAGE_SECONDS` 30 atau lebih). Dengan tahap yang sangat pendek, tahap pemulihan terlalu singkat untuk menguras antrean dari puncak, jadi gunakan durasi pendek hanya untuk uji cepat alurnya.
