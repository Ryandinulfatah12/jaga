// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Minimal MSE player for go2rtc's WebSocket API (/live/api/ws?src=<stream>),
// the protocol checked in the phase 2 e2e test: send {"type":"mse","value":
// "<codecs>"}, receive {"type":"mse","value":"video/mp4; codecs=..."} then
// fMP4 fragments as binary messages. No transcoding: the browser must
// support the camera codec (H.264 everywhere; H.265 only where MSE has it).
// ponytail: MSE only, ~1 s latency. WebRTC (lower latency, go2rtc's
// /api/webrtc is already proxied) can be added as a second mode later.

const CODECS = [
  'avc1.640029', 'avc1.64002A', 'avc1.640033', 'avc1.4D4029', 'avc1.42E01E',
  'hvc1.1.6.L153.B0', 'mp4a.40.2', 'mp4a.40.5', 'flac', 'opus',
]

function supported() {
  const MS = window.ManagedMediaSource ?? window.MediaSource
  if (!MS) return { MS: null, codecs: '' }
  return { MS, codecs: CODECS.filter((c) => MS.isTypeSupported(`video/mp4; codecs="${c}"`)).join() }
}

/**
 * Plays stream `src` into `video`. Returns a stop function. onState gets
 * 'connecting' | 'playing' | 'error:<message>'; the player reconnects after
 * errors (2 s, then 5 s, ...) until stopped.
 */
export function play(video, src, onState = () => {}) {
  const { MS, codecs } = supported()
  if (!MS) {
    onState('error:this browser has no Media Source Extensions')
    return () => {}
  }
  let ws, ms, sb, url, timer
  let stopped = false
  let delay = 2000
  const queue = []

  const cleanup = () => {
    ws?.close()
    ws = null
    if (url) URL.revokeObjectURL(url)
    url = null
    sb = null
    queue.length = 0
  }
  const retry = (why) => {
    if (stopped) return
    cleanup()
    onState('error:' + why)
    timer = setTimeout(connect, delay)
    delay = Math.min(delay * 2, 30000)
  }
  const pump = () => {
    if (!sb || sb.updating || !queue.length) return
    try {
      sb.appendBuffer(queue.shift())
    } catch (e) {
      // QuotaExceeded: drop what is far behind the live edge and go on.
      if (e.name === 'QuotaExceededError' && video.buffered.length) {
        sb.remove(video.buffered.start(0), video.currentTime - 5)
      } else retry(e.message)
    }
  }

  function connect() {
    onState('connecting')
    ms = new MS()
    url = URL.createObjectURL(ms)
    video.disableRemotePlayback = true // ManagedMediaSource (iOS) requires it
    video.src = url
    ms.addEventListener('sourceopen', () => {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
      ws = new WebSocket(`${proto}//${location.host}/live/api/ws?src=${encodeURIComponent(src)}`)
      ws.binaryType = 'arraybuffer'
      ws.onopen = () => ws.send(JSON.stringify({ type: 'mse', value: codecs }))
      ws.onmessage = (ev) => {
        if (typeof ev.data !== 'string') {
          queue.push(ev.data)
          pump()
          return
        }
        const msg = JSON.parse(ev.data)
        if (msg.type === 'mse') {
          try {
            sb = ms.addSourceBuffer(msg.value)
          } catch {
            return retry('codec not supported by this browser')
          }
          sb.mode = 'segments'
          sb.addEventListener('updateend', () => {
            // Stay near the live edge and keep the buffer short.
            const b = video.buffered
            if (b.length) {
              const end = b.end(b.length - 1)
              if (end - video.currentTime > 3) video.currentTime = end - 0.5
              if (!sb.updating && video.currentTime - b.start(0) > 30) sb.remove(b.start(0), video.currentTime - 10)
            }
            pump()
          })
          video.play().catch(() => {}) // muted autoplay
          delay = 2000
          onState('playing')
        } else if (msg.type === 'error') {
          retry(String(msg.value).replace(/^mse: /, ''))
        }
      }
      ws.onclose = () => retry('connection closed')
    }, { once: true })
  }

  connect()
  return () => {
    stopped = true
    clearTimeout(timer)
    cleanup()
    video.removeAttribute('src')
    video.load()
  }
}
