# Branding

### Nama

**Jaga** (dibaca *JA-ga*, /ˈdʒa.ga/), kata Indonesia/Melayu untuk "menjaga, berjaga". Pendek, mudah diketik, dan langsung menjelaskan fungsinya. Nama ini juga dipakai untuk binary CLI:

```
jaga start
```

| Konteks | Penulisan |
| --- | --- |
| Kalimat / judul | Jaga |
| Binary, paket, image, repo | `jaga` (huruf kecil semua) |
| Prefix env var | `JAGA_` (contoh `JAGA_SECRET_KEY`) |
| Prefix CSS / token | `--jg-` |

Hindari "JAGA" (bukan singkatan) dan "Jaga NVR" sebagai nama produk. "NVR" cukup muncul di tagline.

### Tagline

- **EN (utama, untuk README & GitHub):** Lightweight self-hosted NVR. One binary, no transcoding.
- **ID:** NVR self-host yang ringan. Satu binary, tanpa transcode.
- **Deskripsi repo GitHub (≤ 120 karakter):** `Lightweight self-hosted NVR for RTSP/ONVIF cameras. One Go binary, SQLite, no transcoding.`
- **Topics GitHub:** `nvr`, `cctv`, `rtsp`, `onvif`, `webrtc`, `ffmpeg`, `self-hosted`, `surveillance`, `home-security`, `golang`

### Logo

Logo Jaga berupa huruf **j** monoline dengan titik berwarna amber. Titik itu dibaca sebagai dua hal sekaligus: lampu indikator REC pada kamera dan lentera petugas ronda malam.

| File | Pakai untuk |
| --- | --- |
| [`brand/jaga-mark.svg`](brand/jaga-mark.svg) | Ikon app/PWA, avatar GHCR/Docker Hub |
| [`brand/favicon.svg`](brand/favicon.svg) | Favicon web UI (salinan mark) |
| [`brand/jaga-wordmark-on-dark.svg`](brand/jaga-wordmark-on-dark.svg) | Header README / UI di latar gelap |
| [`brand/jaga-wordmark-on-light.svg`](brand/jaga-wordmark-on-light.svg) | Header README / UI di latar terang |
| [`brand/social-preview.svg`](brand/social-preview.svg) / [`.png`](brand/social-preview.png) | Social preview repo GitHub (1280×640) |

Wordmark digambar sebagai path (stroke 6, ujung bulat, bowl lingkaran r 9), jadi tidak bergantung pada font yang terpasang.

**Aturan pakai**

- Titik pada huruf j selalu `lentera-500`. Huruf lainnya mengikuti warna teks: `malam-50` di latar gelap, `malam-900` di latar terang.
- Ruang kosong di sekitar logo minimal setinggi titik j.
- Ukuran minimum: mark 16 px, wordmark tinggi 20 px.
- Jangan memutar, memberi bayangan, gradien, atau outline. Jangan mengganti warna titik.

README memakai `<picture>` agar wordmark mengikuti tema GitHub:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/jaga-wordmark-on-dark.svg">
  <img src="docs/brand/jaga-wordmark-on-light.svg" alt="Jaga" height="64">
</picture>
```

### Palet warna

Konsepnya *jaga malam*: latar biru-arang (**Malam**) dengan satu aksen hangat (**Lentera**). Amber dipilih karena kontras tinggi di layar gelap, jelas berbeda dari biru yang umum dipakai proyek NVR lain, dan secara alami berarti "perhatian".

**Malam (netral)**

| Token | Hex | Peran |
| --- | --- | --- |
| `malam-50` | `#F4F6F9` | Teks utama (dark), surface (light) |
| `malam-100` | `#E6E9EF` | |
| `malam-200` | `#CDD3DE` | Border (light) |
| `malam-300` | `#A9B2C3` | |
| `malam-400` | `#8792A8` | Teks sekunder (dark) |
| `malam-500` | `#56627A` | Teks sekunder (light), kamera offline |
| `malam-600` | `#3A4559` | |
| `malam-700` | `#263043` | Border (dark) |
| `malam-800` | `#1A2231` | Elemen raised (dark) |
| `malam-900` | `#111723` | Surface (dark), teks utama (light) |
| `malam-950` | `#0B0F17` | Latar (dark) |

