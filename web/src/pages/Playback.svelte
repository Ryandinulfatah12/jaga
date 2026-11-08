<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { get } from '../lib/api.js'

  // One day of one camera: a 24 h bar with the recorded segments, click to
  // seek, and playback that runs on into the next segment like one video.
  // Segment files are plain MP4 served with HTTP Range, so the browser
  // seeks inside a 10-minute file without downloading all of it.

  let cameras = $state([])
  let camId = $state(Number(localStorage.getItem('jaga-pb-cam')) || 0)
  let day = $state(localDay(new Date()))
  let segs = $state([])
  let error = $state('')
  let loading = $state(false)

  let video = $state()
  let current = $state(null) // segment being played
  let playhead = $state(null) // Unix ms shown on the bar
  let hover = $state(null) // { x, t } under the pointer

  function localDay(d) {
    const p = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
  }
  // Midnight of the chosen day in the browser's timezone, as Unix ms.
  const dayStart = $derived(new Date(day + 'T00:00:00').getTime())
  const dayEnd = $derived(dayStart + 24 * 3600 * 1000)
  const pct = (t) => ((t - dayStart) / (dayEnd - dayStart)) * 100
  const clock = (t) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

  onMount(async () => {
    try {
      cameras = await get('/cameras')
      if (!cameras.some((c) => c.id === camId)) camId = cameras[0]?.id ?? 0
    } catch (e) {
      error = e.message
    }
  })

  $effect(() => {
    if (!camId) return
    localStorage.setItem('jaga-pb-cam', String(camId))
    load(camId, dayStart, dayEnd)
  })

  async function load(id, from, to) {
    loading = true
    error = ''
    current = null
    playhead = null
    if (video) {
      video.removeAttribute('src') // a new day or camera starts empty
      video.load()
    }
    try {
      segs = await get(`/cameras/${id}/segments?from=${from}&to=${to}`)
    } catch (e) {
      error = e.message
      segs = []
    } finally {
      loading = false
    }
  }

  // Play the segment that covers t (or the next one after it).
  function seek(t) {
    const seg = segs.find((s) => s.end > t)
    if (!seg) return
    const offset = Math.max(0, (t - seg.start) / 1000)
    if (current?.id === seg.id && video) {
      video.currentTime = offset
      video.play().catch(() => {})
      return
    }
    current = seg
    pendingOffset = offset
  }
  let pendingOffset = 0

  $effect(() => {
    if (!video || !current) return
    video.src = `/api/v1/segments/${current.id}/file`
    const start = () => {
      video.currentTime = pendingOffset
      video.play().catch(() => {})
    }
    video.addEventListener('loadedmetadata', start, { once: true })
    return () => video.removeEventListener('loadedmetadata', start)
  })

  function onTime() {
    if (current && video) playhead = current.start + video.currentTime * 1000
  }
  function onEnded() {
    const i = segs.findIndex((s) => s.id === current?.id)
    const next = segs[i + 1]
    if (next) {
      current = next
      pendingOffset = 0
    }
  }

  function barTime(e) {
    const r = e.currentTarget.getBoundingClientRect()
    const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
    return { x, t: dayStart + (x / r.width) * (dayEnd - dayStart) }
  }
  function onBarKey(e) {
    const step = e.shiftKey ? 600_000 : 60_000
    const base = playhead ?? segs[0]?.start
    if (base == null) return
    if (e.key === 'ArrowRight') seek(base + step)
    else if (e.key === 'ArrowLeft') seek(base - step)
    else return
    e.preventDefault()
  }

  function shiftDay(n) {
    const d = new Date(day + 'T12:00:00')
    d.setDate(d.getDate() + n)
    day = localDay(d)
  }

  const recordedMs = $derived(segs.reduce((n, s) => n + (Math.min(s.end, dayEnd) - Math.max(s.start, dayStart)), 0))
  const cam = $derived(cameras.find((c) => c.id === camId))
</script>

