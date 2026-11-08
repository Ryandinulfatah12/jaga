<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { get, post, setUnauthorizedHandler } from './lib/api.js'
  import { connectStatus, disconnectStatus, stream } from './lib/status.svelte.js'
  import Login from './pages/Login.svelte'
  import Live from './pages/Live.svelte'
  import Cameras from './pages/Cameras.svelte'

  let user = $state(null)
  let checked = $state(false)
  let page = $state(location.hash.slice(1) || 'live')
  let light = $state(document.documentElement.dataset.theme === 'light')

  // Hash routing: no server routes to keep in sync, back button works.
  const go = (p) => (location.hash = p)
  onMount(() => {
    const onHash = () => (page = location.hash.slice(1) || 'live')
    addEventListener('hashchange', onHash)
    setUnauthorizedHandler(() => {
      user = null
      disconnectStatus()
    })
    get('/auth/me')
      .then((u) => (user = u))
      .catch(() => {})
      .finally(() => (checked = true))
    return () => removeEventListener('hashchange', onHash)
  })
  $effect(() => {
    if (user) connectStatus()
  })

  async function logout() {
    await post('/auth/logout').catch(() => {})
    disconnectStatus()
    user = null
  }
  function toggleTheme() {
    light = !light
    if (light) document.documentElement.dataset.theme = 'light'
    else delete document.documentElement.dataset.theme
    localStorage.setItem('jaga-theme', light ? 'light' : 'dark')
  }

  const nav = [
    { id: 'live', label: 'Live' },
    { id: 'cameras', label: 'Cameras' },
  ]
</script>

{#if !checked}
  <div class="grid h-svh place-items-center text-muted">Loading…</div>
{:else if !user}
  <Login onlogin={(u) => (user = u)} />
{:else}
  <div class="flex min-h-svh flex-col">
    <header class="flex h-12 items-center gap-6 border-b border-line bg-surface px-4">
      <a href="#live" class="flex items-center" aria-label="Jaga home">
        <img src="/wordmark-dark.svg" alt="Jaga" class="h-5" class:hidden={light} />
        <img src="/wordmark-light.svg" alt="Jaga" class="h-5" class:hidden={!light} />
      </a>
      <nav class="flex gap-1" aria-label="Main">
        {#each nav as n (n.id)}
          <a
            href={'#' + n.id}
            class="rounded-sm px-3 py-1.5 font-medium {page === n.id ? 'bg-raised text-fg' : 'text-muted hover:text-fg'}"
            aria-current={page === n.id ? 'page' : undefined}>{n.label}</a
          >
        {/each}
      </nav>
      <div class="ml-auto flex items-center gap-3 text-muted">
        {#if !stream.connected}
          <span class="text-accent-fg" role="status">○ reconnecting…</span>
        {/if}
        <button class="rounded-sm px-2 py-1 hover:text-fg" onclick={toggleTheme} aria-label="Toggle light/dark theme">
          {light ? '☾ Dark' : '☀ Light'}
        </button>
        <span><span class="text-fg">{user.username}</span> · {user.role}</span>
        <button class="rounded-sm px-2 py-1 hover:text-fg" onclick={logout}>Log out</button>
      </div>
    </header>
    <main class="flex-1 p-4">
      {#if page === 'cameras'}
        <Cameras {user} />
      {:else}
        <Live {go} {user} />
      {/if}
    </main>
  </div>
{/if}
