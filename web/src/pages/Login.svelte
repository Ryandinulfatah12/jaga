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

<main class="grid min-h-svh place-items-center p-4">
  <form onsubmit={submit} class="w-full max-w-xs rounded-lg border border-line bg-surface p-6">
    <img src="/mark.svg" alt="" class="mx-auto mb-4 size-12" />
    <h1 class="mb-6 text-center text-xl font-semibold">Sign in to Jaga</h1>
    <label class="mb-1 block text-muted" for="u">Username</label>
    <input
      id="u"
      bind:value={username}
      autocomplete="username"
      autocapitalize="none"
      required
      class="mb-4 w-full rounded-sm border border-line bg-bg px-3 py-2"
    />
    <label class="mb-1 block text-muted" for="p">Password</label>
    <input
      id="p"
      type="password"
      bind:value={password}
      autocomplete="current-password"
      required
      class="mb-4 w-full rounded-sm border border-line bg-bg px-3 py-2"
    />
    {#if error}
      <p class="mb-4 text-rec" role="alert">✗ {error}</p>
    {/if}
    <button
      disabled={busy}
      class="w-full rounded-sm bg-accent py-2 font-semibold text-on-accent hover:bg-accent-hover disabled:opacity-60"
      >{busy ? 'Signing in…' : 'Sign in'}</button
    >
  </form>
</main>
