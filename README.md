<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/jaga-wordmark-on-dark.svg">
    <img src="docs/brand/jaga-wordmark-on-light.svg" alt="Jaga" height="72">
  </picture>
</p>

<p align="center"><b>Lightweight self-hosted NVR. One binary, no transcoding.</b></p>

---

Jaga records, streams and watches your RTSP cameras (including ONVIF cameras) from a single Go binary with SQLite. Video is never transcoded: recordings and live view use the camera's own stream (`-c copy`), so a mini PC can handle 16 cameras at 1080p. Only optional recorded audio is converted to AAC, since the G.711 most cameras send cannot go into MP4.

> **Status:** pre-release. It runs and records, but there is no tagged release or published image yet, so you build it yourself (below). See the [roadmap](docs/05-todo.md).

## Features

- Continuous segmented recording (MP4, `-c copy`), retention by days and disk quota, crash-safe (unfinished segments are repaired or dropped at start)
- Record the main or the sub stream per camera, optional audio (AAC)
- Live grid (1/4/9/16) and full-size view in the browser (MSE via go2rtc), snapshot, sound, pan & tilt for ONVIF PTZ cameras
- Timeline playback, export a time range as one MP4
- System page: disk, CPU, memory, per-camera recording stats
- Multi-user login (admin/viewer)

Planned: motion events with Telegram/webhook notifications, two-way audio, release binaries and images.

## Install with Docker

Needs Docker with Compose on Linux (x86-64), and cameras that speak RTSP.

```sh
git clone https://github.com/Ryandinulfatah12/jaga.git && cd jaga

# 1. Secret key: encrypts the camera URLs (and their passwords) in the database.
#    Back up .env: without this key the cameras have to be added again.
printf 'JAGA_SECRET_KEY=%s\n' "$(openssl rand -base64 32)" > .env
chmod 600 .env

# 2. Where recordings go (a big disk), and the web port.
echo 'JAGA_DATA=/mnt/cctv' >> .env
echo 'JAGA_LISTEN=:8080' >> .env

# 3. Build the image (a few minutes) and create the first admin.
docker compose build
docker compose run --rm jaga user add admin      # asks for the password

# 4. Start.
docker compose up -d
```

Open `http://<server-ip>:8080`, sign in, then **Cameras → Add camera**. For each camera:

- **Main URL**: the full-resolution RTSP stream, e.g. `rtsp://user:pass@192.168.1.20:554/stream1`. A `@` in the password must be written as `%40`.
- **Sub URL** (optional, recommended): the small stream, used by the live grid, e.g. `.../stream2`.
- **Record stream**: `main` for full detail, `sub` for much smaller files.
- **ONVIF port** (optional): enables pan & tilt (Tapo: `2020`).
- **Test** checks the URL and shows codec, resolution and bitrate.

The container runs as uid 1000 by default (`JAGA_UID`/`JAGA_GID` in `.env`); that user must be able to write `JAGA_DATA`. It uses the host network, so RTSP to the cameras and WebRTC need no port mapping; open `8080/tcp` in the firewall for the LAN if needed.

Stop any other NVR that still pulls from the same cameras: many cameras (Tapo, for one) allow only a few RTSP sessions, and the extra ones get a black picture.

### Update

```sh
git pull
docker compose build
docker compose up -d     # recording stops for a few seconds
docker image prune -f
```

Database migrations run by themselves at start.

### Backup

Back up `.env` (the secret key) and `JAGA_DATA/jaga.db` (cameras, users, recording index). Recordings are plain MP4 files under `JAGA_DATA/recordings/<camera>/<date>/`, playable with any player.

## Run without Docker

Needs FFmpeg and [go2rtc](https://github.com/AlexxIT/go2rtc) on `PATH`, and Go plus Node.js to build.

```sh
make web build
./jaga init     # create jaga.yml, secret key and first admin
./jaga doctor   # check ffmpeg, go2rtc, data directory and ports
./jaga start    # open http://localhost:8080
```

## Privacy

Jaga never sends data anywhere: no telemetry, no analytics, no cloud account. Audio recording is off by default. If you install cameras, you are responsible for following local laws on video surveillance and data protection. In Indonesia, that includes UU No. 27/2022 (PDP).

Do not expose Jaga directly to the internet (no port forwarding on the router). To watch from outside, use a VPN such as [Tailscale](https://tailscale.com) or WireGuard: install it on the server and your phone, then open `http://<server-vpn-ip>:8080`. A reverse proxy with HTTPS works too, but then the proxy is what keeps Jaga safe. See [SECURITY.md](SECURITY.md).

## Documentation

Development docs (Bahasa Indonesia) live in [`docs/`](docs/README.md): architecture, design, CLI, branding.

## Maintainer

Jaga is an open source project maintained from the personal GitHub account of [@Ryandinulfatah12](https://github.com/Ryandinulfatah12), not by a company or organization. Images are published under that account (`ghcr.io/ryandinulfatah12/jaga`) and the Go module path is `github.com/ryandinulfatah12/jaga`. There is no plan to move to a GitHub organization; if that ever changes, it will be announced in the release notes with a migration path.

## Credits

Jaga runs [FFmpeg](https://ffmpeg.org) (LGPL) and [go2rtc](https://github.com/AlexxIT/go2rtc) (MIT) as separate processes. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Jaga is an independent project, written from scratch. Its design was influenced by observing how existing NVRs such as Shinobi behave, but it contains no code from them and is not affiliated with Shinobi Systems or any other project. ONVIF is a trademark of ONVIF, Inc. Jaga works with ONVIF cameras but is not an ONVIF conformant product.

## Contributing

Contributions are welcome. Commits must be signed off (DCO), and code must not be copied from GPL or proprietary projects. See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[Apache-2.0](LICENSE). The Jaga name and logo are covered by the [brand guidelines](docs/brand/README.md), not the code license.
