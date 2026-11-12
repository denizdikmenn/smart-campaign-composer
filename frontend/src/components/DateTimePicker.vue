<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'

/**
 * Calendar popover with a time row (shadcn-style). The value is a local
 * "YYYY-MM-DDTHH:mm" string, or '' when nothing is chosen; `min` has the
 * same format and disables earlier days.
 */
const props = defineProps<{ modelValue: string; min: string; disabled?: boolean; label?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const pad = (n: number) => String(n).padStart(2, '0')
const dayKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
const parse = (v: string) => (v ? new Date(v) : null)

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const grid = ref<HTMLElement | null>(null)

const selected = computed(() => parse(props.modelValue))
const minDate = computed(() => parse(props.min) ?? new Date())
const today = dayKey(new Date())

const view = ref(new Date((selected.value ?? minDate.value).getFullYear(), (selected.value ?? minDate.value).getMonth(), 1))
const focusKey = ref(dayKey(selected.value ?? minDate.value))

const monthLabel = computed(() => view.value.toLocaleDateString('en-GB', { month: 'long', year: 'numeric' }))
const weekdays = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']

interface Cell {
  date: Date
  key: string
  day: number
  outside: boolean
  disabled: boolean
}
const cells = computed<Cell[]>(() => {
  const first = new Date(view.value.getFullYear(), view.value.getMonth(), 1)
  const lead = (first.getDay() + 6) % 7 // Monday first
  const startOfMin = dayKey(minDate.value)
  return Array.from({ length: 42 }, (_, i) => {
    const date = new Date(first.getFullYear(), first.getMonth(), 1 - lead + i)
    const key = dayKey(date)
    return { date, key, day: date.getDate(), outside: date.getMonth() !== first.getMonth(), disabled: key < startOfMin }
  })
})
const canGoBack = computed(() => dayKey(new Date(view.value.getFullYear(), view.value.getMonth(), 0)) >= dayKey(minDate.value))

const shift = (n: number) => (view.value = new Date(view.value.getFullYear(), view.value.getMonth() + n, 1))

const hour = computed(() => (selected.value ? selected.value.getHours() : null))
const minute = computed(() => (selected.value ? selected.value.getMinutes() : null))
const hours = Array.from({ length: 24 }, (_, i) => i)
const minutes = Array.from({ length: 12 }, (_, i) => i * 5)

function commit(d: Date) {
  emit('update:modelValue', `${dayKey(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}`)
}

/** Default time for a freshly picked day: 09:00, or the next whole hour if that is too early. */
function defaultTime(day: Date) {
  const d = new Date(day.getFullYear(), day.getMonth(), day.getDate(), 9, 0)
  while (d < minDate.value) d.setHours(d.getHours() + 1, 0)
  return d
}

function pick(c: Cell) {
  if (c.disabled) return
  focusKey.value = c.key
  const cur = selected.value
  const d = cur ? new Date(c.date.getFullYear(), c.date.getMonth(), c.date.getDate(), cur.getHours(), cur.getMinutes()) : defaultTime(c.date)
  commit(d)
  if (c.outside) view.value = new Date(c.date.getFullYear(), c.date.getMonth(), 1)
}

function setTime(h: number, m: number) {
  const base = selected.value ?? defaultTime(minDate.value)
  commit(new Date(base.getFullYear(), base.getMonth(), base.getDate(), h, m))
}

const presets = computed(() => {
  const now = new Date()
  const inHour = new Date(now.getFullYear(), now.getMonth(), now.getDate(), now.getHours() + 1, 0)
  const tomorrow = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1, 9, 0)
  const monday = new Date(now.getFullYear(), now.getMonth(), now.getDate() + ((8 - now.getDay()) % 7 || 7), 9, 0)
  return [
    { label: 'In 1 hour', date: inHour },
    { label: 'Tomorrow 09:00', date: tomorrow },
    { label: 'Next Monday 09:00', date: monday },
  ].filter((p) => p.date >= minDate.value)
})
function usePreset(d: Date) {
  commit(d)
  view.value = new Date(d.getFullYear(), d.getMonth(), 1)
  focusKey.value = dayKey(d)
}

const text = computed(() => {
  const d = selected.value
  if (!d) return ''
  return d.toLocaleString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' })
})

// --- popover behaviour -----------------------------------------------------------------
function onDocDown(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) close()
}
function close() {
  open.value = false
}
watch(open, async (v) => {
  if (v) {
    const base = selected.value ?? minDate.value
    view.value = new Date(base.getFullYear(), base.getMonth(), 1)
    focusKey.value = dayKey(base)
    document.addEventListener('mousedown', onDocDown)
    await nextTick()
    grid.value?.querySelector<HTMLElement>('[tabindex="0"]')?.focus()
  } else {
    document.removeEventListener('mousedown', onDocDown)
  }
})
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocDown))

/** Arrow keys move through days, PageUp/PageDown through months, Escape closes. */
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    close()
    root.value?.querySelector<HTMLElement>('.trigger')?.focus()
    return
  }
  const step: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }
  let next: Date | null = null
  const [y = 0, m = 1, d = 1] = focusKey.value.split('-').map(Number)
  if (e.key in step) next = new Date(y, m - 1, d + (step[e.key] ?? 0))
  else if (e.key === 'PageUp') next = new Date(y, m - 2, d)
  else if (e.key === 'PageDown') next = new Date(y, m, d)
  else if (e.key === 'Home') next = new Date(y, m - 1, d - ((new Date(y, m - 1, d).getDay() + 6) % 7))
  else if (e.key === 'End') next = new Date(y, m - 1, d + (6 - ((new Date(y, m - 1, d).getDay() + 6) % 7)))
  if (!next) return
  e.preventDefault()
  if (dayKey(next) < dayKey(minDate.value)) return
  focusKey.value = dayKey(next)
  view.value = new Date(next.getFullYear(), next.getMonth(), 1)
  void nextTick(() => grid.value?.querySelector<HTMLElement>('[tabindex="0"]')?.focus())
}
</script>

