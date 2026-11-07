# Desain detail

### Skema database (SQLite, mode WAL)

| Tabel | Kolom utama | Catatan |
| --- | --- | --- |
| `users` | id, username, password\_hash (argon2id), role, created\_at | role: `admin` / `viewer` |
| `sessions` | token\_hash, user\_id, expires\_at | cookie HttpOnly, rotasi tiap login |
| `cameras` | id, name, main\_url, sub\_url, enabled, record\_mode, record\_audio, segment\_sec, retention\_days, motion\_mode, motion\_config (JSON), created\_at | URL RTSP disimpan terenkripsi (AES-GCM, key dari env); `record_audio` default 0 |
| `segments` | id, camera\_id, path, start\_ts, end\_ts, size\_bytes, has\_motion | index (camera\_id, start\_ts) untuk timeline |
| `events` | id, camera\_id, type, start\_ts, end\_ts, score, snapshot\_path, meta (JSON) | type: `motion`, `offline`, `online`, `disk_full` |
| `notifiers` | id, kind, config (JSON), enabled | kind: `telegram`, `webhook` |
| `settings` | key, value | kuota disk global, path storage, dll. |

Skema ada di [`migrations/00001_initial.sql`](../migrations/00001_initial.sql). Tabel memakai `STRICT` dan constraint `CHECK` untuk nilai enum (`role`, `record_mode` = `continuous`/`off`, `motion_mode` = `off`/`onvif`/`framediff`, tipe event, jenis notifier) serta `json_valid` untuk kolom JSON, jadi data rusak ditolak database, bukan hanya oleh kode Go. `created_at` berupa Unix detik; `start_ts`/`end_ts` segmen dan event berupa Unix milidetik. `retention_days` kamera boleh NULL, artinya ikut `storage.default_retention_days`. Menghapus kamera ikut menghapus baris segmen dan event-nya (`ON DELETE CASCADE`), sedangkan file rekamannya dibersihkan job retensi.

URL kamera dienkripsi AES-256-GCM dengan key yang diturunkan dari `secret_key` lewat HKDF-SHA256, dengan nama kolom sebagai associated data. `secret_key` minimal 32 karakter, dan `jaga start` menolak jalan tanpanya.

### Struktur storage

```
/data
  jaga.db
  recordings/<camera_id>/<YYYY-MM-DD>/<YYYYMMDD-HHMMSS>.mp4   # UTC
  snapshots/<camera_id>/<event_id>.jpg
  thumbs/<camera_id>/latest.jpg
```

Folder per tanggal membuat retensi cukup dengan menghapus folder lama, lalu menghapus baris `segments` yang cocok. Nama file memuat tanggal lengkap karena segment list FFmpeg hanya mengirim nama file tanpa folder, jadi supervisor menurunkan folder dari nama itu. FFmpeg tidak membuat folder sendiri, sehingga supervisor membuat folder hari ini dan besok saat start dan tiap jam. Tanpa itu, rekaman mati tepat tengah malam ([spike](spikes/segment-index.md)).

### Perintah FFmpeg recording

```
ffmpeg -nostdin -protocol_whitelist rtsp,rtsps,rtp,udp,tcp,tls \
  -rtsp_transport tcp -timeout 5000000 -i <main_url> \
  -c copy -map 0:v [-map 0:a?  # hanya jika record_audio] \
  -f segment -segment_time 600 -segment_format mp4 \
  -segment_format_options movflags=+frag_keyframe+empty_moov \
  -reset_timestamps 1 -strftime 1 \
  -segment_list pipe:1 -segment_list_type csv \
  /data/recordings/<id>/%Y-%m-%d/%Y%m%d-%H%M%S.mp4   # env TZ=UTC
```

Argumen dibangun sebagai slice dan dijalankan dengan `exec.Command` tanpa shell. `-protocol_whitelist` mencegah URL seperti `file:` atau `concat:` dipakai untuk membaca file lokal. `-segment_list pipe:1` membuat FFmpeg menulis nama setiap segmen yang selesai ke stdout. Supervisor membacanya lalu meng-insert baris `segments` tanpa perlu polling filesystem. `end_ts` adalah waktu baris itu diterima dan `start_ts = end_ts − durasi` dari `ffprobe` file tersebut. Nama file tidak dipakai sebagai sumber waktu karena segmen pertama bisa telat ±3 s (FFmpeg baru membuka file setelah probing input), dan kolom start/end di CSV memuat offset timestamp kamera. Fragmented MP4 membuat file sering masih bisa diputar walau proses di-kill di tengah segmen, tetapi segmen itu tidak masuk list dan kadang rusak total. Karena itu rekonsiliasi saat startup dan setiap job retensi memeriksa file tanpa baris DB dengan `ffprobe`: file yang terbaca di-insert (`end_ts` = mtime), file rusak dihapus.

### State machine supervisor (per kamera)

