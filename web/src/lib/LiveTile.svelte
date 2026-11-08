<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onDestroy } from 'svelte'
  import { play } from '../lib/mse.js'
  import { statuses, stateView } from '../lib/status.svelte.js'

  /** camera: from GET /cameras; stream: go2rtc stream name; big: fullscreen tile */
  let { camera, stream, big = false, onopen } = $props()

  let video = $state()
  let live = $state('connecting')
  let stop = () => {}

  $effect(() => {
    if (!video) return
    stop()
    stop = play(video, stream, (s) => (live = s))
    return () => stop()
  })
  onDestroy(() => stop())

  const st = $derived(stateView(statuses[camera.id]?.state))
</script>

<!-- The name sits under the video, never on it: cameras burn their own OSD into any corner. -->
<div class="tile relative flex flex-col overflow-hidden rounded-lg border border-line bg-surface {big ? 'h-full' : ''}">
  <div class="vignette relative bg-black {big ? 'min-h-0 flex-1' : 'aspect-video'}">
  <!-- Live view has no audio track to read: always muted, so autoplay works. -->
  <video bind:this={video} muted playsinline class="h-full w-full object-contain" aria-label={'Live: ' + camera.name}></video>

  {#if live !== 'playing'}
    <!-- The tile is always black, so fixed light text instead of theme colours. -->
    <div class="absolute inset-0 grid place-items-center bg-[radial-gradient(ellipse_at_center,rgb(26_34_49),rgb(0_0_0))] p-4 text-center text-neutral-300">
      {#if live.startsWith('error:')}
        <p>
          <span class="mx-auto mb-2 block size-9 rounded-full border border-white/15 text-lg leading-9 text-white/70" aria-hidden="true">⌁</span>
          <span class="font-medium text-white">No live video</span><br /><span class="font-mono text-xs text-neutral-400">{live.slice(6)}</span><br /><span class="text-xs">Retrying…</span>
        </p>
      {:else}
        <p class="flex items-center gap-2"><span class="size-4 animate-spin rounded-full border-2 border-white/20 border-t-accent" aria-hidden="true"></span>Connecting…</p>
      {/if}
    </div>
  {/if}

  </div>
  {#if !big}
    <div class="flex items-center gap-2 border-t border-line px-3 py-2">
      <span class="truncate font-medium">{camera.name}</span>
      <span class="pill ml-auto shrink-0 {st.blink ? 'pill-rec' : st.label === 'retrying' ? 'text-accent-fg' : 'text-muted'}">
        {#if st.cross}<span aria-hidden="true">✕</span>{:else}<span class="size-2 rounded-full {st.hollow ? 'border border-current' : st.dot}" class:rec-blink={st.blink}></span>{/if}
        {st.label}
      </span>
    </div>
  {/if}
  {#if onopen}
    <button class="absolute inset-0 cursor-zoom-in" onclick={onopen} aria-label={'Open ' + camera.name + ' full size'}></button>
  {/if}
</div>