<template>
  <div ref="root" class="dtp" @keydown="onKey">
    <button
      type="button"
      class="trigger"
      :class="{ empty: !text }"
      :disabled="disabled"
      :aria-expanded="open"
      aria-haspopup="dialog"
      :aria-label="label ?? 'Choose date and time'"
      @click="open = !open"
    >
      <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="3" y="4" width="18" height="18" rx="3" />
        <path d="M16 2v4M8 2v4M3 10h18" />
      </svg>
      <span>{{ text || 'Pick a date and time' }}</span>
    </button>

    <Transition name="pop">
      <div v-if="open" class="pop" role="dialog" aria-label="Choose date and time">
        <div class="head">
          <button type="button" class="nav" :disabled="!canGoBack" aria-label="Previous month" @click="shift(-1)">
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 18-6-6 6-6" /></svg>
          </button>
          <strong aria-live="polite">{{ monthLabel }}</strong>
          <button type="button" class="nav" aria-label="Next month" @click="shift(1)">
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 18 6-6-6-6" /></svg>
          </button>
        </div>

        <div class="week" aria-hidden="true">
          <span v-for="w in weekdays" :key="w">{{ w }}</span>
        </div>
        <div ref="grid" class="days" role="grid">
          <button
            v-for="c in cells"
            :key="c.key"
            type="button"
            class="day"
            :class="{ outside: c.outside, today: c.key === today, on: selected && c.key === dayKey(selected) }"
            :disabled="c.disabled"
            :tabindex="c.key === focusKey && !c.disabled ? 0 : -1"
            :aria-selected="!!selected && c.key === dayKey(selected)"
            :aria-label="c.date.toLocaleDateString('en-GB', { dateStyle: 'full' })"
            @click="pick(c)"
          >
            {{ c.day }}
          </button>
        </div>

        <div class="time">
          <span class="muted">Time</span>
          <select :value="hour ?? ''" aria-label="Hour" @change="setTime(Number(($event.target as HTMLSelectElement).value), minute ?? 0)">
            <option v-if="hour === null" value="" disabled>--</option>
            <option v-for="h in hours" :key="h" :value="h">{{ pad(h) }}</option>
          </select>
          <span aria-hidden="true">:</span>
          <select :value="minute ?? ''" aria-label="Minute" @change="setTime(hour ?? 9, Number(($event.target as HTMLSelectElement).value))">
            <option v-if="minute === null" value="" disabled>--</option>
            <option v-for="m in minutes" :key="m" :value="m">{{ pad(m) }}</option>
          </select>
        </div>

        <div v-if="presets.length" class="presets">
          <button v-for="p in presets" :key="p.label" type="button" @click="usePreset(p.date)">{{ p.label }}</button>
        </div>

        <div class="foot">
          <button type="button" class="btn primary" :disabled="!selected" @click="close">Done</button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.dtp {
  position: relative;
  display: inline-block;
}
.trigger {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 38px;
  padding: 6px 12px;
  font-weight: 500;
  text-align: left;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  cursor: pointer;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.trigger.empty {
  color: var(--text-muted);
}
.trigger:hover:not(:disabled),
.trigger[aria-expanded='true'] {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--selected-surface);
}
.trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.pop {
  position: absolute;
  z-index: 30;
  top: calc(100% + 8px);
  left: 0;
  width: 304px;
  max-width: calc(100vw - 32px);
  padding: 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 18px 44px rgb(32 77 60 / 0.16), 0 2px 6px rgb(32 77 60 / 0.06);
  transform-origin: top left;
}
.pop-enter-active,
.pop-leave-active {
  transition: opacity 0.12s ease, transform 0.12s ease;
}
.pop-enter-from,
.pop-leave-to {
  opacity: 0;
  transform: translateY(-4px) scale(0.98);
}
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.head strong {
  font-size: 14px;
}
.nav {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding: 0;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  cursor: pointer;
}
.nav:hover:not(:disabled) {
  background: var(--selected-surface);
}
.nav:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
.week,
.days {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 2px;
}
.week span {
  padding: 4px 0;
  font-size: 11px;
  font-weight: 600;
  text-align: center;
  color: var(--text-muted);
}
.day {
  aspect-ratio: 1;
  padding: 0;
  font-size: 13px;
  font-weight: 500;
  color: var(--text);
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  cursor: pointer;
  transition: background 0.1s;
}
.day:hover:not(:disabled):not(.on) {
  background: var(--selected-surface);
}
.day.outside {
  color: var(--text-muted);
  opacity: 0.55;
}
.day.today:not(.on) {
  border-color: var(--accent-soft);
  background: var(--surface-muted);
  font-weight: 600;
}
.day.on {
  color: #fff;
  background: var(--accent);
  font-weight: 600;
  opacity: 1;
}
.day:disabled {
  opacity: 0.28;
  cursor: not-allowed;
}
.time {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
}
.time .muted {
  margin-right: auto;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}
.time select {
  padding: 5px 8px;
  font-weight: 600;
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}
.presets {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 12px;
}
.presets button {
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 600;
  color: var(--accent);
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: 99px;
  cursor: pointer;
}
.presets button:hover {
  background: var(--accent-soft);
}
.foot {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
