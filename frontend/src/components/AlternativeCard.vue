<script setup lang="ts">
import { computed } from 'vue'
import type { AudienceAlternative } from '../api'

const props = defineProps<{
  alternative: AudienceAlternative
  total: number
  selected: boolean
  /** "Widest reach" / "Most focused" style hint. */
  badge?: string
}>()
defineEmits<{ select: []; preview: [] }>()

const number = new Intl.NumberFormat('en-US')
const pct = (part: number, whole: number) => (whole ? Math.round((part / whole) * 100) : 0)
const empty = computed(() => props.alternative.empty || props.alternative.counts.total === 0)
const share = computed(() => pct(props.alternative.counts.total, props.total))
</script>

<template>
  <article
    class="card"
    :class="{ selected, empty, primary: alternative.primary }"
    role="radio"
    :aria-checked="selected"
    :aria-disabled="empty"
    :tabindex="empty ? -1 : 0"
    @click="!empty && $emit('select')"
    @keydown.enter.self.prevent="!empty && $emit('select')"
    @keydown.space.self.prevent="!empty && $emit('select')"
  >
    <div class="pick">
      <span v-if="!empty" class="dot" :class="{ on: selected }" aria-hidden="true" />
      <span class="name">{{ alternative.name }}</span>
      <span v-if="empty" class="badge muted">No matches</span>
      <span v-else-if="alternative.primary" class="badge main">Best match</span>
      <span v-else-if="badge" class="badge">{{ badge }}</span>
    </div>

    <p class="size">
      {{ number.format(alternative.counts.total) }}
      <small>{{ share }}% of all customers</small>
    </p>
    <p v-if="empty" class="warn">These rules are valid, but no customers match them today. Try a wider timeframe or a lower threshold.</p>

    <p class="reasoning">{{ alternative.reasoning }}</p>

    <ul class="conds" aria-label="Targeting conditions">
      <li v-for="c in alternative.conditions_text" :key="c">{{ c }}</li>
    </ul>

    <dl class="reach">
      <div>
        <dt>Email-eligible</dt>
        <dd>{{ number.format(alternative.counts.email_eligible) }}</dd>
        <div class="bar"><span :style="{ width: pct(alternative.counts.email_eligible, alternative.counts.total) + '%' }" /></div>
      </div>
      <div>
        <dt>SMS-eligible</dt>
        <dd>{{ number.format(alternative.counts.sms_eligible) }}</dd>
        <div class="bar"><span :style="{ width: pct(alternative.counts.sms_eligible, alternative.counts.total) + '%' }" /></div>
      </div>
    </dl>

    <button type="button" class="preview" @click.stop="$emit('preview')">Preview customers →</button>
  </article>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px 18px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
}
.card {
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s, opacity 0.15s, transform 0.15s;
}
.card:hover {
  border-color: var(--accent);
  transform: translateY(-1px);
}
.card:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
.card.primary {
  border-color: var(--accent);
  background: linear-gradient(180deg, var(--accent-soft), var(--surface) 55%);
  box-shadow: 0 0 0 1px var(--accent), var(--shadow);
}
.card:not(.primary) {
  padding: 14px 16px;
  opacity: 0.85;
}
.card:not(.primary) .size {
  font-size: 22px;
}
.card:not(.primary):hover,
.card:not(.primary).selected {
  opacity: 1;
}
.card.primary .size {
  font-size: 34px;
}
.dot {
  width: 14px;
  height: 14px;
  flex: none;
  border: 2px solid var(--text-muted);
  border-radius: 50%;
}
.dot.on {
  border-color: var(--accent);
  background: radial-gradient(var(--accent) 45%, transparent 50%);
}
.badge.main {
  background: var(--accent);
  color: #fff;
}
.preview {
  align-self: flex-start;
  padding: 4px 0;
  font: inherit;
  font-size: 13px;
  color: var(--accent);
  background: none;
  border: 0;
  cursor: pointer;
}
.preview:hover {
  text-decoration: underline;
}
.card.selected {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}
.card.empty {
  opacity: 0.7 !important;
  cursor: default;
  transform: none;
  border-style: dashed;
}
.badge.muted {
  background: var(--surface-muted);
  color: var(--text-muted);
}
.pick {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}
.name {
  font-weight: 600;
}
.badge {
  margin-left: auto;
  padding: 2px 8px;
  font-size: 11px;
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent);
  white-space: nowrap;
}
.size {
  margin: 0;
  font-size: 28px;
  font-weight: 600;
}
.size small {
  margin-left: 6px;
  font-size: 12px;
  font-weight: 400;
  color: var(--text-muted);
}
.warn {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}
.reasoning {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}
.conds {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.conds li {
  padding: 2px 8px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-muted);
}
.reach {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin: auto 0 0;
}
dt {
  font-size: 12px;
  color: var(--text-muted);
}
dd {
  margin: 0;
  font-weight: 600;
}
.bar {
  height: 4px;
  margin-top: 4px;
  border-radius: 2px;
  background: var(--surface-muted);
  overflow: hidden;
}
.bar span {
  display: block;
  height: 100%;
  background: var(--success);
}
</style>