**Lentera (brand)**

| Token | Hex | Peran |
| --- | --- | --- |
| `lentera-50` | `#FFF8E6` | |
| `lentera-100` | `#FEEDC2` | |
| `lentera-200` | `#FDDB85` | |
| `lentera-300` | `#FCC648` | |
| `lentera-400` | `#FBB534` | Hover, teks aksen & focus ring (dark) |
| `lentera-500` | `#F5A524` | **Warna brand.** Tombol utama, titik logo, motion |
| `lentera-600` | `#C27608` | Focus ring (light) |
| `lentera-700` | `#9C5C05` | Teks aksen / link (light) |
| `lentera-800` | `#7A4708` | |
| `lentera-900` | `#5C360B` | |

**Status**

| Token | Hex | Arti |
| --- | --- | --- |
| `rekam-500` / `rekam-600` | `#F2545B` / `#C9262E` | REC aktif, error, hapus (dark / light) |
| `aman-500` / `aman-600` | `#2FCB7A` / `#13814B` | Kamera online, sukses (dark / light) |
| `malam-500` | `#56627A` | Kamera offline / disabled |
| `lentera-500` @ 35% | `rgb(245 165 36 / .35)` | Blok motion di timeline |

Aturan warna:

- **Amber berarti "lihat ini".** Dipakai untuk aksi utama, event motion, dan peringatan. Jangan untuk dekorasi.
- **Merah hanya untuk REC dan error/aksi destruktif.** Titik REC berkedip pelan (1 s) hanya di live view.
- **Tombol utama:** latar `lentera-500`, teks `malam-950`. Jangan pakai teks putih di atas amber karena kontrasnya hanya 2:1.
- Di tema terang, teks amber pakai `lentera-700`, bukan `lentera-500`.
- Status tidak boleh dibedakan dari warna saja. Selalu sertakan ikon atau label (`● REC`, `○ offline`).

Semua token ada di [`brand/tokens.css`](brand/tokens.css). Tema gelap adalah default. Tema terang aktif lewat `data-theme="light"` pada `<html>`.

### Kontras (WCAG 2.1)

Semua pasangan teks yang dipakai lolos AA (≥ 4.5:1):

| Pasangan | Rasio |
| --- | --- |
| `malam-50` di `malam-950` | 17.7:1 |
| `malam-400` di `malam-950` | 6.1:1 |
| `malam-400` di `malam-800` | 5.1:1 |
| `lentera-500` di `malam-950` | 9.4:1 |
| `malam-950` di `lentera-500` (tombol) | 9.4:1 |
| `rekam-500` di `malam-950` | 5.7:1 |
| `aman-500` di `malam-950` | 9.1:1 |
| `malam-900` di putih | 17.9:1 |
| `malam-500` di `malam-50` | 5.7:1 |
| `lentera-700` di `malam-50` | 4.9:1 |
| `rekam-600` di putih | 5.5:1 |
| `aman-600` di `malam-50` | 4.5:1 |

### Tipografi

| Peran | Font | Catatan |
| --- | --- | --- |
| UI & dokumentasi | **Inter** (OFL) | 400 / 500 / 600. Aktifkan `font-feature-settings: "tnum"` untuk angka di tabel status. |
| Timestamp, kode, CLI, nama file | **JetBrains Mono** (OFL) | 400 / 600 |

Font di-bundle ke dalam binary (embed) supaya UI tetap tampil benar di jaringan lokal tanpa internet. Jangan memuatnya dari Google Fonts.

Skala ukuran UI: 12 / 13 / 14 (body) / 16 / 20 / 24 / 32 px. Radius: 6 (input, chip), 10 (kartu), 14 (modal, ikon app).

### Integrasi frontend (Tailwind)

