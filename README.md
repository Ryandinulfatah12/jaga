<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/jaga-wordmark-on-dark.svg">
    <img src="docs/brand/jaga-wordmark-on-light.svg" alt="Jaga" height="72">
  </picture>
</p>

<p align="center"><b>Lightweight self-hosted NVR. One binary, no transcoding.</b></p>

---

Jaga records, streams and watches your RTSP cameras (including ONVIF cameras) from a single Go binary with SQLite. Video is never transcoded: recordings and live view use the camera's own stream (`-c copy`), so a mini PC can handle 16 cameras at 1080p.

> **Status:** design phase. Nothing to install yet. See the [roadmap](docs/05-todo.md).

## Planned features

- Continuous segmented recording (MP4) with retention by days and disk quota
- Multi-camera live grid over WebRTC/MSE (via go2rtc)
- Timeline playback, clip export
- Motion events (ONVIF or frame diff) with Telegram/webhook notifications
- Multi-user login (admin/viewer)

## Quick start (planned)

```sh
jaga init     # create jaga.yml, secret key and first admin
jaga start    # open http://localhost:8080
```

```yaml
# docker-compose.yml
services:
  jaga:
    image: ghcr.io/ryandinulfatah12/jaga:latest
    network_mode: host
    restart: unless-stopped
    volumes:
      - /mnt/cctv:/data
    environment:
      - JAGA_SECRET_KEY=change-me
```

## Privacy

Jaga never sends data anywhere: no telemetry, no analytics, no cloud account. Audio recording is off by default. If you install cameras, you are responsible for following local laws on video surveillance and data protection. In Indonesia, that includes UU No. 27/2022 (PDP).

Do not expose Jaga directly to the internet. Use a VPN or a reverse proxy with HTTPS. See [SECURITY.md](SECURITY.md).

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
