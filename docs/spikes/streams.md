# Pengukuran stream kamera uji

Tanggal: 7 November 2026 · Script: [`scripts/dev/probe.sh`](../../scripts/dev/probe.sh) (prototipe `jaga probe`), 10 detik per stream

Kamera uji untuk Fase 0–1 adalah [`scripts/dev/rtsp-sim.sh`](../../scripts/dev/rtsp-sim.sh) (mediamtx + file loop). Belum ada kamera fisik. Tabel ini perlu ditambah begitu ada kamera fisik, terutama satu yang mendukung ONVIF event untuk Fase 4.

| Stream | Codec | Resolusi | FPS | Bitrate terukur | Keyframe | Audio |
|---|---|---|---|---|---|---|
| `cam1/main` | H.264 Main | 1920×1080 | 25 | 1986 kbps | 2,00 s | AAC LC 16 kHz mono, 32 kbps |
| `cam1/sub` | H.264 Main | 640×360 | 25 | 516 kbps | 2,00 s | — |
| `cam2/main` | H.265 Main | 1920×1080 | 20 | 865 kbps | 2,00 s | — |
| `cam2/sub` | H.264 Constrained Baseline | 640×360 | 10 | 260 kbps | 2,00 s | — |

Catatan:

- `cam2/main` hanya 865 kbps dari target 1,5 Mbps karena x265 (mode ABR) tidak menghabiskan bitrate target pada konten `testsrc` yang sederhana. Untuk uji kapasitas disk/IO di Fase 5, pakai kamera fisik atau naikkan bitrate dengan CRF/CBR.
- `cam1/sub` memunculkan peringatan decoder `co located POCs unavailable` saat probe. Kemungkinan besar penyebabnya probe yang mulai di tengah GOP. Peringatan ini tidak berbahaya, tetapi `jaga probe` sebaiknya menyaring noise seperti ini dari output.
- Kebutuhan disk dari angka ini (main saja, rekaman 24 jam): cam1 ≈ 21 GB/hari, cam2 ≈ 9 GB/hari.
