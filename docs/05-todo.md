# TODO list

Estimasi total 7–10 minggu kerja part-time. Setiap fase menghasilkan sesuatu yang bisa dipakai.

### Fase 0 — Riset & setup (±1 minggu)

- [x] Amati perilaku Shinobi CE secara black-box: jalankan, catat argumen FFmpeg (`ps -ef`) dan lifecycle monitor. **Jangan membuka source code-nya** ([aturan](09-legal.md#hubungan-dengan-shinobi), [hasil](spikes/shinobi-blackbox.md)); lifecycle saat kamera dicabut belum diamati
- [x] Siapkan 2 kamera uji (1 mendukung ONVIF event) atau RTSP simulator (`mediamtx` + file loop): [`scripts/dev/rtsp-sim.sh`](../scripts/dev/rtsp-sim.sh); kamera ONVIF fisik masih dibutuhkan untuk Fase 4
- [x] Ukur bitrate & codec tiap kamera via `ffprobe`, catat main vs sub stream ([hasil](spikes/streams.md))
- [x] **Spike**: mediamtx + FFmpeg `-f segment -segment_list pipe:1` + kill/stall, untuk membuktikan index segmen andal ([hasil](spikes/segment-index.md))
- [x] Init repo Go, struktur folder, Makefile, lint (`golangci-lint`), CI build
- [x] CI kepatuhan: `go-licenses` dengan allowlist, license checker `web/`, cek DCO, header SPDX, `govulncheck`, Dependabot
- [x] Aktifkan GitHub private vulnerability reporting dan proteksi branch `main` (ruleset: tanpa hapus/force push, wajib PR, check `Build & test`, `Lint`, `Compliance`, `DCO`)
- [x] Pilih driver SQLite (`modernc.org/sqlite`) + tool migrasi
- [x] Tetapkan identitas proyek: repo `github.com/ryandinulfatah12/jaga` (akun pribadi, **tanpa org**), image `ghcr.io/ryandinulfatah12/jaga`, module path Go `github.com/ryandinulfatah12/jaga`. Disclaimer di README bagian Maintainer ([checklist branding](07-branding.md#checklist-sebelum-publikasi))
- [x] Lisensi Apache-2.0, `LICENSE`, `NOTICE`, `CONTRIBUTING.md` (DCO), `SECURITY.md`, `CODE_OF_CONDUCT.md`, `THIRD_PARTY_NOTICES.md`
- [x] Isi kontak di `CODE_OF_CONDUCT.md`
- [x] Cek merek "Jaga" di PDKI & WIPO (kelas 9, 42): aman di PDKI dan WIPO (dicek 7 Nov 2026, tidak ada "Jaga" di kelas 9/42 untuk software/CCTV)
- [x] Pasang aset brand: README dengan wordmark, social preview repo

### Fase 1 — Fondasi recording (±2 minggu)

- [x] Kerangka CLI: `jaga start`, `jaga version`, `jaga migrate`, flag global, load `jaga.yml` + env `JAGA_*` (PR #2; parsing pakai package `flag`, belum cobra)
- [x] Migrasi awal: `users`, `cameras`, `segments`, `events`, `settings` (PR #3, plus `sessions`, `notifiers`)
- [x] Service CRUD kamera + enkripsi URL (PR #3, AES-256-GCM, key HKDF dari `secret_key`)
- [x] Endpoint `POST /cameras/{id}/test` via ffprobe (logika yang sama dipakai `jaga probe`) (`jaga probe` di PR #5, endpoint bersama router Fase 2)
- [x] Validasi URL kamera (skema `rtsp`/`rtsps` saja) + unit test untuk input jahat (`file:`, `exec:`, `-i`, newline) (PR #3, plus fuzz test)
- [x] Builder argumen FFmpeg recording (copy, segment, fragmented MP4) (PR #4)
- [x] Supervisor: spawn, baca segment list dari stdout, insert `segments` (PR #4)
- [x] Deteksi stall via `-progress` + backoff eksponensial (event `offline`/online)
- [x] Graceful shutdown (SIGINT ke semua FFmpeg, tunggu segmen tertutup)
- [x] Job retensi berbasis hari + kuota disk (event `disk_full`)
- [x] Rekonsiliasi saat startup: file di disk tanpa baris DB (dan sebaliknya)
- [x] Uji: cabut kabel kamera 5 menit → recording lanjut otomatis tanpa intervensi (7 Nov 2026, simulasi: network container `jaga` diputus 5 menit dengan kamera mediamtx; backoff 2 s → 60 s, event `offline` 298 s, rekaman lanjut 15 s setelah kabel dipasang. Ulangi dengan kamera fisik sebelum Fase 5)

### Fase 2 — API, auth & live view (±1–2 minggu)

- [x] Router `net/http` (stdlib, tanpa chi), middleware sesi, login/logout, rate limit, CSRF via `http.CrossOriginProtection` (stdlib), plus CRUD `/cameras`
- [x] `jaga init` (config + admin pertama) dan `jaga user add|passwd`
- [x] `jaga doctor`: cek ffmpeg, go2rtc, izin `data_dir`, disk, port (plus config, `secret_key`, admin)
- [x] Integrasi go2rtc: generate config lewat YAML marshaller, API hanya `127.0.0.1`, RTSP server go2rtc mati, reload saat berubah (URL kamera lewat env `JAGA_CAM_n`, tidak ditulis ke file)
- [x] Reverse proxy `/live/*` dengan cek sesi + whitelist path & nama stream (test: `/live/api/config` harus 403)
- [x] Snapshot terbaru per kamera (ambil dari go2rtc `frame.jpeg`, sub-stream bila ada)
- [x] Status kamera realtime lewat SSE `GET /api/v1/events/stream` (stdlib, tanpa dependency WebSocket), plus reload recorder tanpa restart saat kamera berubah

### Fase 3 — Frontend (±2 minggu)

- [x] Setup Tailwind + token brand ([palet](07-branding.md#palet-warna)), font Inter & JetBrains Mono di-embed (Svelte 5 + Vite, `web/`)
- [x] Halaman login & layout dasar (dark default, toggle light)
- [x] Grid live 1/4/9/16 dengan sub-stream, klik untuk fullscreen main-stream (MSE lewat WebSocket go2rtc, status realtime dari SSE)
- [x] Form tambah/edit kamera + tombol test koneksi
- [ ] Timeline playback: bar per jam, highlight segmen motion, seek via Range request
- [ ] Export klip (pilih rentang → download)
- [ ] Halaman sistem: CPU, RAM, disk, status tiap kamera
- [ ] Halaman About: versi + daftar lisensi open source (embed `THIRD_PARTY_LICENSES`)
- [x] Header CSP ketat, font di-embed (tanpa CDN)
- [x] Embed hasil build ke binary Go (`make web build`, tag `webui`; tanpa build, binary menampilkan halaman placeholder)

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
- [ ] (Opsional) domain proyek untuk docs site, mis. `getjaga.dev` (masih kosong per 7 Nov 2026; `jaga.dev` sudah dipakai)
