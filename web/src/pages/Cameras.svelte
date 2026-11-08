<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { del, get, patch, post } from '../lib/api.js'
  import { statuses, stateView } from '../lib/status.svelte.js'

  let { user } = $props()
  const admin = $derived(user.role === 'admin')

  let cameras = $state([])
  let error = $state('')
  let editing = $state(null) // null | 'new' | camera
  let form = $state({})
  let formError = $state('')
  let saving = $state(false)
  let test = $state(null) // { busy } | { ok, streams } | { ok:false, error }

  async function load() {
    try {
      cameras = await get('/cameras')
    } catch (e) {
      error = e.message
    }
  }
  onMount(load)

  function edit(c) {
    editing = c ?? 'new'
    formError = ''
    test = null
    // URLs come back masked, so editing leaves them empty: blank = keep.
    form = c
      ? { name: c.name, main_url: '', sub_url: '', enabled: c.enabled, record_mode: c.record_mode, record_audio: c.record_audio, segment_sec: c.segment_sec, retention_days: c.retention_days ?? '', onvif_port: c.onvif_port ?? '' }
      : { name: '', main_url: '', sub_url: '', enabled: true, record_mode: 'continuous', record_audio: false, segment_sec: 600, retention_days: '', onvif_port: '' }
  }

  function body() {
    const b = {
      name: form.name.trim(),
      enabled: form.enabled,
      record_mode: form.record_mode,
      record_audio: form.record_audio,
      segment_sec: Number(form.segment_sec),
      retention_days: form.retention_days === '' ? null : Number(form.retention_days),
      onvif_port: form.onvif_port === '' ? null : Number(form.onvif_port),
    }
    if (form.main_url.trim()) b.main_url = form.main_url.trim()
    if (form.sub_url.trim()) b.sub_url = form.sub_url.trim()
    return b
  }

  async function save(e) {
    e.preventDefault()
    saving = true
    formError = ''
    try {
      const c = editing === 'new' ? await post('/cameras', body()) : await patch(`/cameras/${editing.id}`, body())
      await load()
      editing = c // stay in the form so "Test connection" works on the saved camera
      edit(c)
      test = { saved: true }
    } catch (err) {
      formError = err.message
    } finally {
      saving = false
    }
  }

  async function runTest(stream) {
    test = { busy: true }
    try {
      test = await post(`/cameras/${editing.id}/test${stream === 'sub' ? '?stream=sub' : ''}`)
    } catch (err) {
      test = { ok: false, error: err.message }
    }
  }

  async function remove() {
    if (!confirm(`Delete camera "${editing.name}"? Its recordings are removed by retention.`)) return
    try {
      await del(`/cameras/${editing.id}`)
      editing = null
      await load()
    } catch (err) {
      formError = err.message
    }
  }

  const kbps = (k) => (k >= 1000 ? (k / 1000).toFixed(1) + ' Mbps' : k + ' kbps')
  const input = 'h-10 w-full rounded-md border border-field bg-bg/70 px-3'
</script>

