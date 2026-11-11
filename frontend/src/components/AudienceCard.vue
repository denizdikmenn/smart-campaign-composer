<script setup lang="ts">
import type { Audience } from '../api'

defineProps<{
  audience: Audience
  selected: boolean
  /** Shown for the category audience: pick which category to count. */
  categories?: string[]
  category?: string
}>()

defineEmits<{ select: []; 'update:category': [value: string] }>()

const number = new Intl.NumberFormat('en-US')
const pct = (part: number, whole: number) => (whole ? Math.round((part / whole) * 100) : 0)
</script>

<template>
  <article class="card" :class="{ selected }">
    <button class="main" :aria-pressed="selected" @click="$emit('select')">
      <h3>{{ audience.name }}</h3>
      <p class="size">{{ number.format(audience.size) }}</p>
      <p class="desc">{{ audience.description }}</p>
    </button>

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
  </article>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  transition: border-color 0.15s, box-shadow 0.15s;
}
.card.selected {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.main {
  flex: 1;
  padding: 16px 18px 8px;
  border: 0;
  border-radius: var(--radius) var(--radius) 0 0;
  background: none;
  text-align: left;
  cursor: pointer;
}
.main:hover {
  background: var(--surface-muted);
}
h3 {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
}
.size {
  margin: 4px 0 6px;
  font-size: 28px;
  font-weight: 700;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}
.desc {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}
.category {
  display: grid;
  gap: 4px;
  padding: 0 18px 8px;
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
}
.reach {
  display: flex;
  gap: 8px;
  margin: 0;
  padding: 10px 18px 14px;
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
</style>
