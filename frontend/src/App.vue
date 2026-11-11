<script setup lang="ts">
import { computed } from 'vue'
import { href, route } from './router'
import CampaignsView from './views/CampaignsView.vue'
import ExplorerView from './views/ExplorerView.vue'
import NewCampaignView from './views/NewCampaignView.vue'

const VIEWS = {
  '/campaigns': CampaignsView,
  '/campaigns/new': NewCampaignView,
  '/explorer': ExplorerView,
} as const

const view = computed(() => VIEWS[route.value])
const section = computed(() => (route.value.startsWith('/campaigns') ? 'campaigns' : 'explorer'))
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <a class="brand" :href="href('/campaigns')">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 21V11" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none" />
          <path d="M12 13c0-5 3.5-8 9-8 0 5.5-3.5 8-9 8Z" fill="currentColor" />
          <path d="M12 16c0-4-3-6.5-7.5-6.5 0 4 3 6.5 7.5 6.5Z" fill="currentColor" opacity="0.6" />
        </svg>
        Campaign Studio
      </a>
      <nav aria-label="Main">
        <a :href="href('/campaigns')" :class="{ active: section === 'campaigns' }" :aria-current="section === 'campaigns' ? 'page' : undefined">
          Campaigns
        </a>
        <a :href="href('/explorer')" :class="{ active: section === 'explorer' }" :aria-current="section === 'explorer' ? 'page' : undefined">
          Audience Explorer
        </a>
      </nav>
    </aside>

    <main class="page">
      <!-- KeepAlive: a generated brief and explorer filters survive switching pages. -->
      <KeepAlive>
        <component :is="view" />
      </KeepAlive>
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 216px minmax(0, 1fr);
  gap: 16px;
  min-height: 100vh;
  padding: 16px;
}
.sidebar {
  position: sticky;
  top: 16px;
  align-self: start;
  min-height: calc(100vh - 32px);
  padding: 20px 12px;
  border-radius: 20px;
  color: #fff;
  background: var(--accent);
  box-shadow: var(--shadow);
}
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 24px 8px;
  font-size: 16px;
  font-weight: 600;
  color: inherit;
  text-decoration: none;
}
.brand svg {
  width: 22px;
  height: 22px;
  color: var(--accent-soft);
}
nav {
  display: grid;
  gap: 4px;
}
nav a {
  padding: 10px 12px;
  border-radius: var(--radius-control);
  color: #fff;
  font-weight: 500;
  text-decoration: none;
  transition: background-color 0.15s;
}
nav a:hover:not(.active) {
  background: rgb(255 255 255 / 0.12);
}
nav a.active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}
.sidebar a:focus-visible {
  outline: 2px solid #fff;
  outline-offset: 2px;
}
.page {
  width: 100%;
  max-width: 1180px;
  margin: 0 auto;
  padding: 16px 8px 96px; /* bottom room for the floating button */
}
@media (max-width: 820px) {
  .shell {
    grid-template-columns: 1fr;
  }
  .sidebar {
    position: static;
    min-height: 0;
    padding: 12px;
  }
  .brand {
    margin-bottom: 8px;
  }
  nav {
    grid-auto-flow: column;
  }
}
</style>
