# CLI

Semuanya dijalankan lewat satu binary `jaga`. Perintah sehari-hari cukup `jaga start`. Perintah lain hanya untuk setup dan perawatan.

### Perintah

| Perintah | Fungsi |
| --- | --- |
| `jaga start` | Jalankan server: API, UI, supervisor, retensi. Ini perintah default. |
| `jaga init [--admin <nama>] [--data-dir <dir>]` | Pertama kali: tulis `jaga.yml` (mode 0600) dengan `secret_key` acak 256 bit, buat admin pertama (default `admin`, password dari stdin). Tidak pernah menimpa config yang sudah ada |
| `jaga doctor` | Cek config, `secret_key`, `ffmpeg`/`ffprobe`/go2rtc, izin tulis `data_dir`, sisa disk vs kuota, DB + admin, port bentrok. Tidak mengubah apa pun. Exit code dari masalah pertama |
| `jaga probe <rtsp-url>` | Uji URL kamera via ffprobe (codec, resolusi, fps, bitrate) tanpa menyimpannya |
| `jaga camera list` | Daftar kamera beserta status (butuh server berjalan atau baca DB langsung) |
| `jaga user add <nama> [--role admin\|viewer]` | Tambah user (default `admin`). Password dibaca dari stdin: di terminal tanpa echo, atau lewat pipe (`echo … \| jaga user add ryan`) |
| `jaga user passwd <nama>` | Reset password (untuk admin yang lupa password) |
| `jaga migrate` | Jalankan migrasi DB tanpa start server |
| `jaga backup <file>` | Salinan konsisten `jaga.db` (kamera, user, index rekaman) via `VACUUM INTO`, aman saat Jaga jalan, file 0600, menolak menimpa. Restore: stop Jaga, salin balik ke `data_dir/jaga.db`. `secret_key` wajib ikut disimpan. Rekaman (MP4 biasa) tidak ikut. |
| `jaga version` | Versi, commit, versi FFmpeg & go2rtc yang terdeteksi |

Flag global: `--config <path>`, `--data-dir <path>`, `--log-level debug|info|warn|error`, `--log-format text|json`, `--no-color`.

Urutan pencarian config: `--config`, lalu `$JAGA_CONFIG`, `./jaga.yml`, `/etc/jaga/jaga.yml`. Setiap key bisa di-override lewat env `JAGA_<KEY>`, misalnya `JAGA_LISTEN=:9090` atau `JAGA_STORAGE_DISK_QUOTA_PERCENT=85`.

Exit code: `0` sukses, `1` error umum, `2` config tidak valid, `3` dependensi hilang (ffmpeg/go2rtc).

### Contoh output `jaga start`

```
$ jaga start
jaga v0.1.0  ·  http://0.0.0.0:8080  ·  /mnt/cctv (412 GB free)

  ● REC  depan-rumah   1920×1080  H.264  2.1 Mbps
  ● REC  garasi        1920×1080  H.265  1.8 Mbps
  ○ off  belakang      connection refused, retry in 8s

✓ ready in 3.2s  ·  3 cameras, 2 recording
```

Pewarnaan mengikuti [palet CLI](07-branding.md#warna-di-cli): `jaga` dan versi amber, `● REC` merah, `✓` hijau, detail teknis abu-abu. Setelah baris `ready`, output beralih ke log biasa:

```
22:41:07 INF camera online     camera=belakang after=24s
22:58:13 WRN motion            camera=garasi score=0.18 snapshot=snapshots/2/481.jpg
23:00:00 INF retention         deleted=36 freed=7.4GB
```

Di mode non-TTY (systemd, Docker), banner tidak ditampilkan dan log langsung ditulis apa adanya. Dengan `--log-format json`, setiap baris berupa satu objek JSON.

### Contoh output `jaga doctor`

```
$ jaga doctor
✓ config       jaga.yml
✓ secret_key   set
✓ ffmpeg       ffmpeg 9.0.2 (/usr/local/bin/ffmpeg)
✓ ffprobe      found
✓ go2rtc       go2rtc 1.9.15 (/opt/jaga/go2rtc)
✓ data_dir     /work/data  writable  44.7 GB free (55% used, quota 90%)
✓ database     /work/data/jaga.db, admin present
✓ port 8080/tcp listen free
✗ port 8555/udp webrtc: listen udp :8555: bind: address already in use
  → stop the other program or set go2rtc.webrtc_port in jaga.yml
```

Exit code mengikuti masalah pertama dari atas (config `2`, dependensi `3`, lainnya `1`), sehingga yang perlu dibereskan duluan selalu yang paling atas. Tanpa go2rtc, rekaman tetap jalan dan hanya live view yang mati.

### Contoh `jaga init`

```
$ echo "$PASS" | jaga init --admin ryan --data-dir /mnt/cctv
✓ jaga.yml written (mode 0600), back up its secret_key
✓ admin "ryan" created in /mnt/cctv/jaga.db
  next: jaga doctor, then jaga start
```

### Implementasi

- Parsing perintah: package `flag` dari standard library, satu `FlagSet` per perintah sehingga flag global boleh ditaruh sebelum atau sesudah perintah. Ganti ke `spf13/cobra` bila perintah mulai bertingkat (`jaga user add`, `jaga camera list`) atau butuh shell completion.
- Output: `charmbracelet/lipgloss` untuk warna, `log/slog` dengan handler teks berwarna untuk TTY dan handler JSON untuk non-TTY.
- Lokasi kode: `cmd/jaga/` hanya menangkap sinyal dan memanggil `cli.Run`, `internal/cli/` untuk tiap subcommand, `internal/config/` untuk `jaga.yml` + env, dan `internal/ui/term/` untuk palet dan simbol.
- Sudah ada: `start`, `init`, `doctor`, `migrate`, `version` (termasuk versi go2rtc), `probe`, `user add|passwd`, `backup`. Belum ada (menyusul): `camera`, `--no-color` dan banner berwarna (bersama `internal/ui/term`).
- `jaga init` sengaja bukan wizard interaktif: hanya password yang ditanya, nilai lain lewat flag, sehingga bisa dipakai di script dan Docker (`echo "$PASS" | jaga init --admin ryan`).

### Contoh output `jaga probe`

```
$ jaga probe rtsp://admin:pass@192.168.0.60:554/stream1 --seconds 6
✓ rtsp://***@192.168.0.60:554/stream1  (sampled 5.8s)
  video  H264 1920×1080 25 fps  2.0 Mbps  keyframe 2.0s
  audio  AAC 16000 Hz 1 ch  32 kbps
```

Default 5 detik sampling (`--seconds 1-60`). Password di URL selalu disamarkan, juga di pesan error (`404 Not Found`, `Connection refused`). Logikanya (`internal/probe`) sama dengan yang dipakai `POST /cameras/{id}/test`.