<div class="rise mx-auto max-w-6xl">
  <div class="mb-5 flex flex-wrap items-center gap-3">
    <h1 class="mr-auto text-2xl font-semibold tracking-tight">Playback</h1>
    <select bind:value={camId} class="h-9 rounded-md border border-field bg-bg/70 px-3" aria-label="Camera">
      {#each cameras as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
    </select>
    <div class="glass flex items-center rounded-full p-0.5">
      <button class="rounded-full px-3 py-1 text-muted hover:text-fg" onclick={() => shiftDay(-1)} aria-label="Previous day">‹</button>
      <input type="date" bind:value={day} max={localDay(new Date())} class="bg-transparent px-1 tnum" aria-label="Day" />
      <button class="rounded-full px-3 py-1 text-muted hover:text-fg disabled:opacity-40" disabled={day >= localDay(new Date())} onclick={() => shiftDay(1)} aria-label="Next day">›</button>
    </div>
  </div>

  {#if error}<p class="mb-3 text-rec" role="alert">✗ {error}</p>{/if}

  <div class="tile mb-4 overflow-hidden rounded-lg border border-line bg-black">
    <div class="relative aspect-video">
      <!-- svelte-ignore a11y_media_has_caption -->
      <video bind:this={video} controls playsinline class="h-full w-full object-contain" ontimeupdate={onTime} onended={onEnded} aria-label={cam ? 'Recording: ' + cam.name : 'Recording'}></video>
      {#if !current}
        <div class="absolute inset-0 grid place-items-center p-4 text-center text-neutral-300">
          {#if loading}
            <p class="flex items-center gap-2"><span class="size-4 animate-spin rounded-full border-2 border-white/20 border-t-accent" aria-hidden="true"></span>Loading…</p>
          {:else if segs.length}
            <p>Click the timeline to play.</p>
          {:else}
            <p><span class="font-medium text-white">No recordings</span><br /><span class="text-xs">for this camera on {day}</span></p>
          {/if}
        </div>
      {/if}
    </div>
    {#if current}
      <div class="flex items-center gap-3 border-t border-line bg-surface px-3 py-2 text-sm">
        <span class="font-medium tnum">{clock(playhead ?? current.start)}</span>
        <span class="text-muted tnum">segment {clock(current.start)} to {clock(current.end)}</span>
        <a class="ml-auto rounded-full border border-line px-3 py-1 text-xs hover:border-accent hover:text-accent-fg" href={`/api/v1/segments/${current.id}/file?download=1`}>Download segment</a>
      </div>
    {/if}
  </div>

  <div class="glass rounded-lg p-4">
    <div class="mb-2 flex items-center text-xs text-muted">
      <span>Timeline</span>
      <span class="ml-auto tnum">{(recordedMs / 3600000).toFixed(1)} h recorded · {segs.length} segments</span>
    </div>
    <!-- 24 h bar: recorded blocks, motion highlight, playhead, hover time. -->
    <div
      class="timeline relative h-12 cursor-pointer rounded-md"
      role="slider"
      tabindex="0"
      aria-label="Timeline, arrow keys move 1 minute (Shift: 10 minutes)"
      aria-valuemin={dayStart}
      aria-valuemax={dayEnd}
      aria-valuenow={playhead ?? dayStart}
      aria-valuetext={playhead ? clock(playhead) : 'not playing'}
      onclick={(e) => seek(barTime(e).t)}
      onpointermove={(e) => (hover = barTime(e))}
      onpointerleave={() => (hover = null)}
      onkeydown={onBarKey}
    >
      {#each segs as s (s.id)}
        <span class="seg" class:motion={s.has_motion} style:left={`${Math.max(0, pct(s.start))}%`} style:width={`${Math.max(0.15, pct(Math.min(s.end, dayEnd)) - Math.max(0, pct(s.start)))}%`}></span>
      {/each}
      {#if playhead}<span class="playhead" style:left={`${pct(playhead)}%`}></span>{/if}
      {#if hover}
        <span class="hover-line" style:left={`${hover.x}px`}></span>
        <span class="hover-tip tnum" style:left={`${hover.x}px`}>{clock(hover.t).slice(0, 5)}</span>
      {/if}
    </div>
    <div class="mt-1 flex justify-between text-[11px] text-muted tnum" aria-hidden="true">
      {#each [0, 3, 6, 9, 12, 15, 18, 21, 24] as h (h)}<span>{String(h).padStart(2, '0')}</span>{/each}
    </div>
  </div>
</div>
