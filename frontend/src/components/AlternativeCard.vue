<script setup lang="ts">
import { computed } from 'vue'
import type { AudienceAlternative } from '../api'

const props = defineProps<{
  alternative: AudienceAlternative
  total: number
  selected: boolean
  /** "Widest reach" / "Most focused" style hint. */
  badge?: string
  /** Position in the row; staggers the entrance animation. */
  index?: number
}>()
defineEmits<{ select: []; preview: [] }>()

const number = new Intl.NumberFormat('en-US')
const pct = (part: number, whole: number) => (whole ? Math.round((part / whole) * 100) : 0)
const empty = computed(() => props.alternative.empty || props.alternative.counts.total === 0)
const share = computed(() => pct(props.alternative.counts.total, props.total))
</script>

<template>
  <!-- Root class is "recommended", not "primary": a parent's scoped .primary rule would leak onto this root. -->
  <article
    class="card"
    :class="{ selected, empty, recommended: alternative.primary }"
    :style="{ '--i': index ?? 0 }"
    role="radio"
    :aria-checked="selected"
    :aria-disabled="empty"
    :tabindex="empty ? -1 : 0"
    @click="!empty && $emit('select')"
    @keydown.enter.self.prevent="!empty && $emit('select')"
    @keydown.space.self.prevent="!empty && $emit('select')"
  >
    <div class="top">
      <span v-if="empty" class="badge muted">No matches</span>
      <span v-else-if="alternative.primary" class="badge rec">
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m8 1.8 1.9 3.9 4.3.6-3.1 3 .7 4.3L8 11.6l-3.8 2 .7-4.3-3.1-3 4.3-.6z" fill="currentColor" /></svg>
        Recommended
      </span>
      <span v-else-if="badge" class="badge">{{ badge }}</span>
      <span v-if="!empty" class="radio" :class="{ on: selected }" aria-hidden="true">
        <svg v-if="selected" viewBox="0 0 16 16"><path d="m4 8.5 2.5 2.5L12 5.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </span>
    </div>

    <h3 class="name">{{ alternative.name }}</h3>

    <p class="size">
      {{ number.format(alternative.counts.total) }}
      <small>people · {{ share }}% of customers</small>
    </p>
    <p v-if="empty" class="warn">These rules are valid, but no customers match them today. Try a wider timeframe or a lower threshold.</p>

    <p class="reasoning">{{ alternative.reasoning }}</p>

    <ul class="conds" aria-label="Targeting conditions">
      <li v-for="c in alternative.conditions_text" :key="c">{{ c }}</li>
    </ul>

    <dl class="reach">
      <div>
        <dt>Email</dt>
        <dd>{{ number.format(alternative.counts.email_eligible) }}</dd>
        <div class="bar" aria-hidden="true"><span :style="{ width: pct(alternative.counts.email_eligible, alternative.counts.total) + '%' }" /></div>
      </div>
      <div>
        <dt>SMS</dt>
        <dd>{{ number.format(alternative.counts.sms_eligible) }}</dd>
        <div class="bar" aria-hidden="true"><span :style="{ width: pct(alternative.counts.sms_eligible, alternative.counts.total) + '%' }" /></div>
      </div>
    </dl>

    <button type="button" class="preview" @click.stop="$emit('preview')">
      Preview customers
      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 8h10m-4-4 4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
  </article>
</template>

<style scoped>
/* Secondary options: frosted, light elevation. Text stays sharp; only the
   surface is translucent so options remain easy to compare. */
.card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 18px 20px;
  color: var(--text);
  background: rgb(255 255 255 / 0.62);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  border: 1px solid var(--border);
  border-radius: 20px;
  box-shadow: 0 2px 8px rgb(32 77 60 / 0.04);
  transform: scale(0.96); /* secondary options step back */
  cursor: pointer;
  transition:
    transform 0.22s cubic-bezier(0.2, 0.7, 0.2, 1),
    box-shadow 0.22s,
    border-color 0.18s,
    background-color 0.18s;
  animation: rise 0.45s cubic-bezier(0.2, 0.7, 0.2, 1) backwards;
  animation-delay: calc(var(--i) * 90ms);
}
.card:hover {
  background: var(--surface);
  border-color: #c4d2bf;
  transform: scale(0.97) translateY(-3px);
  box-shadow: var(--shadow);
}
.card:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 3px;
}

/* Recommended option: larger, opaque, gradient ring (pistachio -> forest,
   drawn with the padding-box/border-box trick so it follows the radius),
   a soft top sheen and a wide pistachio halo. */