1. `idle` → `starting`: kamera enabled, spawn FFmpeg.
2. `starting` → `running`: segmen pertama tercatat atau stderr menunjukkan frame masuk dalam 15 detik.
3. `running` → `stalled`: tidak ada output baru selama 2× `segment_sec` atau progress (`-progress pipe:2`) berhenti 30 detik. Proses di-kill.
4. `stalled` / `exited` → `backoff`: tunggu 2 s, 4 s, 8 s ... maks 60 s, lalu kembali ke `starting`. Event `offline` dicatat setelah 3 kali gagal. Setiap exit yang tidak diminta supervisor masuk ke sini, **termasuk exit 0**, karena FFmpeg keluar dengan kode 0 saat `-timeout` RTSP habis. Exit code tidak dipakai untuk menentukan penyebab; alasannya diambil dari baris stderr terakhir.
5. `running` → `stopping` → `idle`: kamera di-disable atau config berubah (SIGINT dulu agar segmen tertutup rapi, SIGKILL setelah 5 detik).

### Motion detection

- **Mode `onvif`**: subscribe PullPoint event kamera (`tns1:RuleEngine/CellMotionDetector`). Hampir nol CPU.
- **Mode `framediff`**: FFmpeg kedua membaca `sub_url`, keluarkan raw gray 320×240 @ 3 fps ke stdout (`-vf scale=320:240,format=gray -r 3 -f rawvideo pipe:1`). Go membandingkan frame berurutan: hitung persentase piksel yang berubah lebih dari threshold di dalam mask zona.
- **Debounce**: event dibuka saat skor melewati ambang N frame berturut-turut, ditutup setelah 10 detik tenang. Snapshot diambil di frame dengan skor tertinggi.
- Segmen yang overlap dengan event ditandai `has_motion = 1`, sehingga timeline bisa menyorot dan retensi bisa memprioritaskan menyimpannya.

### Retensi & disk

- Job tiap 10 menit: hapus segmen lebih tua dari `retention_days` per kamera.
- Jika pemakaian disk > kuota (default 90%): hapus segmen tertua global yang `has_motion = 0`, baru kemudian yang bermotion. Catat event `disk_full` jika tetap penuh.

### REST API (prefix `/api/v1`)

| Method | Path | Fungsi |
| --- | --- | --- |
| POST | `/auth/login`, `/auth/logout` | sesi cookie |
| GET / POST | `/cameras` | list & tambah kamera |
| GET / PATCH / DELETE | `/cameras/{id}` | detail, ubah, hapus |
| POST | `/cameras/{id}/test` | probe RTSP via ffprobe |
| GET | `/cameras/{id}/status` | state supervisor, fps, bitrate, uptime |
| GET | `/cameras/{id}/snapshot` | JPEG terbaru |
| GET | `/cameras/{id}/segments?from=&to=` | data timeline |
| GET | `/segments/{id}/file` | stream MP4 dengan HTTP Range |
| GET | `/cameras/{id}/clip?from=&to=` | gabung segmen via concat demuxer (copy) |
| GET | `/events?camera=&from=&to=&type=` | daftar event |
| GET / POST / PATCH | `/notifiers` | konfigurasi notifikasi |
| GET | `/system` | CPU, RAM, disk, versi |
| GET | `/ws` | WebSocket push status kamera & event realtime |
| ANY | `/live/*` | reverse proxy ke go2rtc (WebRTC/MSE), dicek sesi |

### Keamanan

- Semua endpoint, termasuk proxy live, wajib sesi valid.
- **go2rtc dikunci.** API hanya listen di `127.0.0.1` (atau unix socket bila tersedia). Server RTSP bawaannya (`:8554`) dimatikan, sehingga yang terbuka hanya port media WebRTC. Ini krusial karena API go2rtc bisa membuat source `exec:` yang menjalankan perintah arbitrer.
- **Proxy `/live/*` memakai whitelist.** Yang diteruskan hanya signaling WebRTC/MSE dan `frame.jpeg` untuk nama stream milik kamera yang boleh diakses user tersebut. Endpoint `/api/config`, `/api/streams` (tulis), dan lainnya selalu ditolak.
- **Validasi URL kamera.**
  - Skema hanya `rtsp`/`rtsps`, di-parse dengan `net/url`.
  - Tolak spasi, newline, `#`, dan URL yang diawali `-`.
  - Config go2rtc ditulis lewat YAML marshaller, sehingga URL tidak pernah bisa menjadi source `exec:`/`ffmpeg:`.
- Kredensial kamera tidak pernah dikirim ke frontend. API mengembalikan URL yang sudah dimasking, dan logger menyamarkan `user:pass@`.
- **File diakses lewat ID, bukan path.** `/segments/{id}/file` mengambil path dari DB lalu memastikan hasil `filepath.Clean` berada di bawah `data_dir`. Daftar concat untuk export klip dibangun dari DB dengan cara yang sama.
- Rate limit login (5 percobaan per menit per IP), CSRF token untuk request yang mengubah data, dan cookie `HttpOnly; SameSite=Lax`, ditambah `Secure` bila memakai HTTPS.
- Header `Content-Security-Policy` ketat (tanpa inline script, tanpa CDN). Nama kamera dan input lain di-escape oleh Svelte.
- URL webhook hanya boleh diatur admin. Tolak alamat loopback dan link-local secara default (cegah SSRF ke go2rtc/metadata).
- **Tidak ada akun default.** `jaga start` menolak jalan bila belum ada admin atau `secret_key` kosong.
- **Tanpa telemetri.** Cek versi baru hanya aktif kalau pengguna mengaktifkannya (`update_check: false` default).
