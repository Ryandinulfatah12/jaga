# Observasi black-box Shinobi CE

Tanggal: 7 November 2026 · Instance: Shinobi CE (image `shinobi-systems/shinobi:dev`, FFmpeg 4.3.9) di homeserver, 1 kamera IP 720p 15 fps (main + sub). Container berjalan 10 hari tanpa restart.

Metode sesuai [aturan](../09-legal.md#hubungan-dengan-shinobi): hanya `ps` (argumen proses), isi folder rekaman, `ffprobe` file hasil, dan log container. **Source code, template, dan skema DB tidak dibuka.** Kredensial dan ID internal di bawah disamarkan.

## Proses

- Satu proses induk Node per stream kamera, yang menjalankan satu proses `ffmpeg`. Argumen FFmpeg ditulis ke file `cmd_*.txt` di `/dev/shm` lalu dibaca proses induk.
- Proses FFmpeg kedua stream berumur ±3,1 hari, sedangkan container 10 hari. Berarti monitor sempat di-restart sekali (4 Nov, sama dengan waktu file `cmd_*.txt` ditulis ulang), tapi penyebabnya tidak tercatat.
- Log container 10 hari hanya berisi notifikasi update PM2. Reconnect atau restart monitor tidak masuk log container, jadi diagnosis gangguan kamera dari luar sulit. Jaga harus mencatat setiap restart beserta alasannya.

## Argumen FFmpeg yang teramati

Stream sub (live saja):

```
ffmpeg -progress pipe:5 -use_wallclock_as_timestamps 1 \
  -analyzeduration 1000000 -probesize 1000000 -fflags +igndts \
  -rtsp_transport tcp -loglevel warning -i rtsp://<cam>/stream1 \
  -strict -2 -fflags +genpts+igndts -muxdelay 0 -muxpreload 0 \
  -c:a aac -c:v copy -preset ultrafast \
  -f hls -hls_time 2 -hls_list_size 3 -start_number 0 -hls_allow_cache 0 \
  -hls_flags +delete_segments+omit_endlist+discont_start /dev/shm/.../s.m3u8
```

Stream main: argumen yang sama (live HLS), ditambah output kedua untuk rekaman dalam **satu proses**:

```
  -acodec aac -vcodec copy -strict -2 -segment_format_options movflags=faststart \
  -fflags +genpts+igndts -muxdelay 0 -muxpreload 0 \
  -f segment -segment_atclocktime 1 -reset_timestamps 1 -strftime 1 \
  -segment_list pipe:8 -segment_time 900 videos/<group>/<monitor>/%Y-%m-%dT%H-%M-%S.mp4
```

## Hasil rekaman

- Segmen 15 menit, sejajar jam dinding (`-segment_atclocktime 1`): `…T09-00-01`, `…T09-15-00`. Durasi terukur 898,5–900,6 s.
- File datar dalam satu folder per kamera (170 file, 7,9 GB untuk ±2 hari), tanpa subfolder tanggal.
- Ada folder `<monitor>_timelapse/<YYYY-MM-DD>/` berisi JPEG tiap 15 menit.
- Live view lewat HLS di `/dev/shm` (tmpfs), 3 segmen × 2 s, file lama dihapus FFmpeg.

## Pelajaran untuk Jaga

| Observasi | Dampak untuk Jaga |
|---|---|
| Video `-c:v copy`, tapi audio **di-transcode** ke AAC | Jaga juga copy audio (atau buang audio). Transcode audio murah, tapi tetap proses decode yang bisa dihindari. Kalau codec audio kamera tidak didukung MP4 (mis. G.711), baru pertimbangkan AAC opsional |
| `-preset ultrafast` diberikan walau video copy | Argumen tidak berguna. Builder Jaga hanya memberi argumen yang berlaku |
| Live dan rekaman satu proses FFmpeg | Satu koneksi RTSP ke kamera (baik untuk kamera murah dengan limit koneksi), tapi kalau live bermasalah, rekaman ikut mati. Jaga memisahkan: go2rtc untuk live, FFmpeg untuk rekaman |
| `-use_wallclock_as_timestamps 1` + `+genpts+igndts` | Cara lain menghadapi timestamp kamera yang melompat. Jaga memakai waktu server saat segmen ditutup ([spike](segment-index.md)), jadi tidak perlu menimpa timestamp stream |
| `-segment_atclocktime 1` | Segmen rapi sejajar jam, memudahkan timeline. **Layak diadopsi** di Jaga, cek dulu interaksinya dengan `end_ts` dari spike |
| File datar tanpa folder tanggal | Menghindari masalah folder tengah malam dari spike, tapi listing folder membesar (±100 file/hari/kamera). Jaga tetap pakai folder tanggal yang dibuat supervisor |
| `movflags=faststart`, bukan fragmented MP4 | File yang terpotong saat crash tidak bisa diputar. Jaga tetap fragmented MP4 |
| `-segment_list pipe:8` | Pendekatan "list ke pipe" sama dengan Jaga, mengonfirmasi pilihan desain |
| `-progress pipe:5` | Progress sebagai sinyal hidup, sama dengan watchdog di desain Jaga |
| Tidak ada `-timeout` RTSP | Bergantung pada watchdog progress untuk kamera macet. Jaga memakai keduanya ([spike skenario H](segment-index.md)) |
| Tidak ada `-protocol_whitelist` | Jaga tetap membatasi protokol (keamanan URL kamera) |

## Belum diamati

- Lifecycle saat kamera dicabut atau mati (perlu kamera yang boleh dicabut sementara).
- Mode rekaman motion/event dan perilaku retensi saat disk penuh.
