# Arsitektur sistem

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
