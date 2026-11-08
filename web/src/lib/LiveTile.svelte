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

<div class="relative overflow-hidden rounded-md border border-line bg-black {big ? 'h-full' : 'aspect-video max-h-full'}">
  <!-- Live view has no audio track to read: always muted, so autoplay works. -->
  <video bind:this={video} muted playsinline class="h-full w-full object-contain" aria-label={'Live: ' + camera.name}></video>

  {#if live !== 'playing'}
    <!-- The tile is always black, so fixed light text instead of theme colours. -->
    <div class="absolute inset-0 grid place-items-center p-4 text-center text-neutral-300">
      {#if live.startsWith('error:')}
        <p><span class="text-white">No live video.</span><br /><span class="font-mono text-xs">{live.slice(6)}</span><br />Retrying…</p>
      {:else}
        <p>Connecting…</p>
      {/if}
    </div>
  {/if}

  <!-- Bottom bar with a solid backdrop: cameras burn their own clock into the top corner. -->
  <div class="absolute inset-x-0 bottom-0 flex items-center gap-2 bg-black/90 px-3 py-1.5 text-white">
    <span class="truncate font-medium">{camera.name}</span>
    <span class="ml-auto flex shrink-0 items-center gap-1.5 text-xs">
      {#if st.cross}<span aria-hidden="true">✕</span>{:else}<span class="size-2 rounded-full {st.hollow ? 'border border-current' : st.dot}" class:rec-blink={st.blink}></span>{/if}
      {st.label}
    </span>
  </div>

  {#if onopen}
    <button class="absolute inset-0 cursor-zoom-in" onclick={onopen} aria-label={'Open ' + camera.name + ' full size'}></button>
  {/if}
</div>
