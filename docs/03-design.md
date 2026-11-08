# Desain detail

### Skema database (SQLite, mode WAL)

| Tabel | Kolom utama | Catatan |
| --- | --- | --- |
| `users` | id, username, password\_hash (argon2id), role, created\_at | role: `admin` / `viewer` |
| `sessions` | token\_hash, user\_id, expires\_at | cookie HttpOnly, rotasi tiap login |
| `cameras` | id, name, main\_url, sub\_url, enabled, record\_mode, record\_stream, record\_audio, segment\_sec, retention\_days, motion\_mode, motion\_config (JSON), created\_at | URL RTSP disimpan terenkripsi (AES-GCM, key dari env); `record_audio` default 0 |
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
ffmpeg -nostdin -hide_banner -nostats -loglevel warning -progress pipe:3 \
  -protocol_whitelist rtsp,rtsps,rtp,udp,tcp,tls \
  -rtsp_transport tcp -timeout 5000000 -i <main_url | sub_url bila record_stream=sub> \
  -map 0:v -c:v copy [-map 0:a? -c:a aac -b:a 64k  # hanya jika record_audio] \
  -f segment -segment_time 600 -segment_format mp4 \
  -segment_format_options movflags=+frag_keyframe+empty_moov \
  -reset_timestamps 1 -strftime 1 \
  -segment_list pipe:1 -segment_list_type csv \
  /data/recordings/<id>/%Y-%m-%d/%Y%m%d-%H%M%S.mp4   # env TZ=UTC
```

Video selalu di-copy. Audio (bila `record_audio`) di-encode ke AAC karena kamera umumnya mengirim G.711 (Tapo, Hikvision, Dahua) yang tidak bisa masuk MP4; biayanya beberapa persen satu core per kamera. Argumen dibangun sebagai slice oleh `internal/recorder` (`recorder.Build`) dan dijalankan dengan `exec.Command` tanpa shell. URL divalidasi ulang di sana dan selalu menjadi satu argumen tepat setelah `-i`. `-loglevel warning` tetap menampilkan error koneksi (`Connection refused`, `404`) yang dibaca supervisor sebagai alasan restart. `-protocol_whitelist` mencegah URL seperti `file:` atau `concat:` dipakai untuk membaca file lokal. `-segment_list pipe:1` membuat FFmpeg menulis nama setiap segmen yang selesai ke stdout. Supervisor membacanya lalu meng-insert baris `segments` tanpa perlu polling filesystem. `end_ts` adalah waktu baris itu diterima dan `start_ts = end_ts − durasi` dari `ffprobe` file tersebut. Nama file tidak dipakai sebagai sumber waktu karena segmen pertama bisa telat ±3 s (FFmpeg baru membuka file setelah probing input), dan kolom start/end di CSV memuat offset timestamp kamera. Fragmented MP4 membuat file sering masih bisa diputar walau proses di-kill di tengah segmen, tetapi segmen itu tidak masuk list dan kadang rusak total. Karena itu rekonsiliasi saat startup dan setiap job retensi memeriksa file tanpa baris DB dengan `ffprobe`: file yang terbaca di-insert (`end_ts` = mtime), file rusak dihapus.

### State machine supervisor (per kamera)

1. `idle` → `starting`: kamera enabled, spawn FFmpeg.
2. `starting` → `running`: segmen pertama tercatat atau stderr menunjukkan frame masuk dalam 15 detik.
3. `running` → `stalled`: tidak ada segmen baru selama 2× `segment_sec` + 30 detik, atau progress (`-progress pipe:3`, lewat `ExtraFiles` agar stderr tetap untuk pesan error) tidak menunjukkan frame/`out_time` bertambah selama 30 detik. Proses di-kill.
4. `stalled` / `exited` → `backoff`: tunggu 2 s, 4 s, 8 s ... maks 60 s, lalu kembali ke `starting`. Event `offline` dibuka setelah 3 kali gagal berturut-turut (alasan di `meta.reason`) dan ditutup begitu frame kembali masuk; event yang masih terbuka dari run sebelumnya ditutup saat start. Recorder yang sudah jalan selama 60 detik mereset backoff ke 2 s. Setiap exit yang tidak diminta supervisor masuk ke sini, **termasuk exit 0**, karena FFmpeg keluar dengan kode 0 saat `-timeout` RTSP habis. Exit code tidak dipakai untuk menentukan penyebab; alasannya diambil dari baris stderr terakhir.
5. `running` → `stopping` → `idle`: kamera di-disable atau config berubah (SIGINT dulu agar segmen tertutup rapi, SIGKILL setelah 5 detik).

**Graceful shutdown.** SIGINT/SIGTERM pertama ke `jaga` menghentikan semua recorder sekaligus: SIGINT ke setiap FFmpeg, tunggu segmen terakhirnya ditutup dan masuk tabel `segments`, SIGKILL untuk FFmpeg yang belum keluar setelah 5 detik. Waktunya tidak bertambah dengan jumlah kamera (3 kamera berhenti dalam ±0,6 s saat diuji). Sinyal kedua langsung mematikan `jaga` bila shutdown macet. Batas stop dari service manager harus lebih dari 5 detik: Docker default 10 s sudah cukup, untuk systemd pakai `TimeoutStopSec` minimal 15 s.

### Motion detection

- **Mode `onvif`**: subscribe PullPoint event kamera (`tns1:RuleEngine/CellMotionDetector`). Hampir nol CPU.
- **Mode `framediff`**: FFmpeg kedua membaca `sub_url`, keluarkan raw gray 320×240 @ 3 fps ke stdout (`-vf scale=320:240,format=gray -r 3 -f rawvideo pipe:1`). Go membandingkan frame berurutan: hitung persentase piksel yang berubah lebih dari threshold di dalam mask zona.
- **Debounce**: event dibuka saat skor melewati ambang N frame berturut-turut, ditutup setelah 10 detik tenang. Snapshot diambil di frame dengan skor tertinggi.
- Segmen yang overlap dengan event ditandai `has_motion = 1`, sehingga timeline bisa menyorot dan retensi bisa memprioritaskan menyimpannya.

### Retensi & disk

- Job (`internal/retention`) jalan saat start lalu tiap 10 menit: hapus segmen yang `end_ts`-nya lebih tua dari `retention_days` kamera (NULL = `storage.default_retention_days`).
- Jika pemakaian filesystem `data_dir` (dihitung seperti `df`) > kuota (default 90%): hapus segmen tertua global yang `has_motion = 0`, baru kemudian yang bermotion, **secukupnya** sampai di bawah kuota. Event `disk_full` dibuka bila tetap penuh (isi disk lain yang memenuhi) dan ditutup begitu kembali di bawah kuota.
- File dihapus dulu, baru barisnya. Jika file gagal dihapus, baris dibiarkan agar dicoba lagi; jika file sudah tidak ada, baris tetap dihapus. Path di luar `data_dir/recordings` tidak pernah dihapus.
- Folder tanggal yang kosong dan lebih tua dari hari ini ikut dihapus; folder hari ini dan besok dibiarkan untuk FFmpeg.
- **Rekonsiliasi** (`Reconcile`) jalan sebelum setiap putaran retensi, termasuk saat start. File `.mp4` berpola nama Jaga tanpa baris DB di-`ffprobe`: yang terbaca di-insert (`end_ts` = mtime, `start_ts` = `end_ts` − durasi), yang rusak dihapus. File milik kamera yang sudah dihapus ikut dihapus. Baris yang file-nya hilang dihapus. File yang diubah kurang dari 2 menit lalu tidak disentuh, karena mungkin masih ditulis FFmpeg. File dengan nama lain dibiarkan.

### REST API (prefix `/api/v1`)

| Method | Path | Fungsi |
| --- | --- | --- |
| POST | `/auth/login`, `/auth/logout` | sesi cookie (login dibatasi 5×/menit/IP) |
| GET | `/auth/me` | user yang sedang login |
| GET / POST | `/cameras` | list & tambah kamera |
| GET / PATCH / DELETE | `/cameras/{id}` | detail, ubah, hapus |
| POST | `/cameras/{id}/test` | probe RTSP via ffprobe |
| GET | `/cameras/{id}/status` | state recorder (`idle`, `starting`, `recording`, `retrying`, `offline`), sejak kapan, error terakhir |
| POST | `/cameras/{id}/ptz` | `{"action":"move","pan":-1..1,"tilt":-1..1,"zoom":-1..1}` atau `{"action":"stop"}`; 204, 422 bila kamera tanpa `onvif_port`, 502 bila kamera menolak (pesan dari kamera, tanpa URL) |
| GET | `/cameras/{id}/snapshot` | JPEG terbaru |
| GET | `/cameras/{id}/segments?from=&to=` | data timeline |
| GET | `/segments/{id}/file` | stream MP4 dengan HTTP Range |
| GET | `/cameras/{id}/clip?from=&to=` | export klip MP4 (concat, copy) |
| GET | `/cameras/{id}/clip?from=&to=` | gabung segmen via concat demuxer (copy) |
| GET | `/events?camera=&from=&to=&type=` | daftar event |
| GET / POST / PATCH | `/notifiers` | konfigurasi notifikasi |
| GET | `/system` | CPU, RAM, disk, versi |
| GET | `/events/stream` | Server-Sent Events: `snapshot` semua kamera saat tersambung, lalu `status` per perubahan; `: ping` tiap 25 s |
| ANY | `/live/*` | reverse proxy ke go2rtc (WebRTC/MSE), dicek sesi |

Sudah ada: `/auth/*`, `/cameras` (GET/POST), `/cameras/{id}` (GET/PATCH/DELETE), `/cameras/{id}/test`, `/cameras/{id}/snapshot` (502 bila kamera tidak terjangkau; `?stream=main` resolusi penuh, `?download=1` dengan nama file), `/cameras/{id}/status`, `/cameras/{id}/segments?from=&to=` (Unix ms, overlap, maks 48 jam, `[]` bila kosong), `/segments/{id}/file` (Range, `?download=1`; path dari DB dicek `filepath.IsLocal` di bawah `recordings/`, 404 bila file sudah dihapus retensi), `/cameras/{id}/clip?from=&to=` (Unix ms, maks 2 jam; segmen yang overlap digabung FFmpeg concat `-c copy` lalu di-stream sebagai fragmented MP4 tanpa file sementara; potongan jatuh di keyframe, celah rekaman dilewati; 404 bila tidak ada rekaman, 500 bila FFmpeg gagal sebelum byte pertama), `/events/stream`, `/live/*`. Perubahan kamera lewat API langsung diterapkan ke recorder (start/stop/restart hanya kamera yang berubah; rename tidak me-restart) dan ke go2rtc. Role `viewer` hanya boleh GET, ditambah `POST /cameras/{id}/ptz` (hanya mengarahkan kamera, tidak mengubah setting); tulis dan test kamera khusus `admin`. PTZ: tiap `move` diberi timeout 1 s di kamera dan UI mengulangnya selama tombol ditahan, jadi tab yang tertutup tidak membuat kamera terus berputar. URL kamera di response selalu dimasking (`rtsp://***@host/...`). Error berbentuk `{"error": "..."}`.

### Keamanan

- Semua endpoint, termasuk proxy live, wajib sesi valid.
- **go2rtc dikunci.** API hanya listen di `127.0.0.1` (atau unix socket bila tersedia). Server RTSP bawaannya (`:8554`) dimatikan, sehingga yang terbuka hanya port media WebRTC. Ini krusial karena API go2rtc bisa membuat source `exec:` yang menjalankan perintah arbitrer.
- **Proxy `/live/*` memakai whitelist.** Yang diteruskan hanya `GET /api/ws` (MSE/WebRTC via WebSocket), `POST /api/webrtc`, dan `GET /api/frame.jpeg`, dengan query dibangun ulang (hanya `src`, dan hanya nama stream kamera yang aktif). Endpoint lain, termasuk `/api/config` dan `/api/streams`, selalu 403. Origin diperiksa juga untuk GET (upgrade WebSocket), Cookie sesi Jaga tidak diteruskan, dan header CORS go2rtc dibuang.
- **Config go2rtc tanpa kredensial.** Nama stream `cam<ID>` dan `cam<ID>_sub`. Nilai stream di `data_dir/go2rtc.yaml` (mode 0600) hanya `${JAGA_CAM_n}`, URL aslinya diberikan lewat environment proses go2rtc, sehingga URL tidak pernah ditulis ke disk dan tidak bisa dibaca sebagai prefix source go2rtc (`exec:`, `ffmpeg:`). Environment go2rtc hanya `PATH`, `HOME`, `TZ`, dan `JAGA_CAM_n` (tanpa `JAGA_SECRET_KEY`). `go2rtc.api` di `jaga.yml` wajib loopback.
- **Validasi URL kamera.**
  - Skema hanya `rtsp`/`rtsps`, di-parse dengan `net/url`.
  - Tolak spasi, newline, `#`, dan URL yang diawali `-`.
  - Config go2rtc ditulis lewat YAML marshaller, sehingga URL tidak pernah bisa menjadi source `exec:`/`ffmpeg:`.
- Kredensial kamera tidak pernah dikirim ke frontend. API mengembalikan URL yang sudah dimasking, dan logger menyamarkan `user:pass@`.
- **File diakses lewat ID, bukan path.** `/segments/{id}/file` mengambil path dari DB lalu memastikan hasil `filepath.Clean` berada di bawah `data_dir`. Daftar concat untuk export klip dibangun dari DB dengan cara yang sama.
- Rate limit login (5 percobaan per menit per IP, di memori), cookie `HttpOnly; SameSite=Lax`, ditambah `Secure` bila memakai HTTPS. Pesan login gagal sama untuk user tidak dikenal dan password salah, dan waktunya juga sama (hash dummy).
- CSRF: `http.CrossOriginProtection` dari stdlib (Go 1.25+) menolak request non-GET lintas origin lewat header `Sec-Fetch-Site`/`Origin`, tanpa token. Ditambah `SameSite=Lax` sebagai lapisan kedua.
- Password di-hash dengan argon2id (`golang.org/x/crypto/argon2`, 64 MiB, 3 pass, 4 thread, format PHC). Token sesi 130 bit acak; yang disimpan hanya SHA-256-nya. Reset password mengakhiri semua sesi user itu.
- Error internal (SQL, file) dicatat di log dan klien hanya menerima `internal error`.
- Header `Content-Security-Policy` ketat (tanpa inline script, tanpa CDN). Nama kamera dan input lain di-escape oleh Svelte.
- URL webhook hanya boleh diatur admin. Tolak alamat loopback dan link-local secara default (cegah SSRF ke go2rtc/metadata).
- **Tidak ada akun default.** `jaga start` menolak jalan bila belum ada admin atau `secret_key` kosong.
- **Tanpa telemetri.** Cek versi baru hanya aktif kalau pengguna mengaktifkannya (`update_check: false` default).
