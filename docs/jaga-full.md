# Jaga — Architecture, Design & TODO

Versi: 6 November 2026

## Ringkasan & tujuan

**Jaga** adalah NVR self-host yang merekam, menampilkan live, dan mendeteksi gerak dari kamera RTSP/ONVIF, berjalan sebagai **satu binary Go + SQLite** dengan FFmpeg sebagai worker. Arsitektur mengikuti pola Shinobi (monitor per kamera, segmen rekaman, event), tetapi ditulis ulang dari nol dan jauh lebih ringan. Namanya diambil dari kata "jaga" (menjaga, berjaga) dan sekaligus menjadi nama CLI-nya: `jaga start`.

**Prinsip utama: tidak ada transcode.** Rekaman dan live view memakai stream asli kamera (`-c copy`). Pekerjaan CPU berat hanya untuk motion detection di sub-stream resolusi rendah.

### Scope MVP

- Manajemen kamera (tambah, edit, nonaktifkan, test koneksi)
- Recording kontinu tersegmentasi (MP4, 10 menit per file) + retensi otomatis berbasis hari dan kuota disk
- Live view grid multi-kamera via WebRTC/MSE (go2rtc)
- Playback dengan timeline per kamera + download klip
- Motion event (ONVIF event atau frame diff) + notifikasi Telegram/webhook
- Login multi-user dengan role admin/viewer

### Non-goals (versi awal)

- Transcoding, PTZ control, rekaman audio dua arah
- Object detection AI (direncanakan sebagai plugin fase akhir)
- Cluster multi-node dan cloud storage

### Target resource

| Metrik | Target |
| --- | --- |
| RAM aplikasi (tanpa FFmpeg) | < 50 MB |
| CPU per kamera, recording copy-mode | 1–3% (1 core x86 modern) |
| CPU per kamera, motion frame diff 320×240 @ 3 fps | < 5% |
| Kapasitas awal | 16 kamera 1080p di mini PC N100 |
| Cold start sampai semua kamera merekam | < 10 detik |

## Arsitektur sistem

Semua logika ada di satu proses Go; FFmpeg dan go2rtc berjalan sebagai child process yang dikelola Supervisor.

![Diagram arsitektur](architecture.svg)

```mermaid
flowchart TB
  CAM["Kamera IP<br/>RTSP main · RTSP sub · ONVIF event"]
  subgraph W[Worker process]
    REC["FFmpeg recorder<br/>main · -c copy"]
    FM["FFmpeg motion<br/>sub · gray 3 fps"]
    G2["go2rtc<br/>WebRTC / MSE"]
  end
  subgraph JG["jaga · Go binary"]
    SUP["Supervisor<br/>spawn · restart · stall"]
    ME["Motion engine<br/>frame diff / ONVIF"]
    RET["Retention<br/>hari + kuota disk"]
    API["API · Auth · WS<br/>REST + proxy live"]
  end
  ST[("Storage /data<br/>MP4 · snapshots · jaga.db")]
  BR["Browser<br/>Svelte SPA"]
  NT["Telegram / Webhook"]
  CAM -- main --> REC
  CAM -- sub --> FM
  CAM -- ONVIF --> ME
  CAM -- main + sub --> G2
  SUP -- spawn --> REC
  SUP -. spawn .-> FM
  SUP -. spawn .-> G2
  REC -- MP4 segmen --> ST
  FM -- frame --> ME
  SUP -- index segmen --> ST
  ME -- event + snapshot --> ST
  ME --> NT
  RET -- hapus lama --> ST
  API <-- proxy --> G2
  API <-- REST · WS --> BR
```

Supervisor juga men-spawn FFmpeg motion dan go2rtc (panahnya tidak digambar agar ringkas). Video kamera hanya di-copy ke disk dan ke browser; satu-satunya decoding terjadi di sub-stream resolusi rendah untuk motion.

### Alur data utama

1. **Recording**: kamera (main) → FFmpeg recorder `-c copy` → file MP4 per segmen → nama file dikirim lewat stdout ke Supervisor → baris `segments` di SQLite.
2. **Live view**: browser → API (cek sesi) → reverse proxy → go2rtc → kamera. WebRTC/MSE, tanpa transcode.
3. **Playback**: browser minta `/segments?from=&to=` → timeline → putar file MP4 via HTTP Range; export klip memakai concat demuxer (copy).
4. **Motion**: ONVIF event atau frame diff sub-stream → debounce → baris `events` + snapshot → notifikasi Telegram/webhook → push WebSocket ke browser.
5. **Retensi**: job periodik menghapus folder tanggal lama dan segmen tertua saat kuota disk terlampaui.

## Desain detail

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

## Struktur project & deployment

Satu repo Go, frontend di-build lalu di-embed (`embed.FS`) sehingga hasil akhirnya satu binary.

```
jaga/
  cmd/jaga/main.go           # entrypoint: tangkap SIGINT/SIGTERM, panggil cli.Run
  internal/
    cli/                     # start, init, doctor, probe, user, migrate, backup
    config/                  # load jaga.yml + override env JAGA_*
    db/                      # koneksi SQLite, migrasi (goose/embed SQL), query
    camera/                  # model & service kamera
    supervisor/              # manager proses FFmpeg + state machine
    recorder/                # builder argumen FFmpeg, parser segment list
    motion/                  # onvif.go, framediff.go, debounce
    retention/               # job cleanup & kuota disk
    live/                    # pengelola go2rtc (config, proses, proxy)
    notify/                  # telegram.go, webhook.go
    api/                     # router (chi), handler, middleware auth
    ws/                      # hub WebSocket
    ui/term/                 # palet & simbol output CLI (lipgloss)
    licenses/                # embed THIRD_PARTY_LICENSES untuk About & `jaga version --licenses`
  web/                       # frontend Svelte + Vite (tokens dari docs/brand/tokens.css)
  migrations/
  scripts/fetch-ffmpeg.sh    # unduh FFmpeg LGPL ter-pin + source tarball untuk rilis
  scripts/dev/               # simulator RTSP, probe, spike (khusus development)
  docs/spikes/               # hasil spike & pengukuran
  LICENSE  NOTICE  THIRD_PARTY_NOTICES.md  SECURITY.md  CONTRIBUTING.md  CODE_OF_CONDUCT.md
  Dockerfile
  docker-compose.yml
  jaga.example.yml
```

### Konfigurasi (`jaga.yml` + override env `JAGA_*`)

