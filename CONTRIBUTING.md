# Contributing to Jaga

Thanks for helping. Jaga is licensed under Apache-2.0, and every contribution must be safe to ship under that license.

## Developer Certificate of Origin (DCO)

Every commit must be signed off:

```sh
git commit -s -m "recorder: handle segment list EOF"
```

This adds a `Signed-off-by: Your Name <you@example.com>` line. By adding it you certify the [Developer Certificate of Origin 1.1](https://developercertificate.org): that you wrote the change or otherwise have the right to submit it under the project's license. CI rejects commits without a sign-off.

## Where code may come from

- **Allowed:** code you wrote yourself, and code from projects under MIT, BSD, ISC, Apache-2.0 or a similar permissive license, as long as you keep the original copyright notice and mention the source in the PR.
- **Not allowed:** code, SQL, UI text or assets copied from GPL, AGPL, LGPL or proprietary projects. This includes other NVRs such as Shinobi (CE and Pro), ZoneMinder, Moonfire NVR and SentryShot.
- **Not allowed:** snippets from Stack Overflow or other CC BY-SA sources, unless trivial.
- **Not allowed:** code with no license at all. "Found on GitHub" does not mean free to use.
- **AI-assisted code** is welcome, but you are responsible for it as if you wrote it. Do not submit output that reproduces recognizable third-party code.
- If you have read the source code of Shinobi or another copyleft NVR, please say so in your PR when working on the matching feature, so reviewers can take extra care.

Using those projects as a user is fine, including observing their behavior. Copying their code is not.

## Dependencies

New dependencies must use an allowed license (MIT, BSD-2/3-Clause, ISC, Apache-2.0, MPL-2.0, 0BSD, Zlib). CI checks this with `go-licenses` and a license checker for `web/`. GPL/AGPL libraries cannot be linked into Jaga. ML models need a permissive license for **both code and weights**, so Ultralytics YOLO (AGPL-3.0) cannot be used. See [docs/09-legal.md](docs/09-legal.md) for the full policy.

## Source file headers

Every source file starts with:

```go
// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors
```

## Security issues

Do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
