# Spike: `-segment_list pipe:1` sebagai index segmen

Tanggal: 7 November 2026 · Script: [`scripts/dev/spike-segment.sh`](../../scripts/dev/spike-segment.sh) · Risiko terkait: [06-risks.md](../06-risks.md)

## Pertanyaan

Apakah baris yang ditulis FFmpeg ke stdout lewat `-f segment -segment_list pipe:1 -segment_list_type csv` cukup andal untuk mengisi tabel `segments`, termasuk saat FFmpeg di-kill, kamera macet, atau disk/folder bermasalah?

## Setup

- FFmpeg 9.0.2, mediamtx v1.21.3, macOS 26.6 arm64.
- Kamera dari [`scripts/dev/rtsp-sim.sh`](../../scripts/dev/rtsp-sim.sh): cam1 H.264, cam2 H.265 (main) dan H.264 baseline 10 fps (sub), GOP 2 detik.
- Perintah recorder sama dengan [03-design.md](../03-design.md#perintah-ffmpeg-recording), tetapi `-segment_time 4` agar cepat.
- Setiap file dicek dengan `ffprobe -count_packets`. Skenario A–G dijalankan tiga kali, H dua kali, I sekali. Angka di bawah berasal dari run terakhir, dan perilakunya sama di semua run kecuali disebut.

## Hasil

| # | Skenario | Exit code | Yang terjadi |
|---|---|---|---|
| A | SIGKILL di tengah segmen | (killed) | Semua segmen yang sudah tertutup ada di list. Segmen yang sedang ditulis **tidak** ada di list. Isinya bisa terbaca sampai fragmen terakhir (`partial file`, 28 paket ≈ 2 s), tetapi di satu run file-nya rusak total (`moov atom not found`) |
| B | SIGINT | 255 | FFmpeg keluar dalam ±1 s, segmen terakhir ditutup rapi **dan** ditulis ke list |
| C | Kamera H.265 | 255 (SIGINT) | Sama seperti B. Jumlah paket per segmen pas 80 = 4 s @ 20 fps |
| D | Folder output belum ada | 254 | FFmpeg tidak membuat folder: `Failed to open segment`, keluar dalam 2 s |
| E | Folder strftime berganti saat merekam (`%H%M/%S.mp4`) | 254 | Rekaman berjalan normal sampai menit berganti, lalu mati karena folder menit baru tidak ada. List hanya berisi **nama file tanpa folder** (`15.mp4`, bukan `0859/15.mp4`) |
| F | Kamera tidak bisa dihubungi | 195 | Gagal dalam 1 s: `Connection refused` |
| G | Path RTSP tidak dikenal | 8 | Gagal dalam 1 s: `404 Not Found` |
| H | Kamera macet (publisher di-SIGSTOP, TCP tetap terbuka) | **0** | `-timeout 5000000` bekerja: FFmpeg keluar sendiri ±10 s setelah stream berhenti, segmen terakhir ditutup dan masuk list |
| I | Waktu tiba baris list vs nama file | 255 (SIGINT) | Lihat tabel berikutnya |

Skenario I (cam2/sub 10 fps, recorder start 09:00:26.20):

| Baris tiba | File | Durasi (ffprobe) | Tiba − durasi |
|---|---|---|---|
| 09:00:31.37 | `090030.mp4` | 4.00 s | 09:00:27 |
| 09:00:35.37 | `090031.mp4` | 4.00 s | 09:00:31 |
| 09:00:39.37 | `090035.mp4` | 4.00 s | 09:00:35 |
| 09:00:40.27 | `090039.mp4` | 0.90 s | 09:00:39 |

## Temuan

1. **List andal sebagai sinyal "segmen sudah ditutup".** Di semua skenario, setiap segmen yang tertutup muncul di list, termasuk saat SIGINT dan saat FFmpeg keluar sendiri karena stall. Tidak ada baris untuk file yang tidak ada. Baris dikirim tanpa delay buffering yang terlihat: jaraknya tepat 4,00 s.
2. **Segmen yang sedang ditulis saat SIGKILL hilang dari list.** Isi file kadang masih bisa diputar, kadang tidak. Ini harus ditangani rekonsiliasi, bukan oleh list.
3. **Nama file strftime tidak akurat untuk segmen pertama.** Nama diambil saat file dibuka, yaitu setelah FFmpeg selesai probing input. Di skenario I, `090030.mp4` sebenarnya berisi video mulai sekitar 09:00:27, jadi selisihnya ±3 s. Segmen berikutnya akurat. Sebaliknya, `waktu tiba baris − durasi` cocok untuk semua segmen.
4. **Kolom start/end di CSV bukan durasi yang bisa dipakai.** Untuk segmen pertama, `end − start` lebih panjang 0,3–1,8 s daripada isi file karena offset timestamp awal stream. Untuk H.265 dengan B-frame, ada celah 0,2 s antar baris padahal jumlah paketnya kontinu.
5. **FFmpeg tidak pernah membuat folder.** Dengan pola desain saat ini (`%Y-%m-%d/%H%M%S.mp4`), rekaman **akan mati setiap tengah malam**, dan entri list `000003.mp4` tidak bisa dibedakan dari file jam yang sama di hari lain.
6. **Exit code tidak bisa dipakai untuk klasifikasi.** SIGINT = 255, folder hilang = 254, connection refused = 195, 404 = 8, dan stall justru = 0. Penyebab harus dibaca dari stderr, dan keputusan restart tidak boleh bergantung pada exit code.
7. Loop file di simulator (`-stream_loop`) membuat diskontinuitas timestamp yang memotong satu segmen jadi 2 s. Itu artefak simulator, tetapi kamera murah yang reboot atau melompat timestamp akan menghasilkan efek serupa: panjang segmen tidak boleh diasumsikan sama dengan `segment_time`.

## Keputusan untuk Fase 1

- **Path segmen:** `recordings/<camera_id>/<YYYY-MM-DD>/<YYYYMMDD-HHMMSS>.mp4` (pola FFmpeg `%Y-%m-%d/%Y%m%d-%H%M%S.mp4`). Nama file sendiri sudah unik, jadi supervisor bisa menurunkan folder dari nama yang dikirim list. FFmpeg dijalankan dengan `TZ=UTC` supaya nama tidak bentrok saat perubahan zona waktu. UI yang mengonversi ke waktu lokal.
- **Folder tanggal dibuat supervisor**, bukan FFmpeg: hari ini dan besok, saat start dan lewat timer tiap jam. Kalau FFmpeg tetap mati dengan `Failed to open segment` (misalnya jam sistem melompat), supervisor membuat folder dan restart lewat backoff biasa.
- **Waktu segmen:** `end_ts` = waktu baris list diterima, `start_ts` = `end_ts − durasi`. Durasi diambil dari `ffprobe` file lokal yang baru ditutup (murah, sekali per 10 menit per kamera). Nama file hanya identitas, bukan sumber waktu.
- **Restart:** setiap exit yang tidak diminta supervisor (termasuk exit 0) masuk `backoff`. Exit setelah supervisor mengirim SIGINT dianggap normal apa pun kodenya. Pesan stderr terakhir disimpan sebagai alasan (`Connection refused`, `404`, `timed out`, `Failed to open segment`).
- **Stall:** `-timeout 5000000` sudah menangkap kamera yang diam dengan TCP tetap terbuka (±10 s). Watchdog `-progress` 30 s tetap dipakai sebagai cadangan untuk kasus yang belum diuji (data masih mengalir tetapi frame tidak bertambah).
- **Rekonsiliasi saat startup dan setiap job retensi:** file `.mp4` tanpa baris DB di-`ffprobe`. File yang bisa dibaca di-insert dengan `end_ts` = mtime dan `start_ts` = mtime − durasi. File yang tidak bisa dibaca (mis. `moov atom not found`) dihapus. Baris DB yang file-nya hilang dihapus.

Perubahan ini sudah dimasukkan ke [03-design.md](../03-design.md#perintah-ffmpeg-recording).

## Belum diuji

- Kamera fisik (hanya simulator mediamtx).
- Disk penuh saat merekam.
- Kamera yang terus mengirim paket tetapi timestamp-nya beku.
- Linux/Docker. Spike ini dijalankan di macOS; ulangi di target deployment (debian-slim) sebelum Fase 5.