Dibuat otomatis oleh `jaga init`. Detail urutan pencarian file ada di [CLI](#cli).

```yaml
listen: ":8080"
data_dir: /data
ffmpeg_path: ffmpeg
go2rtc:
  mode: embedded      # embedded = spawn binary bawaan | external
  api: 127.0.0.1:1984
storage:
  disk_quota_percent: 90
  default_retention_days: 7
secret_key: ${JAGA_SECRET_KEY}   # enkripsi URL kamera
```

### Docker

- Image multi-stage: build Node (frontend) → build Go (`CGO_ENABLED=0`, driver `modernc.org/sqlite`) → runtime `debian:bookworm-slim` + FFmpeg **build LGPL** yang di-pin (BtbN, butuh glibc) + binary go2rtc + `THIRD_PARTY_LICENSES/`. `ENTRYPOINT ["jaga"]`, `CMD ["start"]`. Berjalan sebagai user non-root. Alasan memakai LGPL dijelaskan di [Lisensi & kepatuhan](#ffmpeg).
- `network_mode: host` direkomendasikan agar RTSP/WebRTC tidak terkendala NAT Docker; alternatif: publish 8080 dan port WebRTC 8555 TCP/UDP.
- Volume `/data` di disk terpisah (HDD surveillance), bukan di disk OS.
- Healthcheck: `GET /api/v1/system`.

```yaml
services:
  jaga:
    image: ghcr.io/ryandinulfatah12/jaga:latest
    network_mode: host
    restart: unless-stopped
    volumes:
      - /mnt/cctv:/data
    environment:
      - JAGA_SECRET_KEY=change-me   # generate: openssl rand -hex 32 (jaga start menolak nilai ini)
```

## TODO list

Estimasi total 7–10 minggu kerja part-time. Setiap fase menghasilkan sesuatu yang bisa dipakai.

### Fase 0 — Riset & setup (±1 minggu)

- [ ] Amati perilaku Shinobi CE secara black-box: jalankan, catat argumen FFmpeg (`ps -ef`) dan lifecycle monitor. **Jangan membuka source code-nya** ([aturan](#hubungan-dengan-shinobi))
- [x] Siapkan 2 kamera uji (1 mendukung ONVIF event) atau RTSP simulator (`mediamtx` + file loop): [`scripts/dev/rtsp-sim.sh`](../scripts/dev/rtsp-sim.sh); kamera ONVIF fisik masih dibutuhkan untuk Fase 4
- [x] Ukur bitrate & codec tiap kamera via `ffprobe`, catat main vs sub stream ([hasil](spikes/streams.md))
- [x] **Spike**: mediamtx + FFmpeg `-f segment -segment_list pipe:1` + kill/stall, untuk membuktikan index segmen andal ([hasil](spikes/segment-index.md))
- [x] Init repo Go, struktur folder, Makefile, lint (`golangci-lint`), CI build
- [x] CI kepatuhan: `go-licenses` dengan allowlist, license checker `web/`, cek DCO, header SPDX, `govulncheck`, Dependabot
- [x] Aktifkan GitHub private vulnerability reporting dan proteksi branch `main` (ruleset: tanpa hapus/force push, wajib PR, check `Build & test`, `Lint`, `Compliance`, `DCO`)
- [x] Pilih driver SQLite (`modernc.org/sqlite`) + tool migrasi
- [x] Tetapkan identitas proyek: repo `github.com/ryandinulfatah12/jaga` (akun pribadi, **tanpa org**), image `ghcr.io/ryandinulfatah12/jaga`, module path Go `github.com/ryandinulfatah12/jaga`. Disclaimer di README bagian Maintainer ([checklist branding](#checklist-sebelum-publikasi))
- [x] Lisensi Apache-2.0, `LICENSE`, `NOTICE`, `CONTRIBUTING.md` (DCO), `SECURITY.md`, `CODE_OF_CONDUCT.md`, `THIRD_PARTY_NOTICES.md`
- [x] Isi kontak di `CODE_OF_CONDUCT.md`
- [x] Cek merek "Jaga" di PDKI & WIPO (kelas 9, 42): aman di PDKI dan WIPO (dicek 7 Nov 2026, tidak ada "Jaga" di kelas 9/42 untuk software/CCTV)
- [x] Pasang aset brand: README dengan wordmark, social preview repo

### Fase 1 — Fondasi recording (±2 minggu)

- [ ] Kerangka CLI: `jaga start`, `jaga version`, `jaga migrate`, flag global, load `jaga.yml` + env `JAGA_*`
- [ ] Migrasi awal: `users`, `cameras`, `segments`, `events`, `settings`
- [ ] Service CRUD kamera + enkripsi URL
- [ ] Endpoint `POST /cameras/{id}/test` via ffprobe (logika yang sama dipakai `jaga probe`)
- [ ] Validasi URL kamera (skema `rtsp`/`rtsps` saja) + unit test untuk input jahat (`file:`, `exec:`, `-i`, newline)
- [ ] Builder argumen FFmpeg recording (copy, segment, fragmented MP4)
- [ ] Supervisor: spawn, baca segment list dari stdout, insert `segments`
- [ ] Deteksi stall via `-progress` + backoff eksponensial
- [ ] Graceful shutdown (SIGINT ke semua FFmpeg, tunggu segmen tertutup)
- [ ] Job retensi berbasis hari + kuota disk
- [ ] Rekonsiliasi saat startup: file di disk tanpa baris DB (dan sebaliknya)
- [ ] Uji: cabut kabel kamera 5 menit → recording lanjut otomatis tanpa intervensi

### Fase 2 — API, auth & live view (±1–2 minggu)

- [ ] Router chi, middleware sesi, login/logout, rate limit, CSRF
- [ ] `jaga init` (wizard config + admin pertama) dan `jaga user add|passwd`
- [ ] `jaga doctor`: cek ffmpeg, go2rtc, izin `data_dir`, disk, port
- [ ] Integrasi go2rtc: generate config lewat YAML marshaller, API hanya `127.0.0.1`, RTSP server go2rtc mati, reload saat berubah
- [ ] Reverse proxy `/live/*` dengan cek sesi + whitelist path & nama stream (test: `/live/api/config` harus 403)
- [ ] Snapshot terbaru per kamera (ambil dari go2rtc `frame.jpeg`)
- [ ] Hub WebSocket untuk status kamera realtime

### Fase 3 — Frontend (±2 minggu)

- [ ] Setup Tailwind + token brand ([palet](#palet-warna)), font Inter & JetBrains Mono di-embed
- [ ] Halaman login & layout dasar (dark default, toggle light)
- [ ] Grid live 1/4/9/16 dengan sub-stream, klik untuk fullscreen main-stream
- [ ] Form tambah/edit kamera + tombol test koneksi
- [ ] Timeline playback: bar per jam, highlight segmen motion, seek via Range request
- [ ] Export klip (pilih rentang → download)
- [ ] Halaman sistem: CPU, RAM, disk, status tiap kamera
- [ ] Halaman About: versi + daftar lisensi open source (embed `THIRD_PARTY_LICENSES`)
- [ ] Header CSP ketat, font di-embed (tanpa CDN)
- [ ] Embed hasil build ke binary Go

### Fase 4 — Motion & notifikasi (±1–2 minggu)

- [ ] Mode ONVIF PullPoint (subscribe, renew, parse event)
- [ ] Mode frame diff: proses FFmpeg sub-stream raw gray + algoritma diff di Go
- [ ] Editor zona mask di UI (grid sel yang bisa di-toggle)
- [ ] Debounce, snapshot event, tandai `has_motion` di segmen
- [ ] Notifier Telegram (foto snapshot) & webhook JSON
- [ ] Halaman daftar event dengan filter + loncat ke playback

### Fase 5 — Hardening & rilis (±1 minggu)

- [ ] Benchmark 16 kamera di mini PC: catat CPU, RAM, IO, bandingkan dengan target
- [ ] Uji ketahanan 72 jam (memory leak, jumlah goroutine, file descriptor)
- [ ] Auto-discovery ONVIF (WS-Discovery) di jaringan lokal
- [ ] Backup/restore konfigurasi (`jaga backup`, export JSON)
- [ ] Dockerfile multi-stage (debian-slim, FFmpeg LGPL ter-pin, user non-root) + docker-compose + README instalasi
- [ ] Panduan "akses dari luar rumah": Tailscale/WireGuard atau reverse proxy + HTTPS, dengan peringatan agar tidak melakukan port forward
- [ ] Review keamanan mandiri sebelum rilis: threat model, fuzz parser segment list & URL
- [ ] Rilis v0.1 via GoReleaser: binary amd64 + arm64, image GHCR, checksum, SBOM, tanda tangan (cosign/attestation), tarball source FFmpeg ([checklist rilis](#checklist-setiap-rilis))

### Backlog (setelah v0.1)

- [ ] Plugin object detection (ONNX, model berlisensi permisif seperti YOLOX/RT-DETR; **bukan Ultralytics YOLO, karena AGPL**) yang hanya jalan saat ada motion
- [ ] Mode record `motion-only` dengan pre-buffer
- [ ] Kontrol PTZ via ONVIF
- [ ] PWA + push notification di HP
- [ ] Sinkron arsip event ke S3/MinIO
- [ ] i18n UI (`en`, `id`)
- [ ] Privacy mask per kamera, retensi maksimum, audit log akses rekaman
- [ ] Homebrew tap & paket `.deb`
- [ ] (Opsional) domain proyek untuk docs site, mis. `getjaga.dev` (masih kosong per 7 Nov 2026; `jaga.dev` sudah dipakai)

## Risiko & mitigasi

### Teknis

| Risiko | Dampak | Mitigasi |
| --- | --- | --- |
| Stream RTSP kamera murah tidak stabil / hang tanpa error | Rekaman bolong diam-diam | Deteksi stall via progress, timeout RTSP, event `offline`, alert notifikasi |
| Kamera H.265, browser tidak mendukung | Live view gagal | Live pakai sub-stream H.264; go2rtc fallback ke MSE; playback H.265 hanya via download |
| Timestamp kamera melompat / NTP salah | Timeline kacau | Waktu server saat segmen ditutup sebagai `end_ts`, `-reset_timestamps 1`, durasi dari ffprobe, nama file UTC |
| FFmpeg tidak membuat folder tanggal baru | Rekaman semua kamera mati tiap tengah malam | Supervisor membuat folder hari ini + besok, restart jika `Failed to open segment` ([spike](spikes/segment-index.md)) |
| Disk penuh atau HDD rusak | Kehilangan rekaman | Kuota disk, event `disk_full`, cek SMART di halaman sistem (backlog) |
| Write amplification SQLite saat 16 kamera | Lock contention | WAL mode, satu goroutine writer dengan antrean batch insert |
| Frame diff false positive (bayangan, hujan, IR switch) | Notifikasi spam | Mask zona, threshold per kamera, minimum durasi, cooldown notifikasi |
| `segment_list pipe:1` tidak andal (buffering, segmen terakhir hilang saat FFmpeg di-kill) | Index segmen tidak lengkap | [Spike Fase 0](spikes/segment-index.md): list andal untuk segmen yang tertutup (SIGINT, stall); segmen yang sedang ditulis saat SIGKILL hilang, sehingga rekonsiliasi file ↔ DB tetap wajib saat startup dan setiap retensi |

### Keamanan

Software CCTV adalah sasaran empuk, karena berisi video rumah atau kantor dan sering terekspos ke internet. Detail kontrolnya ada di [desain keamanan](#keamanan).

| Risiko | Dampak | Mitigasi |
| --- | --- | --- |
| UI diekspos langsung ke internet (port forward) | Brute force, eksploitasi 0-day | Dokumentasi menyarankan VPN (Tailscale/WireGuard) atau reverse proxy + HTTPS; rate limit login; banner peringatan di UI bila diakses dari IP publik tanpa HTTPS |
| API go2rtc terjangkau dari jaringan | **RCE**: go2rtc mendukung source `exec:` yang menjalankan perintah apa pun | API go2rtc hanya listen di `127.0.0.1` (atau unix socket); server RTSP go2rtc (`:8554`) dimatikan; proxy `/live/*` memakai whitelist path, bukan meneruskan semua |
| URL kamera disalahgunakan (`file:`, `concat:`, `exec:`, argumen diawali `-`) | Baca file lokal, injeksi argumen FFmpeg/go2rtc | Validasi skema hanya `rtsp://` / `rtsps://`; `-protocol_whitelist`; `exec.Command` tanpa shell; config go2rtc ditulis lewat YAML marshaller, bukan string concat |
| Kredensial kamera bocor | Akses kamera oleh pihak lain | Enkripsi di DB, masking di API dan log, go2rtc hanya localhost |
| Kehilangan `secret_key` | URL kamera terenkripsi tidak bisa dibuka | `jaga init` menampilkan peringatan untuk mem-backup key; `jaga backup` mengingatkan hal yang sama |
| Default lemah (admin/admin, tanpa secret) | Akses tanpa izin | Tidak ada akun default; `jaga start` menolak jalan tanpa admin dan `secret_key` (exit code 2) |
| Supply chain (dependensi disusupi, rilis dipalsukan) | Malware di instalasi pengguna | Versi dependensi di-pin, Dependabot, `govulncheck`, SBOM, artefak ditandatangani, branch `main` diproteksi |
| Laporan celah dibuka sebagai issue publik | Celah dieksploitasi sebelum ada patch | [`SECURITY.md`](../SECURITY.md) dengan GitHub private vulnerability reporting |

### Privasi & hukum

- **Jaga tidak mengirim data apa pun ke luar.** Tidak ada telemetri dan tidak ada analytics. Cek update hanya aktif kalau pengguna menyalakannya. Nyatakan ini di README.
- **Rekaman audio default mati** (opt-in per kamera). Di banyak yurisdiksi, merekam percakapan diatur lebih ketat daripada merekam video.
- **Kewajiban hukum ada di pemasang CCTV.** README dan dokumentasi menyebutkan bahwa pengguna wajib mematuhi aturan setempat. Di Indonesia, UU No. 27/2022 tentang Pelindungan Data Pribadi (Pasal 17) mengatur pemasangan alat pengolah data visual di tempat umum, termasuk kewajiban memasang pemberitahuan. Di Uni Eropa berlaku GDPR.
- Fitur yang mendukung kepatuhan dimasukkan ke backlog: privacy mask (area yang di-blur atau tidak direkam), retensi maksimum, dan audit log akses rekaman.
- Jaga tidak menyertakan pengenalan wajah dan tidak akan menambahkannya sebagai fitur bawaan.

### Proyek open source

| Risiko | Dampak | Mitigasi |
| --- | --- | --- |
| Kode copyleft/proprietary masuk lewat kontribusi | Jaga terpaksa ganti lisensi atau terkena klaim | DCO, aturan provenance di `CONTRIBUTING.md`, cek lisensi di CI |
| Dependensi atau model AI berlisensi AGPL/GPL | Seluruh proyek ikut terikat copyleft | Allowlist lisensi; Ultralytics YOLO dilarang ([detail](#object-detection-backlog)) |
| Kewajiban FFmpeg di image Docker terlewat | Pelanggaran lisensi FFmpeg | Build LGPL + tarball source di setiap rilis ([detail](#ffmpeg)) |
| Bentrok nama atau merek "Jaga" | Terpaksa rebrand setelah ada pengguna | Cek PDKI/WIPO kelas 9 & 42 sebelum rilis publik |
| Bus factor satu maintainer | Proyek mati, celah tidak di-patch | Dokumentasi arsitektur (folder ini), CI lengkap, ajak co-maintainer setelah v0.1 |
| Banjir issue "kamera merek X tidak jalan" | Waktu maintainer habis | Template issue wajib menyertakan output `jaga probe`; daftar kamera teruji yang diisi komunitas |

### Lisensi

Ringkasnya: Jaga memakai lisensi Apache-2.0, dan Shinobi hanya dipakai sebagai referensi perilaku (black-box) tanpa membaca atau menyalin kodenya. FFmpeg dipakai sebagai proses terpisah dengan build LGPL beserta source-nya. Aturan lengkapnya ada di [Lisensi & kepatuhan](#lisensi--kepatuhan-open-source).

## Branding

### Nama

**Jaga** (dibaca *JA-ga*, /ˈdʒa.ga/), kata Indonesia/Melayu untuk "menjaga, berjaga". Pendek, mudah diketik, dan langsung menjelaskan fungsinya. Nama ini juga dipakai untuk binary CLI:

```
jaga start
```

| Konteks | Penulisan |
| --- | --- |
| Kalimat / judul | Jaga |
| Binary, paket, image, repo | `jaga` (huruf kecil semua) |
| Prefix env var | `JAGA_` (contoh `JAGA_SECRET_KEY`) |
| Prefix CSS / token | `--jg-` |

Hindari "JAGA" (bukan singkatan) dan "Jaga NVR" sebagai nama produk. "NVR" cukup muncul di tagline.

### Tagline

- **EN (utama, untuk README & GitHub):** Lightweight self-hosted NVR. One binary, no transcoding.
- **ID:** NVR self-host yang ringan. Satu binary, tanpa transcode.
- **Deskripsi repo GitHub (≤ 120 karakter):** `Lightweight self-hosted NVR for RTSP/ONVIF cameras. One Go binary, SQLite, no transcoding.`
- **Topics GitHub:** `nvr`, `cctv`, `rtsp`, `onvif`, `webrtc`, `ffmpeg`, `self-hosted`, `surveillance`, `home-security`, `golang`

### Logo

Logo Jaga berupa huruf **j** monoline dengan titik berwarna amber. Titik itu dibaca sebagai dua hal sekaligus: lampu indikator REC pada kamera dan lentera petugas ronda malam.

| File | Pakai untuk |
| --- | --- |
| [`brand/jaga-mark.svg`](brand/jaga-mark.svg) | Ikon app/PWA, avatar GHCR/Docker Hub |
| [`brand/favicon.svg`](brand/favicon.svg) | Favicon web UI (salinan mark) |
| [`brand/jaga-wordmark-on-dark.svg`](brand/jaga-wordmark-on-dark.svg) | Header README / UI di latar gelap |
| [`brand/jaga-wordmark-on-light.svg`](brand/jaga-wordmark-on-light.svg) | Header README / UI di latar terang |
| [`brand/social-preview.svg`](brand/social-preview.svg) / [`.png`](brand/social-preview.png) | Social preview repo GitHub (1280×640) |

Wordmark digambar sebagai path (stroke 6, ujung bulat, bowl lingkaran r 9), jadi tidak bergantung pada font yang terpasang.

**Aturan pakai**

- Titik pada huruf j selalu `lentera-500`. Huruf lainnya mengikuti warna teks: `malam-50` di latar gelap, `malam-900` di latar terang.
- Ruang kosong di sekitar logo minimal setinggi titik j.
- Ukuran minimum: mark 16 px, wordmark tinggi 20 px.
- Jangan memutar, memberi bayangan, gradien, atau outline. Jangan mengganti warna titik.

README memakai `<picture>` agar wordmark mengikuti tema GitHub:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/jaga-wordmark-on-dark.svg">
  <img src="docs/brand/jaga-wordmark-on-light.svg" alt="Jaga" height="64">
</picture>
```

### Palet warna

Konsepnya *jaga malam*: latar biru-arang (**Malam**) dengan satu aksen hangat (**Lentera**). Amber dipilih karena kontras tinggi di layar gelap, jelas berbeda dari biru yang umum dipakai proyek NVR lain, dan secara alami berarti "perhatian".

**Malam (netral)**

| Token | Hex | Peran |
| --- | --- | --- |
| `malam-50` | `#F4F6F9` | Teks utama (dark), surface (light) |
| `malam-100` | `#E6E9EF` | |
| `malam-200` | `#CDD3DE` | Border (light) |
| `malam-300` | `#A9B2C3` | |
| `malam-400` | `#8792A8` | Teks sekunder (dark) |
| `malam-500` | `#56627A` | Teks sekunder (light), kamera offline |
| `malam-600` | `#3A4559` | |
| `malam-700` | `#263043` | Border (dark) |
| `malam-800` | `#1A2231` | Elemen raised (dark) |
| `malam-900` | `#111723` | Surface (dark), teks utama (light) |
| `malam-950` | `#0B0F17` | Latar (dark) |

**Lentera (brand)**

| Token | Hex | Peran |
| --- | --- | --- |
| `lentera-50` | `#FFF8E6` | |
| `lentera-100` | `#FEEDC2` | |
| `lentera-200` | `#FDDB85` | |
| `lentera-300` | `#FCC648` | |
| `lentera-400` | `#FBB534` | Hover, teks aksen & focus ring (dark) |
| `lentera-500` | `#F5A524` | **Warna brand.** Tombol utama, titik logo, motion |
| `lentera-600` | `#C27608` | Focus ring (light) |
| `lentera-700` | `#9C5C05` | Teks aksen / link (light) |
| `lentera-800` | `#7A4708` | |
| `lentera-900` | `#5C360B` | |

**Status**

| Token | Hex | Arti |
| --- | --- | --- |
| `rekam-500` / `rekam-600` | `#F2545B` / `#C9262E` | REC aktif, error, hapus (dark / light) |
| `aman-500` / `aman-600` | `#2FCB7A` / `#13814B` | Kamera online, sukses (dark / light) |
| `malam-500` | `#56627A` | Kamera offline / disabled |
| `lentera-500` @ 35% | `rgb(245 165 36 / .35)` | Blok motion di timeline |

Aturan warna:

- **Amber berarti "lihat ini".** Dipakai untuk aksi utama, event motion, dan peringatan. Jangan untuk dekorasi.
- **Merah hanya untuk REC dan error/aksi destruktif.** Titik REC berkedip pelan (1 s) hanya di live view.
- **Tombol utama:** latar `lentera-500`, teks `malam-950`. Jangan pakai teks putih di atas amber karena kontrasnya hanya 2:1.
- Di tema terang, teks amber pakai `lentera-700`, bukan `lentera-500`.
- Status tidak boleh dibedakan dari warna saja. Selalu sertakan ikon atau label (`● REC`, `○ offline`).

Semua token ada di [`brand/tokens.css`](brand/tokens.css). Tema gelap adalah default. Tema terang aktif lewat `data-theme="light"` pada `<html>`.

### Kontras (WCAG 2.1)

Semua pasangan teks yang dipakai lolos AA (≥ 4.5:1):

| Pasangan | Rasio |
| --- | --- |
| `malam-50` di `malam-950` | 17.7:1 |
| `malam-400` di `malam-950` | 6.1:1 |
| `malam-400` di `malam-800` | 5.1:1 |
| `lentera-500` di `malam-950` | 9.4:1 |
| `malam-950` di `lentera-500` (tombol) | 9.4:1 |
| `rekam-500` di `malam-950` | 5.7:1 |
| `aman-500` di `malam-950` | 9.1:1 |
| `malam-900` di putih | 17.9:1 |
| `malam-500` di `malam-50` | 5.7:1 |
| `lentera-700` di `malam-50` | 4.9:1 |
| `rekam-600` di putih | 5.5:1 |
| `aman-600` di `malam-50` | 4.5:1 |

### Tipografi

| Peran | Font | Catatan |
| --- | --- | --- |
| UI & dokumentasi | **Inter** (OFL) | 400 / 500 / 600. Aktifkan `font-feature-settings: "tnum"` untuk angka di tabel status. |
| Timestamp, kode, CLI, nama file | **JetBrains Mono** (OFL) | 400 / 600 |

Font di-bundle ke dalam binary (embed) supaya UI tetap tampil benar di jaringan lokal tanpa internet. Jangan memuatnya dari Google Fonts.

Skala ukuran UI: 12 / 13 / 14 (body) / 16 / 20 / 24 / 32 px. Radius: 6 (input, chip), 10 (kartu), 14 (modal, ikon app).

### Integrasi frontend (Tailwind)

```js
// web/tailwind.config.js
export default {
  theme: {
    extend: {
      colors: {
        malam: { 50: "#F4F6F9", 100: "#E6E9EF", 200: "#CDD3DE", 300: "#A9B2C3", 400: "#8792A8",
                 500: "#56627A", 600: "#3A4559", 700: "#263043", 800: "#1A2231", 900: "#111723", 950: "#0B0F17" },
        lentera: { 50: "#FFF8E6", 100: "#FEEDC2", 200: "#FDDB85", 300: "#FCC648", 400: "#FBB534",
                   500: "#F5A524", 600: "#C27608", 700: "#9C5C05", 800: "#7A4708", 900: "#5C360B" },
        rekam: { 400: "#F87177", 500: "#F2545B", 600: "#C9262E" },
        aman: { 400: "#5BDB98", 500: "#2FCB7A", 600: "#13814B" },
      },
      fontFamily: {
        sans: ["Inter", "-apple-system", "Segoe UI", "sans-serif"],
        mono: ["JetBrains Mono", "SF Mono", "Menlo", "monospace"],
      },
    },
  },
};
```

Komponen sebaiknya memakai variabel semantik (`var(--jg-surface)`, `var(--jg-accent)`) dan bukan warna skala secara langsung, supaya pergantian tema cukup dilakukan di satu tempat.

### Warna di CLI

Output terminal memakai palet yang sama. Implementasinya lewat `charmbracelet/lipgloss` dengan fallback ANSI-256:

| Peran | Truecolor | ANSI-256 | Contoh |
| --- | --- | --- | --- |
| Nama brand, aksen | `#F5A524` | 214 | `jaga v0.1.0` |
| REC / error | `#F2545B` | 203 | `● REC`, `✗ ffmpeg not found` |
| Online / sukses | `#2FCB7A` | 42 | `✓ ready` |
| Teks sekunder | `#8792A8` | 245 | path, durasi, URL |

- Warna dimatikan otomatis jika `NO_COLOR` diset, output bukan TTY, atau flag `--no-color` dipakai.
- Log dalam mode non-TTY / `--log-format json` tidak memakai warna maupun simbol.

Detail perintah dan contoh output ada di [CLI](#cli).

### Suara & gaya tulisan

- **Singkat, tenang, faktual.** Jaga ibarat petugas jaga: melapor seperlunya, tidak panik.
  - Bagus: `garasi offline (3x gagal). Coba lagi dalam 30s.`
  - Hindari: `ERROR!!! Camera garasi has FAILED!`
- Pesan error selalu memuat apa yang terjadi dan langkah berikutnya.
- README, UI, dan pesan CLI ditulis dalam bahasa Inggris (untuk komunitas global). Dokumentasi pengembangan di `docs/` tetap dalam bahasa Indonesia. Terjemahan UI (i18n `id`, `en`) masuk backlog.
- Hindari bahasa yang menakut-nakuti ("protect your family from intruders"). Fokus pada fungsi: rekam, lihat, cari.
- Untuk nama pihak lain, tulis "works with ONVIF cameras", jangan "ONVIF conformant/certified". Lihat [aturan merek](#merek-dan-nama-pihak-lain).

### Lisensi aset brand

Logo dan nama Jaga tidak tercakup lisensi Apache-2.0 (pasal 6). Aturan pemakaiannya ada di [`brand/README.md`](brand/README.md).

### Checklist sebelum publikasi

- [x] Identitas: repo, GHCR, dan module path Go di bawah akun pribadi `ryandinulfatah12`, tanpa org (keputusan 7 Nov 2026). Domain opsional, lihat backlog
- [x] Upload `social-preview.png` di Settings → Social preview
- [x] Lisensi Apache-2.0 + `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `THIRD_PARTY_NOTICES.md` ([detail](#lisensi--kepatuhan-open-source))
- [x] Isi kontak penegakan di `CODE_OF_CONDUCT.md`
- [x] Cek merek di PDKI & WIPO (kelas 9, 42): aman di PDKI dan WIPO (dicek 7 Nov 2026, tidak ada "Jaga" di kelas 9/42 untuk software/CCTV)

## CLI

Semuanya dijalankan lewat satu binary `jaga`. Perintah sehari-hari cukup `jaga start`. Perintah lain hanya untuk setup dan perawatan.

### Perintah

| Perintah | Fungsi |
| --- | --- |
| `jaga start` | Jalankan server: API, UI, supervisor, retensi. Ini perintah default. |
| `jaga init` | Wizard pertama kali: buat `jaga.yml`, generate `secret_key`, buat admin pertama |
| `jaga doctor` | Cek `ffmpeg`/`ffprobe`/go2rtc, izin tulis `data_dir`, sisa disk, port bentrok |
| `jaga probe <rtsp-url>` | Uji URL kamera via ffprobe (codec, resolusi, fps, bitrate) tanpa menyimpannya |
| `jaga camera list` | Daftar kamera beserta status (butuh server berjalan atau baca DB langsung) |
| `jaga user add <nama> [--role admin\|viewer]` | Tambah user, password diminta interaktif |
| `jaga user passwd <nama>` | Reset password (untuk admin yang lupa password) |
| `jaga migrate` | Jalankan migrasi DB tanpa start server |
| `jaga backup [--out file.json]` | Export konfigurasi (kamera, notifier, user tanpa hash). Rekaman tidak ikut. |
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

Pewarnaan mengikuti [palet CLI](#warna-di-cli): `jaga` dan versi amber, `● REC` merah, `✓` hijau, detail teknis abu-abu. Setelah baris `ready`, output beralih ke log biasa:

```
22:41:07 INF camera online     camera=belakang after=24s
22:58:13 WRN motion            camera=garasi score=0.18 snapshot=snapshots/2/481.jpg
23:00:00 INF retention         deleted=36 freed=7.4GB
```

Di mode non-TTY (systemd, Docker), banner tidak ditampilkan dan log langsung ditulis apa adanya. Dengan `--log-format json`, setiap baris berupa satu objek JSON.

### Contoh output `jaga doctor`

```
$ jaga doctor
✓ ffmpeg 7.1          /usr/bin/ffmpeg
✓ go2rtc 1.9.9        embedded
✓ data_dir            /mnt/cctv  writable  412 GB free
✗ port 8555/udp       in use by pid 1834 (go2rtc)
  → stop the other go2rtc or set go2rtc.webrtc_port in jaga.yml
```

### Implementasi

- Parsing perintah: package `flag` dari standard library, satu `FlagSet` per perintah sehingga flag global boleh ditaruh sebelum atau sesudah perintah. Ganti ke `spf13/cobra` bila perintah mulai bertingkat (`jaga user add`, `jaga camera list`) atau butuh shell completion.
- Output: `charmbracelet/lipgloss` untuk warna, `log/slog` dengan handler teks berwarna untuk TTY dan handler JSON untuk non-TTY.
- Lokasi kode: `cmd/jaga/` hanya menangkap sinyal dan memanggil `cli.Run`, `internal/cli/` untuk tiap subcommand, `internal/config/` untuk `jaga.yml` + env, dan `internal/ui/term/` untuk palet dan simbol.
- Belum ada (menyusul): `--no-color` dan banner berwarna (bersama `internal/ui/term`), versi go2rtc di `jaga version` (Fase 2).

## Lisensi & kepatuhan open source

Dokumen ini menjelaskan aturan supaya Jaga aman dirilis, dipakai, dan dikontribusikan. Isinya praktik standar proyek open source, **bukan nasihat hukum**. Sebelum Jaga dipakai secara komersial (misalnya dijual bersama perangkat), minta konsultan HKI meninjau ulang.

### Lisensi Jaga: Apache-2.0

Kode Jaga dirilis di bawah **Apache License 2.0** ([`LICENSE`](../LICENSE), teks resmi dari apache.org). Alasannya:

- **Permisif.** Bisa dipakai komersial, di-fork, atau dibundel ke perangkat, sama seperti MIT.
- **Ada lisensi paten eksplisit dari kontributor** (pasal 3), sesuatu yang tidak dimiliki MIT. Ini relevan karena domain video penuh paten.
- **Tidak memberi hak atas merek** (pasal 6), sehingga nama dan logo Jaga tetap terlindungi walaupun kodenya bebas.
- **Kompatibel dengan semua komponen yang direncanakan**: go2rtc (MIT), FFmpeg (dipanggil sebagai proses terpisah), dan font OFL.

Setiap file sumber diberi header SPDX:

```go
// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors
```

Kalau suatu saat ingin pindah ke MIT, lakukan **sebelum ada kontribusi dari orang lain**. Setelah itu, mengganti lisensi butuh persetujuan setiap kontributor.

### Hubungan dengan Shinobi

Shinobi CE berlisensi GPLv3, sedangkan Shinobi Pro memakai EULA sendiri yang mewajibkan lisensi berbayar untuk instalasi komersial. Karena itu Jaga **tidak boleh mengandung kode, aset, atau teks dari Shinobi**, baik CE maupun Pro. Kalau ada yang tersalin, Jaga bisa dianggap karya turunan GPL, atau lebih buruk lagi, melanggar EULA Pro.

Metode yang dipakai adalah **referensi perilaku (black-box)**, bukan clean-room formal:

- **Boleh:** menjalankan Shinobi, mengamati argumen FFmpeg (`ps -ef`), melihat skema DB dari instance yang berjalan, membaca dokumentasi publiknya, dan mencatat ide fitur. Ide, arsitektur, dan fungsi tidak dilindungi hak cipta.
- **Tidak boleh:** membuka atau membaca source code Shinobi (`.js`, `.sql`, template, CSS), menyalin teks UI atau dokumentasinya, atau memakai logo dan screenshot-nya di materi Jaga.
- Skema DB Jaga dirancang sendiri ([desain](#skema-database-sqlite-mode-wal)) dan tidak meniru nama tabel atau kolom Shinobi.
- Bagi siapa pun yang **sudah pernah** membaca source Shinobi: jangan menulis bagian Jaga yang fungsinya sama, atau minimal nyatakan hal itu di PR supaya bisa di-review lebih ketat.

Aturan yang sama berlaku untuk NVR lain yang berlisensi copyleft: **Moonfire NVR, SentryShot / OS-NVR, dan ZoneMinder**. Kode dari proyek permisif (misalnya Frigate dan Viseron yang MIT) secara hukum boleh dipakai, asal notice hak ciptanya dipertahankan dan dicatat di `THIRD_PARTY_NOTICES.md`. Meski begitu, sebaiknya tetap menulis kode sendiri.

Penyebutan nama Shinobi di dokumentasi (misalnya "terinspirasi Shinobi") diperbolehkan sebagai penggunaan nominatif. Syaratnya: tulis pernyataan tidak berafiliasi, dan jangan memakai logonya.

### Kebijakan lisensi dependensi

| Status | Lisensi |
| --- | --- |
| **Diizinkan** | MIT, BSD-2-Clause, BSD-3-Clause, ISC, Apache-2.0, 0BSD, Zlib, CC0-1.0, Unicode-3.0, MPL-2.0 (sebagai dependensi tanpa modifikasi) |
| **Hanya dengan aturan khusus** | LGPL / GPL: **hanya** sebagai program terpisah yang dipanggil lewat proses (FFmpeg), tidak pernah di-link · OFL-1.1: hanya untuk font · CC-BY-4.0: hanya untuk aset/dokumentasi, dengan atribusi |
| **Dilarang** | GPL / AGPL / LGPL yang di-link ke binary · SSPL · BUSL · Elastic · Commons Clause · lisensi "non-commercial" · JSON License · CC BY-SA / NC untuk kode · kode **tanpa lisensi** (default-nya: semua hak dilindungi) |

Kebijakan ini diterapkan otomatis di CI:

- `go-licenses check ./... --allowed_licenses=MIT,BSD-2-Clause,BSD-3-Clause,ISC,Apache-2.0,MPL-2.0,0BSD,Zlib` untuk modul Go
- `license-checker-rseidelsohn --onlyAllow ...` (atau padanannya) untuk dependensi `web/`, termasuk devDependency yang ikut ke bundle
- Build gagal kalau muncul dependensi dengan lisensi baru atau yang tidak dikenali

### Komponen pihak ketiga yang direncanakan

| Komponen | Lisensi | Cara dipakai | Kewajiban |
| --- | --- | --- | --- |
| go2rtc | MIT | Binary dibundel, child process | Sertakan teks lisensi MIT + copyright |
| FFmpeg | LGPL-2.1+ (atau GPL bila build `--enable-gpl`) | Program terpisah, child process | Lihat [bagian FFmpeg](#ffmpeg) |
| Inter | OFL-1.1 | Font di-embed ke web UI | Sertakan `OFL.txt`; jangan dijual terpisah |
| JetBrains Mono | OFL-1.1 | Font di-embed ke web UI | Sertakan `OFL.txt` |
| modernc.org/sqlite | BSD-3-Clause | Di-link | Sertakan notice |
| go-chi/chi, charmbracelet/lipgloss, pressly/goose | MIT | Di-link | Sertakan notice |
| spf13/cobra | Apache-2.0 | Di-link | Sertakan notice + NOTICE bila ada |
| golang.org/x/crypto | BSD-3-Clause | Di-link (argon2id) | Sertakan notice |
| Svelte, Vite, Tailwind CSS | MIT | Build frontend | Sertakan notice untuk yang ikut ke bundle |
| mediamtx | MIT | Hanya untuk testing, tidak dirilis | Tidak ada |

Tabel ini masih rencana. Sumber kebenarannya adalah output `go-licenses` dan license checker di setiap rilis. Lisensi library ONVIF untuk Go berbeda-beda per proyek, jadi cek dulu sebelum memakainya, atau tulis klien SOAP sendiri. Kebutuhannya hanya beberapa operasi: GetProfiles, GetStreamUri, dan PullPoint.

Teks lisensi semua komponen dikumpulkan saat build ke `THIRD_PARTY_LICENSES/` dan:

- disertakan di arsip rilis dan image Docker (`/usr/share/doc/jaga/`),
- bisa dibuka dari UI lewat halaman **About → Open source licenses**, dan dari CLI lewat `jaga version --licenses`.

### FFmpeg

Jaga sendiri **tidak terikat GPL/LGPL** karena FFmpeg hanya dipanggil sebagai proses terpisah lewat argumen CLI dan pipe, tanpa linking. Ini termasuk *aggregate* menurut FAQ GPL. Tetapi **mendistribusikan binary FFmpeg** di dalam image Docker atau arsip rilis tetap menimbulkan kewajiban:

1. **Pakai build LGPL**, bukan GPL. Jaga hanya butuh demux/mux (`-c copy`), decoder H.264/H.265, scaler, dan encoder MJPEG untuk snapshot. Semuanya bawaan FFmpeg dan tersedia di build LGPL. Tidak ada kebutuhan untuk libx264/libx265 (GPL).
   - Pilihan yang direkomendasikan: build `linux64-lgpl` / `linuxarm64-lgpl` dari BtbN/FFmpeg-Builds pada **branch rilis yang di-pin** (misalnya `n9.0`). Build ini butuh glibc, jadi image runtime memakai `debian:bookworm-slim`, bukan Alpine.
   - Paket `ffmpeg` di Alpine/Debian di-build dengan `--enable-gpl`. Kalau tetap memakainya, kewajiban di bawah berlaku dengan GPL.
2. **Sertakan teks lisensi** FFmpeg dan `ffmpeg -version` (yang memuat konfigurasi build) di `THIRD_PARTY_LICENSES/ffmpeg/`.
3. **Sediakan source yang persis sama dengan binary-nya.** Unggah tarball source FFmpeg versi tersebut, plus skrip build-nya, sebagai asset di setiap GitHub Release. Jangan hanya menautkan upstream, karena tautan bisa hilang.
4. Pengguna boleh mengganti FFmpeg dengan build mereka sendiri lewat `ffmpeg_path`. Ini juga memenuhi semangat LGPL.

Catatan paten: H.264 dan H.265 dilindungi paten (pool MPEG LA / Access Advance). Jaga tidak meng-encode video, dan decode dilakukan oleh FFmpeg. Proyek open source lain yang sejenis mendistribusikan FFmpeg dengan cara yang sama, tetapi pihak yang memakai Jaga secara komersial (misalnya menjual NVR berbasis Jaga) bertanggung jawab memeriksa lisensi paten codec di negaranya.

### Object detection (backlog)

**Jangan memakai Ultralytics YOLO** (YOLOv5/v8/YOLO11 dan seterusnya). Lisensinya AGPL-3.0, dan menurut Ultralytics lisensi itu juga berlaku untuk model hasil training. Memakainya berarti seluruh Jaga harus AGPL, atau pengguna harus membeli Enterprise License.

Hindari juga YOLOv7 dan YOLOv9 (GPL-3.0), serta model yang weights-nya berlisensi "non-commercial".

Kandidat yang lisensinya permisif (cek ulang lisensi kode **dan** weights saat implementasi):

- YOLOX (Apache-2.0)
- RT-DETR / D-FINE (Apache-2.0)

Dataset yang dipakai untuk training weights juga perlu dicek. Contohnya, lisensi anotasi COCO adalah CC BY 4.0, jadi perlu atribusi.

### Merek dan nama pihak lain

| Nama | Boleh | Jangan |
| --- | --- | --- |
| ONVIF | "works with ONVIF cameras", "uses ONVIF events" | "ONVIF conformant/compliant/certified" atau memakai logo ONVIF. Klaim conformance hanya boleh untuk produk anggota yang sudah terdaftar resmi. |
| Shinobi, Frigate, dll. | Perbandingan faktual, "inspired by" | Klaim afiliasi, logo, atau nama mereka di nama paket |
| Telegram | "Telegram notifications" | Logo Telegram sebagai ikon utama fitur, atau kesan sebagai produk resmi |
| Merek kamera (Hikvision, Dahua, dll.) | Daftar "tested cameras" | Logo merek, klaim "certified by" |

### Nama dan logo Jaga

- Hak cipta kode dipegang **The Jaga Authors** (semua kontributor). Kontributor tetap memiliki hak cipta atas kodenya; mereka melisensikannya di bawah Apache-2.0 lewat DCO.
- Nama "Jaga" dan logonya **tidak** ikut dilisensikan oleh Apache-2.0 (pasal 6). Siapa pun boleh memakainya untuk merujuk ke proyek ini, tetapi fork yang mengubah fungsinya secara signifikan sebaiknya memakai nama lain. Kebijakan ini dituliskan di [`docs/brand/README.md`](brand/README.md).
- **Sebelum rilis publik**, cek bentrokan merek di [PDKI](https://pdki-indonesia.dgip.go.id) dan WIPO Global Brand Database untuk kelas 9 (software) dan 42 (SaaS). "Jaga" sudah dipakai sebagai merek di industri lain (misalnya radiator di Eropa). Itu kelas yang berbeda sehingga risikonya rendah, tetapi tetap perlu dicek di kelas software.

### Kontribusi: DCO

Setiap commit wajib memakai `Signed-off-by` ([Developer Certificate of Origin 1.1](https://developercertificate.org)), dibuat dengan `git commit -s`. Dengan sign-off, kontributor menyatakan bahwa mereka berhak mengirim kode tersebut di bawah lisensi proyek. Aturan lengkapnya ada di [`CONTRIBUTING.md`](../CONTRIBUTING.md):

- dilarang menyalin kode dari proyek copyleft atau proprietary,
- kode yang dibantu AI tetap menjadi tanggung jawab kontributor,
- snippet dari Stack Overflow (CC BY-SA) dilarang kecuali benar-benar trivial.

DCO dipilih karena lebih ringan daripada CLA. Kekurangannya: tanpa CLA, lisensi tidak bisa diganti sepihak di masa depan.

### Checklist setiap rilis

- [ ] `go-licenses` dan license checker frontend lolos
- [ ] `THIRD_PARTY_LICENSES/` ter-generate dan ikut ke arsip, image, dan halaman About
- [ ] Tarball source FFmpeg + skrip build diunggah ke GitHub Release
- [ ] SBOM (SPDX/CycloneDX) dibuat oleh GoReleaser/syft dan dilampirkan
- [ ] Artefak rilis ditandatangani (cosign keyless atau GitHub artifact attestation) dan checksum dipublikasikan
- [ ] `govulncheck` dan audit `npm` tanpa temuan tingkat high
- [ ] Changelog tidak memuat klaim "ONVIF conformant" atau klaim afiliasi
