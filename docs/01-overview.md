# Ringkasan & tujuan

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

- Transcoding, rekaman audio dua arah (PTZ dasar via ONVIF sudah ada sejak Fase 3)
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
