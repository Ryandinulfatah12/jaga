# TODO list

Estimasi total 7–10 minggu kerja part-time. Setiap fase menghasilkan sesuatu yang bisa dipakai.

### Fase 0 — Riset & setup (±1 minggu)

- [ ] Amati perilaku Shinobi CE secara black-box: jalankan, catat argumen FFmpeg (`ps -ef`) dan lifecycle monitor. **Jangan membuka source code-nya** ([aturan](09-legal.md#hubungan-dengan-shinobi))
- [ ] Siapkan 2 kamera uji (1 mendukung ONVIF event) atau RTSP simulator (`mediamtx` + file loop)
- [ ] Ukur bitrate & codec tiap kamera via `ffprobe`, catat main vs sub stream
- [ ] **Spike**: mediamtx + FFmpeg `-f segment -segment_list pipe:1` + kill/stall, untuk membuktikan index segmen andal
- [ ] Init repo Go, struktur folder, Makefile, lint (`golangci-lint`), CI build
- [ ] CI kepatuhan: `go-licenses` dengan allowlist, license checker `web/`, cek DCO, header SPDX, `govulncheck`, Dependabot
- [ ] Aktifkan GitHub private vulnerability reporting dan proteksi branch `main`
- [ ] Pilih driver SQLite (`modernc.org/sqlite`) + tool migrasi
- [ ] Amankan nama `jaga`: org/repo GitHub, GHCR, module path Go, domain (lihat [checklist branding](07-branding.md#checklist-sebelum-publikasi))
- [x] Lisensi Apache-2.0, `LICENSE`, `NOTICE`, `CONTRIBUTING.md` (DCO), `SECURITY.md`, `CODE_OF_CONDUCT.md`, `THIRD_PARTY_NOTICES.md`
- [ ] Isi kontak di `CODE_OF_CONDUCT.md` dan `<org>` di README/compose
- [ ] Cek merek "Jaga" di PDKI & WIPO (kelas 9, 42)
- [ ] Pasang aset brand: README dengan wordmark, social preview, avatar org

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

- [ ] Setup Tailwind + token brand ([palet](07-branding.md#palet-warna)), font Inter & JetBrains Mono di-embed
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
- [ ] Rilis v0.1 via GoReleaser: binary amd64 + arm64, image GHCR, checksum, SBOM, tanda tangan (cosign/attestation), tarball source FFmpeg ([checklist rilis](09-legal.md#checklist-setiap-rilis))

### Backlog (setelah v0.1)

- [ ] Plugin object detection (ONNX, model berlisensi permisif seperti YOLOX/RT-DETR; **bukan Ultralytics YOLO, karena AGPL**) yang hanya jalan saat ada motion
- [ ] Mode record `motion-only` dengan pre-buffer
- [ ] Kontrol PTZ via ONVIF
- [ ] PWA + push notification di HP
- [ ] Sinkron arsip event ke S3/MinIO
- [ ] i18n UI (`en`, `id`)
- [ ] Privacy mask per kamera, retensi maksimum, audit log akses rekaman
- [ ] Homebrew tap & paket `.deb`
