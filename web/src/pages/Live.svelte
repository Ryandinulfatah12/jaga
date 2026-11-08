<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { get } from '../lib/api.js'
  import LiveTile from '../lib/LiveTile.svelte'

  let { go, user } = $props()

  let cameras = $state([])
  let error = $state('')
  let loaded = $state(false)
  let layout = $state(Number(localStorage.getItem('jaga-grid')) || 4)
  let page = $state(0)
  let open = $state(null) // camera shown full size, main stream

  const cols = { 1: 'grid-cols-1', 4: 'sm:grid-cols-2', 9: 'sm:grid-cols-3', 16: 'sm:grid-cols-4' }
  // On wide screens the grid fits the viewport: width is capped so that the
  // rows (16:9 tiles) fit under the header instead of scrolling.
  const rows = { 1: 1, 4: 2, 9: 3, 16: 4 }

  onMount(async () => {
    try {
      cameras = (await get('/cameras')).filter((c) => c.enabled)
    } catch (e) {
      error = e.message
    } finally {
      loaded = true
    }
  })

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
  <div class="fixed inset-0 z-10 flex flex-col bg-black p-2" role="dialog" aria-modal="true" aria-label={open.name}>
    <div class="mb-2 flex items-center gap-3 text-white">
      <span class="font-medium">{open.name}</span>
      <span class="text-xs text-muted">main stream</span>
      <button class="ml-auto rounded-sm px-3 py-1 hover:bg-white/10" onclick={() => (open = null)}>Close (Esc)</button>
    </div>
    <div class="min-h-0 flex-1">
      <LiveTile camera={open} stream={`cam${open.id}`} big />
    </div>
  </div>
{/if}

<div class="mb-4 flex flex-wrap items-center gap-2">
  <h1 class="mr-auto text-xl font-semibold">Live</h1>
  <div class="flex rounded-sm border border-line" role="group" aria-label="Grid size">
    {#each [1, 4, 9, 16] as n (n)}
      <button
        class="px-3 py-1 tnum {layout === n ? 'bg-raised text-fg' : 'text-muted hover:text-fg'}"
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
  <div class="rounded-md border border-dashed border-line p-8 text-center text-muted">
    <p class="mb-3">No cameras yet.</p>
    {#if user.role === 'admin'}
      <button class="rounded-sm bg-accent px-4 py-2 font-semibold text-on-accent hover:bg-accent-hover" onclick={() => go('cameras')}>Add a camera</button>
    {/if}
  </div>
{:else}
  <div class="mx-auto grid grid-cols-1 gap-2 {cols[layout]}" style:max-width={`calc((100svh - 8.5rem) / ${rows[layout]} * 16 / 9 * ${Math.sqrt(layout)})`}>
    {#each shown as c (c.id)}
      <LiveTile camera={c} stream={gridStream(c)} onopen={() => (open = c)} />
    {/each}
  </div>
{/if}
