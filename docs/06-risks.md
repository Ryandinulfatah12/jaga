# Risiko & mitigasi

### Teknis

| Risiko | Dampak | Mitigasi |
| --- | --- | --- |
| Stream RTSP kamera murah tidak stabil / hang tanpa error | Rekaman bolong diam-diam | Deteksi stall via progress, timeout RTSP, event `offline`, alert notifikasi |
| Kamera H.265, browser tidak mendukung | Live view gagal | Live pakai sub-stream H.264; go2rtc fallback ke MSE; playback H.265 hanya via download |
| Timestamp kamera melompat / NTP salah | Timeline kacau | Pakai waktu server untuk nama segmen, `-reset_timestamps 1`, simpan durasi dari ffprobe |
| Disk penuh atau HDD rusak | Kehilangan rekaman | Kuota disk, event `disk_full`, cek SMART di halaman sistem (backlog) |
| Write amplification SQLite saat 16 kamera | Lock contention | WAL mode, satu goroutine writer dengan antrean batch insert |
| Frame diff false positive (bayangan, hujan, IR switch) | Notifikasi spam | Mask zona, threshold per kamera, minimum durasi, cooldown notifikasi |
| `segment_list pipe:1` tidak andal (buffering, segmen terakhir hilang saat FFmpeg di-kill) | Index segmen tidak lengkap | Spike di Fase 0; rekonsiliasi file ↔ DB saat startup dan setiap retensi |

### Keamanan

Software CCTV adalah sasaran empuk, karena berisi video rumah atau kantor dan sering terekspos ke internet. Detail kontrolnya ada di [desain keamanan](03-design.md#keamanan).

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
| Dependensi atau model AI berlisensi AGPL/GPL | Seluruh proyek ikut terikat copyleft | Allowlist lisensi; Ultralytics YOLO dilarang ([detail](09-legal.md#object-detection-backlog)) |
| Kewajiban FFmpeg di image Docker terlewat | Pelanggaran lisensi FFmpeg | Build LGPL + tarball source di setiap rilis ([detail](09-legal.md#ffmpeg)) |
| Bentrok nama atau merek "Jaga" | Terpaksa rebrand setelah ada pengguna | Cek PDKI/WIPO kelas 9 & 42 sebelum rilis publik |
| Bus factor satu maintainer | Proyek mati, celah tidak di-patch | Dokumentasi arsitektur (folder ini), CI lengkap, ajak co-maintainer setelah v0.1 |
| Banjir issue "kamera merek X tidak jalan" | Waktu maintainer habis | Template issue wajib menyertakan output `jaga probe`; daftar kamera teruji yang diisi komunitas |

### Lisensi

Ringkasnya: Jaga memakai lisensi Apache-2.0, dan Shinobi hanya dipakai sebagai referensi perilaku (black-box) tanpa membaca atau menyalin kodenya. FFmpeg dipakai sebagai proses terpisah dengan build LGPL beserta source-nya. Aturan lengkapnya ada di [Lisensi & kepatuhan](09-legal.md).
