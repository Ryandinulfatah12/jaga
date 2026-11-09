<!-- SPDX-License-Identifier: Apache-2.0 -->
<script>
  import { onMount } from 'svelte'
  import { get, post, setUnauthorizedHandler } from './lib/api.js'
  import { connectStatus, disconnectStatus, stream } from './lib/status.svelte.js'
  import Login from './pages/Login.svelte'
  import Live from './pages/Live.svelte'
  import Cameras from './pages/Cameras.svelte'
  import Playback from './pages/Playback.svelte'
  import System from './pages/System.svelte'

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
    { id: 'playback', label: 'Playback' },
    { id: 'cameras', label: 'Cameras' },
    { id: 'system', label: 'System' },
  ]
</script>

{#if !checked}
  <div class="grid h-svh place-items-center text-muted">Loading…</div>
{:else if !user}
  <Login onlogin={(u) => (user = u)} />
{:else}
  <div class="flex min-h-svh flex-col">
    <header class="glass sticky top-0 z-20 flex h-14 items-center gap-2 border-x-0 border-t-0 px-3 sm:gap-6 sm:px-5">
      <a href="#live" class="flex shrink-0 items-center" aria-label="Jaga home">
        <img src="/mark.svg" alt="Jaga" class="size-7 sm:hidden" />
        <img src="/wordmark-dark.svg" alt="Jaga" class="h-5 {light ? 'hidden' : 'hidden sm:block'}" />
        <img src="/wordmark-light.svg" alt="Jaga" class="h-5 {light ? 'hidden sm:block' : 'hidden'}" />
      </a>
      <!-- Scrolls sideways on narrow phones instead of pushing Log out off screen. -->
      <nav class="no-scrollbar flex min-w-0 gap-1 self-stretch overflow-x-auto" aria-label="Main">
        {#each nav as n (n.id)}
          <a
            href={'#' + n.id}
            class="relative flex items-center px-1.5 font-medium whitespace-nowrap transition-colors sm:px-3 {page === n.id ? 'text-fg' : 'text-muted hover:text-fg'}"
            aria-current={page === n.id ? 'page' : undefined}>{n.label}{#if page === n.id}<span class="absolute inset-x-3 bottom-0 h-0.5 rounded-full bg-accent shadow-[0_0_10px_rgb(245_165_36/0.8)]" {@attach (el) => el.parentElement.scrollIntoView({ block: 'nearest', inline: 'nearest' })}></span>{/if}</a
          >
        {/each}
      </nav>
      <div class="ml-auto flex shrink-0 items-center gap-1 whitespace-nowrap text-muted sm:gap-3">
        {#if !stream.connected}
          <span class="text-accent-fg" role="status">○ reconnecting…</span>
        {/if}
        <button class="flex min-h-9 min-w-9 items-center justify-center rounded-md px-2 whitespace-nowrap hover:bg-raised/60 hover:text-fg" onclick={toggleTheme} aria-label="Toggle light/dark theme">
          {light ? '☾' : '☀'}<span class="hidden sm:inline">&nbsp;{light ? 'Dark' : 'Light'}</span>
        </button>
        <span class="hidden items-center gap-2 sm:flex">
          <span class="grid size-7 place-items-center rounded-full bg-raised font-semibold text-fg uppercase ring-1 ring-line" title={`${user.username} · ${user.role}`}>{user.username[0]}</span>
          <span class="hidden md:inline"><span class="text-fg">{user.username}</span> · {user.role}</span>
        </span>
        <button class="flex min-h-9 min-w-9 items-center justify-center rounded-sm px-1 hover:text-fg sm:px-2" onclick={logout} aria-label="Log out"><span aria-hidden="true" class="sm:hidden">⏻</span><span class="hidden sm:inline">Log out</span></button>
      </div>
    </header>
    <main class="flex-1 p-3 sm:p-5">
      {#if page === 'cameras'}
        <Cameras {user} />
      {:else if page === 'playback'}
        <Playback />
      {:else if page === 'system'}
        <System />
      {:else}
        <Live {go} {user} />
      {/if}
    </main>
  </div>
{/if}
