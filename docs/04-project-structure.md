# Struktur project & deployment

Satu repo Go, frontend di-build lalu di-embed (`embed.FS`) sehingga hasil akhirnya satu binary.

```
jaga/
  cmd/jaga/main.go           # entrypoint: subcommand cobra, wiring, graceful shutdown
  internal/
    cli/                     # start, init, doctor, probe, user, migrate, backup
    config/                  # load jaga.yml + override env JAGA_*
    db/                      # koneksi SQLite, migrasi (goose/embed SQL), query
    camera/                  # model & service kamera
    supervisor/              # manager proses FFmpeg + state machine
    recorder/                # builder argumen FFmpeg, parser segment list
    motion/                  # onvif.go, framediff.go, debounce
    retention/               # job cleanup & kuota disk
    live/                    # pengelola go2rtc (config, proses, proxy)
    notify/                  # telegram.go, webhook.go
    api/                     # router (chi), handler, middleware auth
    ws/                      # hub WebSocket
    ui/term/                 # palet & simbol output CLI (lipgloss)
    licenses/                # embed THIRD_PARTY_LICENSES untuk About & `jaga version --licenses`
  web/                       # frontend Svelte + Vite (tokens dari docs/brand/tokens.css)
  migrations/
  scripts/fetch-ffmpeg.sh    # unduh FFmpeg LGPL ter-pin + source tarball untuk rilis
  LICENSE  NOTICE  THIRD_PARTY_NOTICES.md  SECURITY.md  CONTRIBUTING.md  CODE_OF_CONDUCT.md
  Dockerfile
  docker-compose.yml
  jaga.example.yml
```

### Konfigurasi (`jaga.yml` + override env `JAGA_*`)

Dibuat otomatis oleh `jaga init`. Detail urutan pencarian file ada di [CLI](08-cli.md).

```yaml
listen: ":8080"
data_dir: /data
ffmpeg_path: ffmpeg
go2rtc:
  mode: embedded      # embedded = spawn binary bawaan | external
  api: 127.0.0.1:1984
storage:
  disk_quota_percent: 90
  default_retention_days: 7
secret_key: ${JAGA_SECRET_KEY}   # enkripsi URL kamera
```

### Docker

- Image multi-stage: build Node (frontend) → build Go (`CGO_ENABLED=0`, driver `modernc.org/sqlite`) → runtime `debian:bookworm-slim` + FFmpeg **build LGPL** yang di-pin (BtbN, butuh glibc) + binary go2rtc + `THIRD_PARTY_LICENSES/`. `ENTRYPOINT ["jaga"]`, `CMD ["start"]`. Berjalan sebagai user non-root. Alasan memakai LGPL dijelaskan di [Lisensi & kepatuhan](09-legal.md#ffmpeg).
- `network_mode: host` direkomendasikan agar RTSP/WebRTC tidak terkendala NAT Docker; alternatif: publish 8080 dan port WebRTC 8555 TCP/UDP.
- Volume `/data` di disk terpisah (HDD surveillance), bukan di disk OS.
- Healthcheck: `GET /api/v1/system`.

```yaml
services:
  jaga:
    image: ghcr.io/<org>/jaga:latest
    network_mode: host
    restart: unless-stopped
    volumes:
      - /mnt/cctv:/data
    environment:
      - JAGA_SECRET_KEY=change-me   # generate: openssl rand -hex 32 (jaga start menolak nilai ini)
```
