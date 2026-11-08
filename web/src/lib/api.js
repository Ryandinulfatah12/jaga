// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Thin fetch wrapper for /api/v1. The session is an HttpOnly cookie, so
// there is no token to keep here.

export class APIError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

/** Called on 401 from any request except login; App sends the user to the login page. */
let onUnauthorized = () => {}
export function setUnauthorizedHandler(fn) {
  onUnauthorized = fn
}

export async function api(method, path, body) {
  const res = await fetch('/api/v1' + path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return null
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    if (res.status === 401 && path !== '/auth/login') onUnauthorized()
    throw new APIError(res.status, data?.error ?? res.statusText)
  }
  return data
}

export const get = (p) => api('GET', p)
export const post = (p, b) => api('POST', p, b ?? {})
export const patch = (p, b) => api('PATCH', p, b)
export const del = (p) => api('DELETE', p)