.card.recommended {
  z-index: 1;
  --fill: var(--surface);
  border: 2px solid transparent;
  background:
    radial-gradient(120% 50% at 50% 0%, #f2f8de 0%, rgb(255 255 255 / 0) 70%) padding-box,
    linear-gradient(var(--fill), var(--fill)) padding-box,
    linear-gradient(140deg, #e6efc4 0%, #9cbf86 45%, #204d3c 100%) border-box;
  transform: scale(1.04);
  box-shadow:
    0 0 0 6px rgb(221 232 181 / 0.5),
    0 0 70px 10px rgb(203 222 150 / 0.55),
    0 30px 60px rgb(32 77 60 / 0.2);
}
/* One-time light sweep across the recommended card on entrance. */
.card.recommended::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
  background: linear-gradient(105deg, transparent 35%, rgb(255 255 255 / 0.75) 50%, transparent 65%) no-repeat;
  background-size: 250% 100%;
  background-position: 150% 0;
  animation: sheen 1.1s 0.55s ease-out both;
}
.card.recommended:hover {
  transform: scale(1.04) translateY(-4px);
  box-shadow:
    0 0 0 6px rgb(221 232 181 / 0.65),
    0 0 84px 14px rgb(203 222 150 / 0.65),
    0 36px 70px rgb(32 77 60 / 0.24);
}

/* A selected secondary option comes forward to full size. */
.card.selected {
  transform: scale(1);
  background: var(--selected-surface);
  border-color: var(--accent);
  box-shadow:
    0 0 0 1px var(--accent),
    var(--shadow);
}
.card.selected:hover {
  transform: translateY(-3px);
}
/* Selected recommended: keep the ring and halo, tint the fill. */
.card.recommended.selected {
  transform: scale(1.04);
  --fill: var(--selected-surface);
  border-color: transparent;
  /* Re-declared: the later .card.selected rule would otherwise flatten it. */
  background:
    linear-gradient(var(--fill), var(--fill)) padding-box,
    linear-gradient(140deg, #e6efc4 0%, #9cbf86 45%, #204d3c 100%) border-box;
  box-shadow:
    0 0 0 6px rgb(221 232 181 / 0.7),
    0 0 70px 10px rgb(203 222 150 / 0.6),
    0 30px 60px rgb(32 77 60 / 0.2);
}
.card.recommended.selected:hover {
  transform: scale(1.04) translateY(-4px);
}
.card.empty {
  cursor: default;
  opacity: 0.75;
  transform: none;
  border-style: dashed;
  box-shadow: none;
}

.top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 24px;
}
.badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  font-size: 11px;
  font-weight: 600;
  border-radius: 999px;
  color: var(--text);
  background: var(--surface-muted);
  border: 1px solid var(--border);
  white-space: nowrap;
}
.badge.rec {
  color: var(--accent);
  background: var(--accent-soft);
  border-color: transparent;
}
.badge.rec svg {
  width: 12px;
  height: 12px;
}
.badge.muted {
  color: var(--text-muted);
}
.radio {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  margin-left: auto;
  border: 2px solid #b9c8b4;
  border-radius: 50%;
  color: #fff;
  transition: background-color 0.18s, border-color 0.18s;
}
.radio.on {
  border-color: var(--accent);
  background: var(--accent);
}
.radio svg {
  width: 14px;
  height: 14px;
}

.name {
  margin: 0;
  font-size: 16px;
  line-height: 1.4;
  font-weight: 600;
}
.size {
  margin: 0;
  font-size: 30px;
  font-weight: 600;
  line-height: 1.1;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}
.recommended .size {
  font-size: 36px;
}
.size small {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0;
  color: var(--text-muted);
}
.warn,
.reasoning {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
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
  padding: 3px 10px;
  font-size: 12px;
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
}
.reach {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
  margin: auto 0 0;
  padding-top: 12px;
  border-top: 1px solid var(--border);
}
dt {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}
dd {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.bar {
  height: 4px;
  margin-top: 6px;
  border-radius: 2px;
  background: var(--border);
  overflow: hidden;
}
.bar span {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--success);
  transition: width 0.4s ease-out;
}
.preview {
  position: relative;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  align-self: flex-start;
  padding: 6px 0;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  color: var(--accent);
  background: none;
  border: 0;
  cursor: pointer;
}
.preview svg {
  width: 14px;
  height: 14px;
  transition: transform 0.15s;
}
.preview:hover svg {
  transform: translateX(3px);
}

/* A lone option has nothing to stand out from: no scaling either way. */
.card:only-child,
.card.selected:only-child,
.card.recommended:only-child,
.card.recommended.selected:only-child {
  transform: none;
}
.card:only-child:hover,
.card.recommended:only-child:hover {
  transform: translateY(-3px);
}

@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(16px) scale(0.94);
  }
}
@keyframes sheen {
  from {
    background-position: 150% 0;
  }
  to {
    background-position: -50% 0;
  }
}
</style>
