<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { get } from '../lib/api.js'
  import LiveTile from '../lib/LiveTile.svelte'
  import { statuses } from '../lib/status.svelte.js'

  let { go, user } = $props()

  let cameras = $state([])
  let error = $state('')
  let loaded = $state(false)
  let layout = $state(Number(localStorage.getItem('jaga-grid')) || 4)
  let page = $state(0)
  let open = $state(null) // camera shown full size, main stream

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
    if (e.key === 'Escape') open = null
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <div class="fixed inset-0 z-30 flex flex-col bg-black/95 p-3 backdrop-blur" role="dialog" aria-modal="true" aria-label={open.name}>
    <div class="mb-2 flex items-center gap-3 text-white">
      <span class="font-medium">{open.name}</span>
      <span class="text-xs text-muted">main stream</span>
      <button class="ml-auto rounded-full border border-white/15 px-4 py-1 hover:bg-white/10" onclick={() => (open = null)}>Close (Esc)</button>
    </div>
    <div class="min-h-0 flex-1">
      <LiveTile camera={open} stream={`cam${open.id}`} big />
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
