# Threat model & review keamanan v0.1

Review mandiri sebelum rilis v0.1 (November 2026), dari source di `main` dan instance uji terpisah. Bukan audit pihak ketiga. Risiko tingkat tinggi ada di [06-risks.md](06-risks.md#keamanan), kontrolnya di [03-design.md](03-design.md#keamanan).

## Aset

- **Video**: live dan rekaman rumah/kantor. Aset paling sensitif.
- **Kredensial kamera**: URL RTSP terenkripsi di DB (`secret_key`), dalam bentuk plaintext hanya ada di memori Jaga dan di env go2rtc/FFmpeg.
- **Akun Jaga**: hash argon2id, token sesi (DB hanya menyimpan SHA-256-nya).
- **Host**: Jaga menjalankan FFmpeg dan go2rtc. Eksekusi perintah lewat salah satunya berarti host diambil alih.

## Pelaku dan batas kepercayaan

| Pelaku | Bisa apa | Batas yang harus menahan |
| --- | --- | --- |
| Orang di internet (lewat tunnel/proxy) | Mengirim request HTTP apa pun ke port Jaga | Login (argon2id, rate limit per IP), sesi, CSRF (`CrossOriginProtection` + `Sec-Fetch-Site` di WebSocket) |
| Perangkat lain di LAN | Seperti di atas, plus port WebRTC go2rtc (8555) | Sama; API go2rtc hanya di loopback |
| User `viewer` | Melihat live/rekaman, PTZ, export | Semua tulis dan test kamera khusus `admin` |
| User `admin` | Mengubah kamera (URL masuk ke argv FFmpeg dan config go2rtc) | Validasi URL (skema, `-`, `#`, kontrol), `-protocol_whitelist`, tanpa shell |
| Proses lain di host yang sama | Konek ke port loopback | **API go2rtc kini wajib password** (lihat temuan 1) |
| Kamera (firmware tidak dipercaya) | Mengirim RTSP/ONVIF apa pun | Body ONVIF dibatasi 1 MiB, timeout, tanpa redirect; FFmpeg hanya `-c copy` |

## Temuan dan perbaikan

1. **API go2rtc tanpa autentikasi di loopback (tinggi, diperbaiki).** go2rtc 1.9.15 di `127.0.0.1:1984` menerima `GET /api/streams` (berisi URL kamera beserta password) dan `POST /api/config` dari proses lokal mana pun tanpa login. Dites di instance terpisah: `POST /api/config` menimpa config dengan source `exec:`, yang dijalankan saat go2rtc restart. Akibatnya: siapa pun yang bisa membuka koneksi ke loopback host (container lain dengan host network, service lain, SSRF dari aplikasi lain di host) bisa mengeksekusi perintah sebagai user Jaga dan membaca kredensial kamera. **Fix:** Jaga membuat password acak setiap start, menyalakan `api.username/password` + `local_auth: true` di go2rtc (password lewat env `JAGA_G2R_PASS`, tidak ditulis ke `go2rtc.yaml`), lalu proxy `/live/*` menambahkan Basic Auth. Setelah fix, request tanpa/salah password dijawab 401 untuk REST maupun WebSocket.
2. **IP klien di belakang tunnel (sedang, diperbaiki).** Di belakang Cloudflare Tunnel semua request datang dari `127.0.0.1`, jadi rate limit login (5/menit/IP) berlaku bersama untuk semua orang: satu penyerang bisa mengunci login semua user, dan log tidak mencatat IP asli. Cookie juga tanpa `Secure`. **Fix:** key config `trust_proxy` (`JAGA_TRUST_PROXY=true`). Header `X-Forwarded-For` (IP terakhir, yang ditambahkan proxy) dan `X-Forwarded-Proto` hanya dipercaya bila request datang dari loopback, jadi klien LAN yang konek langsung ke port tidak bisa memalsukannya. Di HTTPS ditambah header HSTS.
3. **Export klip tanpa batas paralel (rendah, diperbaiki).** Tiap export menjalankan satu FFmpeg. User yang login bisa membuka banyak export sekaligus dan menghabiskan CPU/IO yang dibutuhkan recorder. **Fix:** maksimal 2 export bersamaan, sisanya 429.
4. **Daftar concat FFmpeg (informasional, diperkeras).** Path segmen dari DB sudah dicek `filepath.IsLocal` di bawah `recordings/`. Path berisi baris baru kini dilewati juga, supaya tidak bisa menambah directive concat.

## Yang dicek dan aman

- **Auth**: argon2id dengan batas parameter saat verifikasi (hash rusak tidak bisa memakan GB RAM), perbandingan constant-time, dummy hash untuk user tidak dikenal (timing sama), token 130 bit dari `crypto/rand`, DB hanya menyimpan SHA-256 token, ganti password menghapus semua sesi, cookie `HttpOnly` + `SameSite=Lax`.
- **CSRF**: `http.CrossOriginProtection` untuk semua method tulis. WebSocket (GET) dicek `Sec-Fetch-Site`/`Origin` di proxy live.
- **Proxy live**: whitelist 3 endpoint (`/api/ws`, `/api/webrtc`, `/api/frame.jpeg`) per method, query dibangun ulang hanya `src` yang harus nama stream yang dikenal, header `Cookie`/`Origin`/`Authorization` dari browser dibuang, header CORS go2rtc dibuang.
- **File rekaman**: diakses lewat ID, path dari DB dicek `filepath.IsLocal`. Fuzz `FuzzRecordingPath` (±1 juta input) tidak menemukan path keluar `recordings/`.
- **URL kamera**: fuzz `FuzzValidateURL` (±400 ribu input): yang lolos selalu `rtsp://`/`rtsps://`, tanpa `#`, spasi, atau karakter kontrol. FFmpeg memakai `-protocol_whitelist rtsp,rtsps,rtp,udp,tcp,tls`, jadi URL tidak bisa membaca file lokal.
- **go2rtc**: server RTSP dimatikan, URL kamera hanya lewat env, env go2rtc tanpa `JAGA_SECRET_KEY`.
- **Header**: CSP ketat tanpa inline/CDN, `nosniff`, `frame-ancestors 'none'`, `no-referrer`.
- **Error**: error 500 generik ke klien, detail hanya di log. Password kamera di-mask di log dan API.

## Risiko yang diterima (v0.1)

- **Port WebRTC 8555 go2rtc terbuka di semua interface.** Dibutuhkan browser untuk WebRTC. Port ini hanya bicara ICE/DTLS untuk sesi yang sudah dibuat lewat API (yang kini dikunci), tapi tetap permukaan serangan go2rtc. Saat ini UI memakai MSE lewat WebSocket, jadi port ini bisa ditutup di firewall dari internet.
- **Tanpa 2FA dan tanpa lockout per akun.** Rate limit per IP + argon2id. Di belakang tunnel, aktifkan `trust_proxy` dan pertimbangkan Cloudflare Access sebagai lapisan login kedua.
- **Sesi 30 hari, tanpa daftar sesi di UI.** Logout dan ganti password menghapus sesi.
- **`viewer` bisa PTZ dan export.** Disengaja (lihat desain); viewer tetap tidak bisa mengubah kamera.
- **Proxy di host lain.** `trust_proxy` hanya percaya loopback. Proxy di mesin lain butuh daftar `trusted_proxies` (belum ada).

## Untuk deploy di belakang Cloudflare Tunnel

1. Set `JAGA_TRUST_PROXY=true` (cloudflared di host yang sama, konek ke `localhost`).
2. Pertimbangkan Cloudflare Access (login email/OTP) di depan hostname-nya, terutama karena Jaga belum punya 2FA.
3. Port Jaga dan 8555 jangan di-port-forward di router.
