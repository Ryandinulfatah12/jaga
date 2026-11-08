// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

// Theme: dark by default; light is stored per browser (docs/07-branding.md).
if (localStorage.getItem('jaga-theme') === 'light') document.documentElement.dataset.theme = 'light'

mount(App, { target: document.getElementById('app') })