<div class="rise mb-5 flex items-center">
  <h1 class="mr-auto text-2xl font-semibold tracking-tight">Cameras</h1>
  {#if admin}
    <button class="btn-primary rounded-md px-4 py-2" onclick={() => edit(null)}>+ Add camera</button>
  {/if}
</div>

{#if error}
  <p class="text-rec" role="alert">✗ {error}</p>
{/if}

<div class="glass rise overflow-x-auto rounded-lg">
  <table class="w-full text-left">
    <thead class="text-xs tracking-wide text-muted uppercase">
      <tr>
        <th class="px-3 py-2 font-medium">Name</th>
        <th class="px-3 py-2 font-medium">Status</th>
        <th class="px-3 py-2 font-medium">Main URL</th>
        <th class="px-3 py-2 font-medium">Recording</th>
        <th class="px-3 py-2"></th>
      </tr>
    </thead>
    <tbody>
      {#each cameras as c (c.id)}
        {@const s = statuses[c.id]}
        {@const v = stateView(s?.state)}
        <tr class="border-t border-line/60 transition-colors hover:bg-raised/40">
          <td class="px-3 py-2 font-medium">{c.name}{#if !c.enabled}<span class="ml-2 text-muted">(disabled)</span>{/if}</td>
          <td class="px-3 py-2">
            <span class="flex items-center gap-1.5 {v.text}">
              {#if v.cross}<span aria-hidden="true">✕</span>{:else}<span class="size-2 rounded-full {v.hollow ? 'border border-current' : v.dot}"></span>{/if}{v.label}
            </span>
            {#if s?.error}<span class="block max-w-xs truncate font-mono text-xs text-muted" title={s.error}>{s.error}</span>{/if}
          </td>
          <td class="px-3 py-2 font-mono text-xs text-muted">{c.main_url}</td>
          <td class="px-3 py-2 text-muted">{c.record_mode === 'off' ? 'off' : `${c.segment_sec / 60} min segments`}</td>
          <td class="px-3 py-2 text-right">
            {#if admin}<button class="rounded-sm px-2 py-1 text-accent-fg hover:underline" onclick={() => edit(c)}>Edit</button>{/if}
          </td>
        </tr>
      {:else}
        <tr><td colspan="5" class="px-3 py-6 text-center text-muted">No cameras yet.</td></tr>
      {/each}
    </tbody>
  </table>
</div>

{#if editing}
  <div class="fixed inset-0 z-30 grid place-items-center bg-black/60 p-4 backdrop-blur-sm" role="dialog" aria-modal="true" aria-labelledby="cam-title">
    <form onsubmit={save} class="glass rise max-h-full w-full max-w-lg overflow-y-auto rounded-lg p-6 shadow-2xl shadow-black/50">
      <h2 id="cam-title" class="mb-4 text-lg font-semibold">{editing === 'new' ? 'Add camera' : 'Edit ' + editing.name}</h2>

      <label class="mb-1 block text-muted" for="f-name">Name</label>
      <input id="f-name" bind:value={form.name} required maxlength="64" class="{input} mb-3" />

      <label class="mb-1 block text-muted" for="f-main">Main stream URL</label>
      <input
        id="f-main"
        bind:value={form.main_url}
        required={editing === 'new'}
        placeholder={editing === 'new' ? 'rtsp://user:pass@192.168.1.10:554/stream1' : editing.main_url + '  (blank = keep)'}
        autocomplete="off"
        spellcheck="false"
        class="{input} mb-3 font-mono text-[13px] placeholder:text-muted"
      />
      <label class="mb-1 block text-muted" for="f-sub">Sub stream URL <span class="text-xs">(optional, used for the grid)</span></label>
      <input
        id="f-sub"
        bind:value={form.sub_url}
        placeholder={editing !== 'new' && editing.sub_url ? editing.sub_url + '  (blank = keep)' : 'rtsp://…/stream2'}
        autocomplete="off"
        spellcheck="false"
        class="{input} mb-3 font-mono text-[13px] placeholder:text-muted"
      />

      <div class="mb-3 grid grid-cols-2 gap-3">
        <div>
          <label class="mb-1 block text-muted" for="f-mode">Recording</label>
          <select id="f-mode" bind:value={form.record_mode} class={input}>
            <option value="continuous">Continuous</option>
            <option value="off">Off</option>
          </select>
        </div>
        <div>
          <label class="mb-1 block text-muted" for="f-seg">Segment length (s)</label>
          <input id="f-seg" type="number" min="10" max="3600" bind:value={form.segment_sec} class="{input} tnum" />
        </div>
        <div>
          <label class="mb-1 block text-muted" for="f-ret">Keep for (days)</label>
          <input id="f-ret" type="number" min="1" bind:value={form.retention_days} placeholder="default" class="{input} tnum" />
        </div>
        <div>
          <label class="mb-1 block text-muted" for="f-onvif">ONVIF port <span class="text-xs">(pan &amp; tilt)</span></label>
          <input id="f-onvif" type="number" min="1" max="65535" bind:value={form.onvif_port} placeholder="off (Tapo: 2020)" class="{input} tnum" />
        </div>
        <div class="flex flex-col justify-end gap-1">
          <label class="flex items-center gap-2"><input type="checkbox" bind:checked={form.enabled} /> Enabled</label>
          <label class="flex items-center gap-2"><input type="checkbox" bind:checked={form.record_audio} /> Record audio</label>
        </div>
      </div>

      {#if formError}
        <p class="mb-3 text-rec" role="alert">✗ {formError}</p>
      {/if}

      {#if editing !== 'new'}
        <div class="mb-4 rounded-md border border-line bg-bg/60 p-3" aria-live="polite">
          <div class="mb-2 flex items-center gap-2">
            <span class="mr-auto font-medium">Connection</span>
            <button type="button" class="rounded-full border border-line px-3 py-1 transition-colors hover:border-accent hover:text-accent-fg" disabled={test?.busy} onclick={() => runTest('main')}>Test main</button>
            {#if editing.sub_url}
              <button type="button" class="rounded-full border border-line px-3 py-1 transition-colors hover:border-accent hover:text-accent-fg" disabled={test?.busy} onclick={() => runTest('sub')}>Test sub</button>
            {/if}
          </div>
          {#if test?.saved}
            <p class="text-online">✓ Saved. Recording and live view use the new settings now.</p>
          {:else if test?.busy}
            <p class="text-muted">Probing for 5 seconds…</p>
          {:else if test?.ok}
            <ul class="font-mono text-xs">
              {#each test.streams as s (s.index)}
                <li>
                  <span class="text-online">✓</span>
                  {s.type} {s.codec.toUpperCase()}
                  {#if s.width}{s.width}×{s.height}{/if}
                  {#if s.fps}{Math.round(s.fps)} fps{/if}
                  {#if s.sample_rate}{s.sample_rate} Hz{/if}
                  {#if s.bitrate_kbps}· {kbps(s.bitrate_kbps)}{/if}
                  {#if s.gop_seconds}· keyframe {s.gop_seconds.toFixed(1)}s{/if}
                </li>
              {/each}
            </ul>
          {:else if test}
            <p class="font-mono text-xs text-rec">✗ {test.error}</p>
          {:else}
            <p class="text-muted">Save first, then test the saved URL.</p>
          {/if}
        </div>
      {/if}

      <div class="flex items-center gap-2">
        {#if editing !== 'new'}
          <button type="button" class="rounded-sm px-3 py-2 text-rec hover:underline" onclick={remove}>Delete</button>
        {/if}
        <button type="button" class="ml-auto rounded-sm px-4 py-2 text-muted hover:text-fg" onclick={() => (editing = null)}>Close</button>
        <button disabled={saving} class="btn-primary rounded-md px-5 py-2 disabled:opacity-60">{saving ? 'Saving…' : 'Save'}</button>
      </div>
    </form>
  </div>
{/if}
