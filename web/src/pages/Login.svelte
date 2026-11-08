<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { post } from '../lib/api.js'

  let { onlogin } = $props()
  let username = $state('')
  let password = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(e) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      onlogin(await post('/auth/login', { username, password }))
    } catch (err) {
      error = err.status === 429 ? 'Too many attempts. Wait a minute and try again.' : err.message
      password = ''
    } finally {
      busy = false
    }
  }
</script>

<!-- Decorative wall of camera frames behind the form; a few "recording". -->
<div class="wall" aria-hidden="true">
  {#each Array(36) as _, i (i)}
    <div class:on={[3, 8, 14, 21, 27, 32].includes(i)} class:lit={[8, 14, 15, 20, 22].includes(i)}></div>
  {/each}
</div>

<main class="relative grid min-h-svh place-items-center p-4">
  <form onsubmit={submit} class="glass rise w-full max-w-sm rounded-lg p-8 shadow-2xl shadow-black/50">
    <img src="/mark.svg" alt="" class="mx-auto mb-4 size-14" />
    <h1 class="text-center text-2xl font-semibold tracking-tight">Sign in to Jaga</h1>
    <p class="mb-7 text-center text-muted">Lightweight self-hosted NVR</p>
    <label class="mb-1 block text-muted" for="u">Username</label>
    <input
      id="u"
      bind:value={username}
      autocomplete="username"
      autocapitalize="none"
      required
      class="mb-4 w-full rounded-md border border-field bg-bg/70 px-3 py-2.5"
    />
    <label class="mb-1 block text-muted" for="p">Password</label>
    <input
      id="p"
      type="password"
      bind:value={password}
      autocomplete="current-password"
      required
      class="mb-4 w-full rounded-md border border-field bg-bg/70 px-3 py-2.5"
    />
    {#if error}
      <p class="mb-4 text-rec" role="alert">✗ {error}</p>
    {/if}
    <button
      disabled={busy}
      class="btn-primary mt-2 w-full rounded-md py-2.5 disabled:opacity-60"
      >{busy ? 'Signing in…' : 'Sign in'}</button
    >
  </form>
</main>