```js
// web/tailwind.config.js
export default {
  theme: {
    extend: {
      colors: {
        malam: { 50: "#F4F6F9", 100: "#E6E9EF", 200: "#CDD3DE", 300: "#A9B2C3", 400: "#8792A8",
                 500: "#56627A", 600: "#3A4559", 700: "#263043", 800: "#1A2231", 900: "#111723", 950: "#0B0F17" },
        lentera: { 50: "#FFF8E6", 100: "#FEEDC2", 200: "#FDDB85", 300: "#FCC648", 400: "#FBB534",
                   500: "#F5A524", 600: "#C27608", 700: "#9C5C05", 800: "#7A4708", 900: "#5C360B" },
        rekam: { 400: "#F87177", 500: "#F2545B", 600: "#C9262E" },
        aman: { 400: "#5BDB98", 500: "#2FCB7A", 600: "#13814B" },
      },
      fontFamily: {
        sans: ["Inter", "-apple-system", "Segoe UI", "sans-serif"],
        mono: ["JetBrains Mono", "SF Mono", "Menlo", "monospace"],
      },
    },
  },
};
```

Komponen sebaiknya memakai variabel semantik (`var(--jg-surface)`, `var(--jg-accent)`) dan bukan warna skala secara langsung, supaya pergantian tema cukup dilakukan di satu tempat.

### Warna di CLI

Output terminal memakai palet yang sama. Implementasinya lewat `charmbracelet/lipgloss` dengan fallback ANSI-256:

| Peran | Truecolor | ANSI-256 | Contoh |
| --- | --- | --- | --- |
| Nama brand, aksen | `#F5A524` | 214 | `jaga v0.1.0` |
| REC / error | `#F2545B` | 203 | `● REC`, `✗ ffmpeg not found` |
| Online / sukses | `#2FCB7A` | 42 | `✓ ready` |
| Teks sekunder | `#8792A8` | 245 | path, durasi, URL |

- Warna dimatikan otomatis jika `NO_COLOR` diset, output bukan TTY, atau flag `--no-color` dipakai.
- Log dalam mode non-TTY / `--log-format json` tidak memakai warna maupun simbol.

Detail perintah dan contoh output ada di [CLI](08-cli.md).

### Suara & gaya tulisan

- **Singkat, tenang, faktual.** Jaga ibarat petugas jaga: melapor seperlunya, tidak panik.
  - Bagus: `garasi offline (3x gagal). Coba lagi dalam 30s.`
  - Hindari: `ERROR!!! Camera garasi has FAILED!`
- Pesan error selalu memuat apa yang terjadi dan langkah berikutnya.
- README, UI, dan pesan CLI ditulis dalam bahasa Inggris (untuk komunitas global). Dokumentasi pengembangan di `docs/` tetap dalam bahasa Indonesia. Terjemahan UI (i18n `id`, `en`) masuk backlog.
- Hindari bahasa yang menakut-nakuti ("protect your family from intruders"). Fokus pada fungsi: rekam, lihat, cari.
- Untuk nama pihak lain, tulis "works with ONVIF cameras", jangan "ONVIF conformant/certified". Lihat [aturan merek](09-legal.md#merek-dan-nama-pihak-lain).

### Lisensi aset brand

Logo dan nama Jaga tidak tercakup lisensi Apache-2.0 (pasal 6). Aturan pemakaiannya ada di [`brand/README.md`](brand/README.md).

### Checklist sebelum publikasi

- [x] Identitas: repo, GHCR, dan module path Go di bawah akun pribadi `ryandinulfatah12`, tanpa org (keputusan 7 Nov 2026). Domain opsional, lihat backlog
- [x] Upload `social-preview.png` di Settings → Social preview
- [x] Lisensi Apache-2.0 + `NOTICE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `THIRD_PARTY_NOTICES.md` ([detail](09-legal.md))
- [x] Isi kontak penegakan di `CODE_OF_CONDUCT.md`
- [ ] Cek merek di PDKI & WIPO (kelas 9, 42)
