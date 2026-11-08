<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { post } from './api.js'

  /** Pan & tilt pad like a phone camera app: hold to move, release to stop. */
  let { camera } = $props()

  const SPEED = 0.5
  // The server asks the camera to stop by itself after 1 s, so while a
  // button is held the move is repeated; a lost "stop" can't leave it turning.
  const REPEAT_MS = 700
  let timer = null
  let active = $state('') // direction being held
  let error = $state('')

  const dirs = {
    up: { tilt: 1, label: 'Tilt up', rot: 0, area: 'u' },
    left: { pan: -1, label: 'Pan left', rot: 270, area: 'l' },
    right: { pan: 1, label: 'Pan right', rot: 90, area: 'r' },
    down: { tilt: -1, label: 'Tilt down', rot: 180, area: 'd' },
  }

  async function send(body) {
    try {
      await post(`/cameras/${camera.id}/ptz`, body)
      error = ''
    } catch (e) {
      error = e.message
      end()
    }
  }

  function start(dir) {
    if (active === dir) return
    end(false)
    active = dir
    const d = dirs[dir]
    const move = () => send({ action: 'move', pan: (d.pan ?? 0) * SPEED, tilt: (d.tilt ?? 0) * SPEED })
    move()
    timer = setInterval(move, REPEAT_MS)
  }

  function end(sendStop = true) {
    if (!active) return
    clearInterval(timer)
    timer = null
    active = ''
    if (sendStop) send({ action: 'stop' })
  }

  const keys = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right' }
  function keydown(e) {
    if (keys[e.key] && !e.repeat && !e.target.closest('input, select, textarea')) {
      e.preventDefault()
      start(keys[e.key])
    }
  }
  function keyup(e) {
    if (keys[e.key] === active) end()
  }

  $effect(() => () => end()) // stop when the pad goes away
</script>

<svelte:window onkeydown={keydown} onkeyup={keyup} onblur={() => end()} />

<div class="flex flex-col items-center gap-2">
  <div class="ptz-pad" role="group" aria-label="Pan and tilt (hold, or use the arrow keys)">
    {#each Object.entries(dirs) as [dir, d] (dir)}
      <button
        type="button"
        class="ptz-btn area-{d.area}"
        class:on={active === dir}
        aria-label={d.label}
        onpointerdown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId)
          start(dir)
        }}
        onpointerup={() => end()}
        onpointercancel={() => end()}
        onlostpointercapture={() => end()}
        oncontextmenu={(e) => e.preventDefault()}
        ><svg class="ptz-icon rot-{d.rot}" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 15l6-6 6 6" /></svg></button
      >
    {/each}
  </div>
  {#if error}<p class="max-w-56 text-center text-xs text-rec" role="alert">✗ {error}</p>{/if}
</div>
