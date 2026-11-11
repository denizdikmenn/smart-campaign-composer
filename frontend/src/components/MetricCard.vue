<script setup lang="ts">
defineProps<{
  label: string
  value: string
  hint?: string
  /** 0–100; renders a progress bar when provided. */
  percent?: number
}>()
</script>

<template>
  <article class="card">
    <h3>{{ label }}</h3>
    <p class="value">{{ value }}</p>
    <div v-if="percent !== undefined" class="bar" aria-hidden="true">
      <span :style="{ width: `${percent}%` }" />
    </div>
    <p v-if="hint" class="hint">{{ hint }}</p>
  </article>
</template>

<style scoped>
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  padding: 18px 20px;
}
h3 {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
}
.value {
  margin: 6px 0 10px;
  font-size: 32px;
  font-weight: 700;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}
.bar {
  height: 6px;
  border-radius: 99px;
  background: var(--accent-soft);
  overflow: hidden;
}
.bar span {
  display: block;
  height: 100%;
  background: var(--accent);
  border-radius: 99px;
}
.hint {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--text-muted);
}
</style>
