<script setup lang="ts">
import type { Audience } from '../api'

defineProps<{
  audience: Audience
  selected: boolean
  /** Position in the grid; staggers the entrance animation. */
  index?: number
  /** Shown for the category audience: pick which category to count. */
  categories?: string[]
  category?: string
}>()

defineEmits<{ select: []; 'update:category': [value: string] }>()

const number = new Intl.NumberFormat('en-US')
const pct = (part: number, whole: number) => (whole ? Math.round((part / whole) * 100) : 0)
</script>

<template>
  <article class="card" :class="{ selected }" :style="{ '--i': index ?? 0 }">
    <h3>
      <!-- Stretched button: its ::after covers the whole card, so the card is one click target. -->
      <button
        type="button"
        class="main"
        :aria-pressed="selected"
        :aria-describedby="`aud-${audience.id}-desc`"
        @click="$emit('select')"
      >
        {{ audience.name }}
      </button>
    </h3>
    <p class="size">{{ number.format(audience.size) }} <small>customers</small></p>
    <p :id="`aud-${audience.id}-desc`" class="desc">{{ audience.description }}</p>

    <label v-if="categories?.length" class="category">
      Category
      <select :value="category" @change="$emit('update:category', ($event.target as HTMLSelectElement).value)">
        <option v-for="c in categories" :key="c" :value="c">{{ c }}</option>
      </select>
    </label>

    <dl class="reach">
      <div>
        <dt>Email</dt>
        <dd>{{ number.format(audience.email_eligible) }} <small>{{ pct(audience.email_eligible, audience.size) }}%</small></dd>
      </div>
      <div>
        <dt>SMS</dt>
        <dd>{{ number.format(audience.sms_eligible) }} <small>{{ pct(audience.sms_eligible, audience.size) }}%</small></dd>
      </div>
    </dl>

    <p class="cue" aria-hidden="true">
      <template v-if="selected">
        <svg viewBox="0 0 16 16"><path d="m3.5 8.5 3 3 6-7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
        Showing in table below · click to clear
      </template>
      <template v-else>
        Show customers
        <svg class="arrow" viewBox="0 0 16 16"><path d="M8 3v10m-4-4 4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </template>
    </p>
  </article>
</template>

<style scoped>
.card {
  position: relative;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  transition: border-color 0.15s, box-shadow 0.2s, transform 0.2s, background-color 0.2s;
  animation: enter 0.3s ease-out backwards; /* not "both": would pin transform and block the hover lift */
  animation-delay: calc(var(--i) * 50ms);
}
.card:hover {
  border-color: var(--accent);
  transform: translateY(-2px);
  box-shadow: 0 14px 32px rgb(32 77 60 / 0.14);
}
.card:has(.main:focus-visible) {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.card.selected {
  border-color: var(--accent);
  background: var(--selected-surface);
  box-shadow: 0 0 0 1px var(--accent), var(--shadow);
}
h3 {
  margin: 0;
  padding: 16px 18px 0;
}
.main {
  padding: 0;
  border: 0;
  background: none;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
  text-align: left;
  cursor: pointer;
}
.main:focus-visible {
  outline: none; /* drawn on the card instead */
}
.main::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
}
.size {
  margin: 4px 18px 6px;
  font-size: 28px;
  font-weight: 600;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}
.size small {
  font-size: 13px;
  font-weight: 500;
  letter-spacing: 0;
  color: var(--text-muted);
}
.desc {
  margin: 0 18px 10px;
  font-size: 13px;
  color: var(--text-muted);
}
/* Sits above the stretched button so it stays usable. */
.category {
  position: relative;
  z-index: 1;
  display: grid;
  gap: 4px;
  margin: 0 18px 10px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}
.category select {
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  text-transform: capitalize;
  cursor: pointer;
}
.reach {
  display: flex;
  gap: 8px;
  margin: auto 0 0;
  padding: 10px 18px;
  border-top: 1px solid var(--border);
}
.reach div {
  flex: 1;
}
.reach dt {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}
.reach dd {
  margin: 0;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.reach small {
  font-weight: 400;
  color: var(--text-muted);
}
.cue {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 10px 18px;
  border-top: 1px solid var(--border);
  border-radius: 0 0 var(--radius) var(--radius);
  font-size: 13px;
  font-weight: 600;
  color: var(--accent);
  background: var(--surface-muted);
  transition: background-color 0.2s;
}
.cue svg {
  width: 16px;
  height: 16px;
}
.card:hover .cue {
  background: var(--accent-soft);
}
.card:hover .arrow {
  animation: nudge 0.6s ease-in-out 2;
}
.selected .cue {
  background: var(--accent-soft);
}
@keyframes enter {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
}
@keyframes nudge {
  50% {
    transform: translateY(3px);
  }
}
</style>
