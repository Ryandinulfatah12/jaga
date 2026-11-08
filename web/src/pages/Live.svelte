<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { get } from '../lib/api.js'
  import LiveTile from '../lib/LiveTile.svelte'
  import PTZPad from '../lib/PTZPad.svelte'
  import { statuses } from '../lib/status.svelte.js'

  let { go, user } = $props()

  let cameras = $state([])
  let error = $state('')
  let loaded = $state(false)
  let layout = $state(Number(localStorage.getItem('jaga-grid')) || 4)
  let page = $state(0)
  let open = $state(null) // camera shown full size
  // Cameras like the Tapo C2xx refuse a second RTSP session on the main
  // stream while the recorder holds it, so a camera that records its main
  // stream is watched full size on the sub stream. Recording the sub stream
  // frees main for full-resolution live view.
  // ponytail: per-camera switch if a camera allows two sessions.
  const fullSub = (c) => !!c.sub_url && c.enabled && c.record_mode === 'continuous' && c.record_stream !== 'sub'
  let muted = $state(true)
  let shot = $state('') // snapshot feedback

  const cols = { 1: 'grid-cols-1', 4: 'sm:grid-cols-2', 9: 'sm:grid-cols-3', 16: 'sm:grid-cols-4' }
  // On wide screens the grid fits the viewport: width is capped so that the
  // rows (16:9 video + 2.5rem caption) fit under the header instead of scrolling.
  const rows = { 1: 1, 4: 2, 9: 3, 16: 4 }
  // px, not rem (html is 14px): header + title row + padding, tile caption, grid gap.
  const CHROME = 160
  const CAPTION = 44 // caption row + tile border
  const GAP = 12
  const gridWidth = $derived(
    `calc(((100svh - ${CHROME}px - ${(rows[layout] - 1) * GAP}px) / ${rows[layout]} - ${CAPTION}px) * 16 / 9 * ${rows[layout]} + ${(rows[layout] - 1) * GAP}px)`,
  )

  onMount(async () => {
    try {
      cameras = (await get('/cameras')).filter((c) => c.enabled)
    } catch (e) {
      error = e.message
    } finally {
      loaded = true
    }
  })

  const count = (state) => cameras.filter((c) => statuses[c.id]?.state === state).length
  const pages = $derived(Math.max(1, Math.ceil(cameras.length / layout)))
  const shown = $derived(cameras.slice(page * layout, page * layout + layout))
  // Stream names follow internal/live.StreamName; the grid prefers the sub stream.
  const gridStream = (c) => (c.sub_url ? `cam${c.id}_sub` : `cam${c.id}`)

  function setLayout(n) {
    layout = n
    page = 0
    localStorage.setItem('jaga-grid', String(n))
  }
  function onKey(e) {
    if (e.key === 'Escape') close()
  }
  function close() {
    open = null
    muted = true
    shot = ''
  }
  // Full-resolution JPEG, saved by the browser (Content-Disposition).
  async function snapshot() {
    shot = 'Saving…'
    const res = await fetch(`/api/v1/cameras/${open.id}/snapshot?download=1${fullSub(open) ? '' : '&stream=main'}`, { credentials: 'same-origin' })
    if (!res.ok) {
      shot = '✗ ' + ((await res.json().catch(() => null))?.error ?? res.statusText)
      return
    }
    const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'snapshot.jpg'
    const a = Object.assign(document.createElement('a'), { href: URL.createObjectURL(await res.blob()), download: name })
    a.click()
    URL.revokeObjectURL(a.href)
    shot = '✓ ' + name
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <div class="fixed inset-0 z-30 flex h-dvh flex-col bg-black p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]" role="dialog" aria-modal="true" aria-label={open.name}>
    <div class="mb-2 flex items-center gap-3 text-white">
      <span class="font-medium">{open.name}</span>
      <span class="text-xs text-neutral-400" title={fullSub(open) ? 'The main stream is in use by recording' : ''}>{fullSub(open) ? 'sub stream (main is recording)' : 'main stream'}</span>
      <button class="ml-auto rounded-full border border-white/15 px-4 py-1 hover:bg-white/10" onclick={close}>Close<span class="hidden [@media(hover:hover)]:inline"> (Esc)</span></button>
    </div>
    <div class="flex min-h-0 flex-1 flex-col gap-3 lg:flex-row">
      <!-- Phones: video pinned to the top like a camera app, controls in the thumb zone below. -->
      <div class="min-h-0 shrink lg:flex-1 max-lg:aspect-video max-lg:w-full">
        <LiveTile camera={open} stream={fullSub(open) ? `cam${open.id}_sub` : `cam${open.id}`} big {muted} />
      </div>
      <!-- Controls, like a phone camera app: under the video on small screens, beside it on wide ones. -->
      <aside class="glass flex shrink-0 flex-wrap items-center justify-center gap-5 rounded-lg p-4 text-white lg:w-60 lg:flex-col">
        <div class="flex gap-2">
          <button class="ctl" onclick={snapshot} aria-label="Save snapshot">
            <svg class="ctl-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 8h3l2-3h6l2 3h3v11H4z" /><circle cx="12" cy="13" r="3.5" /></svg>Snapshot
          </button>
          <button class="ctl" class:on={!muted} onclick={() => (muted = !muted)} aria-pressed={!muted} aria-label={muted ? 'Turn sound on' : 'Mute'}>
            <svg class="ctl-icon" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" />
              {#if muted}<path d="M16 9.5l5 5M21 9.5l-5 5" />{:else}<path d="M16 9a4.5 4.5 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11" />{/if}
            </svg>{muted ? 'Sound off' : 'Sound on'}
          </button>
        </div>
        {#if shot}<p class="w-full truncate text-center text-xs text-neutral-300" role="status" title={shot}>{shot.startsWith('✓') ? '✓ Snapshot saved' : shot}</p>{/if}
        {#if open.ptz}
          <div class="flex flex-col items-center gap-1">
            <span class="text-xs tracking-wide text-neutral-400 uppercase">Pan &amp; tilt</span>
            <PTZPad camera={open} />
            <span class="hidden text-xs text-neutral-400 lg:block">Hold, or use the arrow keys</span>
          </div>
        {:else}
          <p class="max-w-56 text-center text-xs text-neutral-500">Pan &amp; tilt: set the ONVIF port in Cameras (Tapo: 2020).</p>
        {/if}
      </aside>
    </div>
  </div>
{/if}

<!-- Toolbar lines up with the grid edges, but never narrower than 40rem. -->
<div class="rise mx-auto mb-5 flex flex-wrap items-center gap-3" style:max-width={`max(40rem, ${gridWidth})`}>
  <div class="mr-auto flex flex-wrap items-center gap-3">
    <h1 class="text-2xl font-semibold tracking-tight">Live</h1>
    {#if cameras.length}
      <!-- Summary: label + glyph, never colour alone. -->
      <div class="flex flex-wrap gap-2 text-xs font-medium" aria-label="Camera summary">
        <span class="glass flex items-center gap-1.5 rounded-full px-2.5 py-1 tnum"><span class="size-2 rounded-full bg-rec rec-blink"></span>{count('recording')} REC</span>
        {#if count('retrying') + count('starting')}
          <span class="glass flex items-center gap-1.5 rounded-full px-2.5 py-1 text-accent-fg tnum"><span class="size-2 rounded-full bg-accent"></span>{count('retrying') + count('starting')} connecting</span>
        {/if}
        {#if count('offline')}
          <span class="glass flex items-center gap-1.5 rounded-full px-2.5 py-1 text-rec tnum"><span aria-hidden="true">✕</span>{count('offline')} offline</span>
        {/if}
        <span class="glass rounded-full px-2.5 py-1 text-muted tnum">{cameras.length} cameras</span>
      </div>
    {/if}
  </div>
  <div class="glass hidden rounded-full p-0.5 sm:flex" role="group" aria-label="Grid size">
    {#each [1, 4, 9, 16] as n (n)}
      <button
        class="rounded-full px-3.5 py-1 tnum transition-colors {layout === n ? 'bg-accent font-semibold text-on-accent shadow-[0_0_14px_-2px_rgb(245_165_36/0.6)]' : 'text-muted hover:text-fg'}"
        aria-pressed={layout === n}
        onclick={() => setLayout(n)}>{n}</button
      >
    {/each}
  </div>
  {#if pages > 1}
    <div class="flex items-center gap-1 text-muted">
      <button class="rounded-sm px-2 py-1 hover:text-fg disabled:opacity-40" disabled={page === 0} onclick={() => page--} aria-label="Previous page">‹</button>
      <span class="tnum">{page + 1} / {pages}</span>
      <button class="rounded-sm px-2 py-1 hover:text-fg disabled:opacity-40" disabled={page >= pages - 1} onclick={() => page++} aria-label="Next page">›</button>
    </div>
  {/if}
</div>

{#if error}
  <p class="text-rec" role="alert">✗ {error}</p>
{:else if loaded && !cameras.length}
  <div class="glass rise mx-auto max-w-md rounded-lg p-10 text-center">
    <img src="/mark.svg" alt="" class="mx-auto mb-4 size-12 opacity-80" />
    <p class="mb-1 text-lg font-semibold">No cameras yet</p>
    <p class="mb-5 text-muted">Add an RTSP camera to start watching and recording.</p>
    {#if user.role === 'admin'}
      <button class="btn-primary rounded-md px-5 py-2.5" onclick={() => go('cameras')}>Add a camera</button>
    {/if}
  </div>
{:else}
  <div class="rise mx-auto grid grid-cols-1 gap-3 {cols[layout]}" style:max-width={gridWidth}>
    {#each shown as c (c.id)}
      <LiveTile camera={c} stream={gridStream(c)} onopen={() => (open = c)} />
    {/each}
  </div>
{/if}
