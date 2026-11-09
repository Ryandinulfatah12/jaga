<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright The Jaga Authors -->
<script>
  import { onMount } from 'svelte'
  import { get } from '../lib/api.js'
  import { statuses, stateView } from '../lib/status.svelte.js'

  // Host health, recordings per camera, and About (versions, licenses).
  // Refreshed every 10 s while open; the camera states are live (SSE).

  let sys = $state(null)
  let cameras = $state([])
  let error = $state('')

  async function refresh() {
    try {
      ;[sys, cameras] = await Promise.all([get('/system'), get('/cameras')])
      error = ''
    } catch (e) {
      error = e.message
    }
  }
  onMount(() => {
    refresh()
    const t = setInterval(refresh, 10_000)
    return () => clearInterval(t)
  })

  const gb = (b) => (b / 1e9).toFixed(b >= 100e9 ? 0 : 1) + ' GB'
  const pct = (a, b) => (b ? Math.round((a / b) * 100) : 0)
  const ago = (t) => {
    const s = Math.max(0, (Date.now() - new Date(t).getTime()) / 1000)
    if (s < 3600) return `${Math.round(s / 60)} min`
    if (s < 86400) return `${(s / 3600).toFixed(1)} h`
    return `${(s / 86400).toFixed(1)} days`
  }
  const day = (ms) => new Date(ms).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short', hour12: false })

  const disk = $derived(sys?.disk)
  const diskPct = $derived(disk ? pct(disk.used, disk.total) : 0)
  const perDay = $derived(sys ? sys.recordings.reduce((n, r) => n + r.last_24h, 0) : 0)
  // Days until the quota is hit at the last 24 h rate; retention deletes
  // older recordings before that, so this is only a "how far" hint.
  const daysLeft = $derived(disk && perDay ? Math.max(0, (disk.total * (disk.quota_percent / 100) - disk.used) / perDay) : null)
  const rec = (id) => sys?.recordings.find((r) => r.camera_id === id)
</script>

<div class="rise mx-auto max-w-6xl space-y-4">
  <h1 class="text-xl font-semibold">System</h1>
  {#if error}<p class="text-accent-fg" role="alert">{error}</p>{/if}

  {#if sys}
    <div class="grid gap-4 sm:grid-cols-3">
      <section class="glass rounded-lg p-4" aria-labelledby="h-disk">
        <h2 id="h-disk" class="text-xs text-muted">Recordings disk</h2>
        <p class="mt-1 text-2xl font-semibold tnum">{diskPct}%</p>
        <div class="meter mt-2" role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow={diskPct} aria-label="Disk used">
          <span class:hot={diskPct >= disk.quota_percent} style:width={`${diskPct}%`}></span>
          <i style:left={`${disk.quota_percent}%`} title={`Quota ${disk.quota_percent}%`}></i>
        </div>
        <p class="mt-2 text-xs text-muted tnum">{gb(disk.used)} of {gb(disk.total)} · oldest recordings go at {disk.quota_percent}%</p>
        <p class="text-xs text-muted tnum">{gb(perDay)} recorded in 24 h{#if daysLeft != null}&nbsp;· quota in ~{daysLeft.toFixed(0)} days at this rate{/if}</p>
      </section>
      <section class="glass rounded-lg p-4" aria-labelledby="h-cpu">
        <h2 id="h-cpu" class="text-xs text-muted">CPU load (1 / 5 / 15 min)</h2>
        <p class="mt-1 text-2xl font-semibold tnum">{sys.load[0].toFixed(2)}</p>
        <p class="mt-2 text-xs text-muted tnum">{sys.load[1].toFixed(2)} · {sys.load[2].toFixed(2)} on {sys.cpus} cores</p>
      </section>
      <section class="glass rounded-lg p-4" aria-labelledby="h-mem">
        <h2 id="h-mem" class="text-xs text-muted">Memory</h2>
        {#if sys.mem_total}
          <p class="mt-1 text-2xl font-semibold tnum">{pct(sys.mem_used, sys.mem_total)}%</p>
          <p class="mt-2 text-xs text-muted tnum">host {gb(sys.mem_used)} of {gb(sys.mem_total)} · Jaga {(sys.rss / 1e6).toFixed(0)} MB</p>
        {:else}
          <p class="mt-1 text-muted">not available on this system</p>
        {/if}
      </section>
    </div>

    <section class="glass overflow-x-auto rounded-lg" aria-labelledby="h-cams">
      <h2 id="h-cams" class="px-4 pt-3 text-xs text-muted">Cameras</h2>
      <table class="w-full text-left text-sm whitespace-nowrap">
        <thead class="text-xs text-muted">
          <tr><th class="px-4 py-2 font-medium">Camera</th><th class="px-4 py-2 font-medium">Status</th><th class="px-4 py-2 font-medium">Recordings</th><th class="px-4 py-2 font-medium">Last 24 h</th><th class="px-4 py-2 font-medium">Oldest</th></tr>
        </thead>
        <tbody>
          {#each cameras as c (c.id)}
            {@const s = statuses[c.id]}
            {@const v = stateView(s?.state)}
            {@const r = rec(c.id)}
            <tr class="border-t border-line/60">
              <td class="px-4 py-2 font-medium">{c.name}</td>
              <td class="px-4 py-2">
                <span class="flex items-center gap-1.5 {v.text}">
                  {#if v.cross}<span aria-hidden="true">✕</span>{:else}<span class="size-2 rounded-full {v.hollow ? 'border border-current' : v.dot}"></span>{/if}{v.label}
                  {#if s?.since && s.state === 'recording'}<span class="text-muted">· {ago(s.since)}</span>{/if}
                </span>
                {#if s?.error && s.state !== 'recording'}<span class="block text-xs text-muted">{s.error}</span>{/if}
              </td>
              <td class="px-4 py-2 tnum">{r ? `${gb(r.bytes)} · ${r.segments} files` : '–'}</td>
              <td class="px-4 py-2 tnum">{r ? gb(r.last_24h) : '–'}</td>
              <td class="px-4 py-2 tnum text-muted">{r?.oldest ? day(r.oldest) : '–'}</td>
            </tr>
          {:else}
            <tr><td class="px-4 py-3 text-muted" colspan="5">No cameras yet.</td></tr>
          {/each}
        </tbody>
      </table>
    </section>

    <section class="glass rounded-lg p-4 text-sm" aria-labelledby="h-about">
      <h2 id="h-about" class="text-xs text-muted">About</h2>
      <p class="mt-1"><span class="font-semibold">Jaga {sys.version}</span> <span class="text-muted">({sys.commit}, {sys.go}) · up {ago(sys.started)}</span></p>
      {#each sys.tools as t (t)}<p class="text-muted tnum">{t}</p>{/each}
      <p class="mt-3 text-muted">
        Jaga is free software under the <a class="text-accent-fg underline" href="https://www.apache.org/licenses/LICENSE-2.0" target="_blank" rel="noreferrer">Apache License 2.0</a>.
        It runs FFmpeg (LGPL-2.1-or-later, separate program) and go2rtc (MIT, separate program), and the web UI uses the Inter and JetBrains Mono fonts (SIL OFL 1.1).
        Full license texts ship in the image under <code class="font-mono">/usr/share/doc/jaga/</code>.
        <a class="text-accent-fg underline" href="https://github.com/Ryandinulfatah12/jaga" target="_blank" rel="noreferrer">Source code</a>
      </p>
    </section>
  {:else if !error}
    <p class="text-muted">Loading…</p>
  {/if}
</div>
