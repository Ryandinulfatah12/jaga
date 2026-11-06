# Lisensi & kepatuhan open source

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
- Skema DB Jaga dirancang sendiri ([desain](03-design.md#skema-database-sqlite-mode-wal)) dan tidak meniru nama tabel atau kolom Shinobi.
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
