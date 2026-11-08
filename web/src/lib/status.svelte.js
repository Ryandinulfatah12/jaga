// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Camera status from GET /api/v1/events/stream (SSE). One EventSource for
// the whole app; it reconnects by itself and every connection starts with a
// "snapshot", so this store never needs to replay anything.

/** @type {Record<number, {camera_id:number,state:string,since?:string,error?:string,failures?:number}>} */
export const statuses = $state({})
export const stream = $state({ connected: false })

let es = null

export function connectStatus() {
  if (es) return
  es = new EventSource('/api/v1/events/stream', { withCredentials: true })
  es.addEventListener('snapshot', (e) => {
    for (const k of Object.keys(statuses)) delete statuses[k]
    for (const s of JSON.parse(e.data)) statuses[s.camera_id] = s
    stream.connected = true
  })
  es.addEventListener('status', (e) => {
    const s = JSON.parse(e.data)
    statuses[s.camera_id] = s
  })
  es.onerror = () => {
    stream.connected = false // EventSource retries on its own
  }
}

export function disconnectStatus() {
  es?.close()
  es = null
  stream.connected = false
}

/** Label and colour class for a state; never colour alone (docs/07-branding.md). */
export function stateView(state) {
  switch (state) {
    case 'recording':
      return { label: 'REC', dot: 'bg-rec', text: 'text-fg', blink: true }
    case 'starting':
      return { label: 'starting', dot: 'bg-accent', text: 'text-muted' }
    case 'retrying':
      return { label: 'retrying', dot: 'bg-accent', text: 'text-accent-fg' }
    case 'offline':
      return { label: 'offline', dot: 'bg-offline', text: 'text-muted', cross: true }
    default:
      return { label: 'not recording', dot: 'bg-offline', text: 'text-muted', hollow: true }
  }
}
