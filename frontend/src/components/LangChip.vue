<script setup lang="ts">
import { languageName } from '../api'
import Flag from './Flag.vue'

/** Language label used app-wide: flag + name (+ optional count). */
withDefaults(defineProps<{ lang: string; count?: number | string; short?: boolean; plain?: boolean }>(), {
  count: undefined,
  short: false,
  plain: false,
})
const number = new Intl.NumberFormat('en-US')
</script>

<template>
  <span class="lang-chip" :class="{ plain }" :title="languageName(lang)">
    <Flag :lang="lang" />
    <span class="name">{{ short ? lang.toUpperCase() : languageName(lang) }}</span>
    <span v-if="count != null" class="n">{{ typeof count === 'number' ? number.format(count) : count }}</span>
  </span>
</template>

<style scoped>
.lang-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px 2px 6px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.5;
  white-space: nowrap;
  color: var(--text);
  border-radius: 99px;
  background: var(--surface);
  border: 1px solid var(--border);
}
.lang-chip.plain {
  padding: 0;
  border: 0;
  background: none;
}
.n {
  padding: 0 6px;
  font-size: 11px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--text-muted);
  border-radius: 99px;
  background: var(--surface-muted);
}
.plain .n {
  padding: 0;
  background: none;
}
</style>
