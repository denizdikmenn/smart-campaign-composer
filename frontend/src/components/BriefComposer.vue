<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { previewCustomers, suggestAudiences, SuggestError, type AudienceAlternative, type PreviewCustomer, type SuggestionResponse } from '../api'
import AlternativeCard from './AlternativeCard.vue'
import PreviewSheet from './PreviewSheet.vue'

const MIN = 10
const MAX = 2000
const EXAMPLES = [
  "Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but haven't purchased since. Keep the message short and friendly.",
  'Win back customers who bought electronics before but have not ordered in the last 3 months.',
  'Welcome offer for new sign-ups in Ankara or Izmir who have not bought anything yet.',
]
const STAGES = ['Reading your brief…', 'Designing audience alternatives…', 'Counting matching customers…']

const emit = defineEmits<{
  /** brief is the text that produced the selected alternative. */
  select: [alternative: AudienceAlternative | null, brief: string]
  /** A response arrived; true when it contains selectable options. */
  generated: [hasOptions: boolean]
}>()

const brief = ref('')
const loading = ref(false)
const stage = ref(0)
const error = ref('')
const errorWarnings = ref<string[]>([])
const result = ref<SuggestionResponse | null>(null)
const resultBrief = ref('') // brief the current result was generated from
const selectedId = ref('')

let controller: AbortController | null = null
let timer: ReturnType<typeof setInterval> | undefined

const length = computed(() => brief.value.trim().length)
const valid = computed(() => length.value >= MIN && length.value <= MAX)
const outcome = computed(() => result.value?.outcome ?? null)
const selectable = computed(() => (result.value?.alternatives ?? []).filter((a) => !a.empty))
const allEmpty = computed(() => outcome.value === 'supported' && selectable.value.length === 0)
const textarea = ref<HTMLTextAreaElement | null>(null)
function refine() {
  textarea.value?.focus()
}

const badges = computed(() => {
  const alts = selectable.value
  if (alts.length < 2) return {} as Record<string, string>
  const by = (f: (a: AudienceAlternative) => number, cmp: (x: number, y: number) => boolean) =>
    alts.reduce((best, a) => (cmp(f(a), f(best)) ? a : best))
  const widest = by((a) => a.counts.total, (x, y) => x > y)
  const narrow = by((a) => a.counts.total, (x, y) => x < y)
  const out: Record<string, string> = {}
  if (widest.id !== narrow.id) {
    if (!widest.primary) out[widest.id] = 'Widest reach'
    if (!narrow.primary) out[narrow.id] = 'Most focused'
  }
  return out
})

function stop() {
  clearInterval(timer)
  loading.value = false
}

async function generate() {
  if (!valid.value || loading.value) return
  controller?.abort()
  controller = new AbortController()
  error.value = ''
  errorWarnings.value = []
  stage.value = 0
  loading.value = true
  timer = setInterval(() => (stage.value = Math.min(stage.value + 1, STAGES.length - 1)), 2500)
  try {
    const text = brief.value.trim()
    const res = await suggestAudiences(text, controller.signal)
    result.value = res
    resultBrief.value = text
    selectedId.value = ''
    emit('select', null, text)
    emit('generated', res.outcome === 'supported' && res.alternatives.some((a) => !a.empty))
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    error.value = e instanceof Error ? e.message : 'Something went wrong'
    if (e instanceof SuggestError) errorWarnings.value = e.warnings
  } finally {
    stop()
  }
}

function cancel() {
  controller?.abort()
  stop()
}

// --- customer preview sheet ---
const previewFor = ref<AudienceAlternative | null>(null)
const previewRows = ref<PreviewCustomer[]>([])
const previewLoading = ref(false)
const previewError = ref('')
let previewCtl: AbortController | null = null

async function openPreview(a: AudienceAlternative) {
  previewCtl?.abort()
  previewCtl = new AbortController()
  previewFor.value = a
  previewRows.value = []
  previewError.value = ''
  previewLoading.value = true
  try {
    previewRows.value = await previewCustomers(a, previewCtl.signal)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    previewError.value = e instanceof Error ? e.message : 'Something went wrong'
  } finally {
    previewLoading.value = false
  }
}

function closePreview() {
  previewCtl?.abort()
  previewFor.value = null
}

function choose(a: AudienceAlternative) {
  selectedId.value = a.id
  emit('select', a, resultBrief.value)
}

// --- option row / slider ---
// Recommended option sits in the middle of three (the "featured" position).
const ordered = computed(() => {
  const alts = result.value?.alternatives ?? []
  const rec = alts.findIndex((a) => a.primary)
  if (alts.length !== 3 || rec < 0 || rec === 1) return alts
  const rest = alts.filter((_, i) => i !== rec)
  return [rest[0]!, alts[rec]!, rest[1]!]
})
const single = computed(() => !allEmpty.value && ordered.value.length === 1)
const resultKey = ref(0)
const track = ref<HTMLElement | null>(null)
const canScroll = ref(false) // options don't all fit: show slider controls
const active = ref(0) // option closest to the slider centre

function cards() {
  return Array.from(track.value?.children ?? []) as HTMLElement[]
}

function measure() {
  const el = track.value
  if (!el) return
  // Layout-based, not scrollWidth: scrollWidth includes the scaled-up recommended
  // card's transform, which would wrongly switch a fitting row into slider mode.
  const last = cards().at(-1)
  const padRight = parseFloat(getComputedStyle(el).paddingRight) || 0
  canScroll.value = !!last && last.offsetLeft + last.offsetWidth + padRight > el.clientWidth + 1
  const mid = el.scrollLeft + el.clientWidth / 2
  const dist = cards().map((c) => Math.abs(c.offsetLeft + c.offsetWidth / 2 - mid))
  active.value = dist.length ? dist.indexOf(Math.min(...dist)) : 0
}

function go(i: number) {
  const el = track.value
  const card = cards()[i]
  if (!el || !card) return
  el.scrollTo({ left: card.offsetLeft - (el.clientWidth - card.offsetWidth) / 2 })
}

// On each new response: replay the entrance and start the slider on the recommended option.
watch(result, async (r) => {
  if (!r) return
  resultKey.value++
  await nextTick()
  measure()
  const rec = ordered.value.findIndex((a) => a.primary)
  if (canScroll.value && rec > 0) {
    const el = track.value
    if (el) el.style.scrollBehavior = 'auto' // jump, don't animate, on first show
    go(rec)
    if (el) el.style.scrollBehavior = ''
    measure()
  }
})

window.addEventListener('resize', measure)

onUnmounted(() => {
  controller?.abort()
  clearInterval(timer)
  window.removeEventListener('resize', measure)
})
</script>

<template>
  <section class="composer" aria-label="Campaign brief">
    <label for="brief" class="label">Describe your campaign</label>
    <textarea
      id="brief"
      ref="textarea"
      v-model="brief"
      rows="4"
      :maxlength="MAX"
      :disabled="loading"
      placeholder="Who do you want to reach, and why? Mention city, recent activity, products, spend…"
      @keydown.ctrl.enter="generate"
      @keydown.meta.enter="generate"
    />
    <div class="row">
      <span class="count" :class="{ low: length > 0 && length < MIN }">
        {{ length }}/{{ MAX }}<template v-if="length > 0 && length < MIN"> · add a few more words</template>
      </span>
      <span class="actions">
        <button v-if="loading" type="button" class="ghost" @click="cancel">Cancel</button>
        <button type="button" class="primary" :disabled="!valid || loading" @click="generate">
          {{ loading ? 'Generating…' : result ? 'Regenerate' : 'Suggest audiences' }}
        </button>
      </span>
    </div>

    <div v-if="!brief && !result" class="examples">
      <span>Try an example:</span>
      <button v-for="ex in EXAMPLES" :key="ex" type="button" class="example" @click="brief = ex">{{ ex }}</button>
    </div>

    <p v-if="loading" class="status" role="status">{{ STAGES[stage] }}</p>

    <div v-if="error" class="alert" role="alert">
      <div>
        <strong>{{ error }}</strong>
        <ul v-if="errorWarnings.length">
          <li v-for="w in errorWarnings" :key="w">{{ w }}</li>
        </ul>
      </div>
      <button type="button" class="ghost" @click="generate">Retry</button>
    </div>

    <div v-if="loading && !result" class="alts" aria-hidden="true">
      <div v-for="n in 3" :key="n" class="skeleton" />
    </div>

    <div v-if="result" class="results" :aria-busy="loading">
      <p v-if="result.source === 'mock'" class="notice">
        Demo mode: no ANTHROPIC_API_KEY is configured, so these alternatives come from a simple built-in generator, not Claude.
      </p>

      <!-- Needs clarification: explain what's missing, keep the brief editable. -->
      <div v-if="outcome === 'needs_clarification'" class="outcome clarify" role="status">
        <h3>We need a bit more detail</h3>
        <p>{{ result.message }}</p>
        <ul v-if="result.questions.length">
          <li v-for="q in result.questions" :key="q">{{ q }}</li>
        </ul>
        <button type="button" class="ghost" @click="refine">Refine the brief</button>
      </div>

      <!-- Unsupported: say which data is missing, offer supported alternatives. -->
      <div v-else-if="outcome === 'unsupported'" class="outcome unsupported" role="status">
        <h3>We can't build this audience accurately</h3>
        <p>{{ result.message }}</p>
        <template v-if="result.unsupported_requirements.length">
          <p class="sub">Not available in our customer data:</p>
          <ul>
            <li v-for="u in result.unsupported_requirements" :key="u">{{ u }}</li>
          </ul>
        </template>
        <template v-if="result.suggestions.length">
          <p class="sub">You could target instead:</p>
          <ul>
            <li v-for="s in result.suggestions" :key="s">{{ s }}</li>
          </ul>
        </template>
        <button type="button" class="ghost" @click="refine">Edit the brief</button>
      </div>

      <!-- Invalid: the proposals did not pass backend checks. -->
      <div v-else-if="outcome === 'invalid'" class="outcome unsupported" role="status">
        <h3>We couldn't build a reliable audience</h3>
        <p>{{ result.message }}</p>
        <ul v-if="result.warnings.length">
          <li v-for="w in result.warnings" :key="w">{{ w }}</li>
        </ul>
        <button type="button" class="ghost" @click="refine">Edit the brief</button>
      </div>

      <template v-else>
        <p v-if="allEmpty" class="outcome empty" role="status">
          No customers match these targeting conditions. The rules are valid; try adjusting the timeframe or thresholds.
        </p>
        <p v-else class="meta" role="status">
          {{ result.message ? result.message + ' ' : '' }}{{ ordered.length > 1 ? 'Choose an audience to continue.' : '' }}
        </p>
        <!-- One option is a real result, not a missing render: say why and how to get variants. -->
        <p v-if="single" class="single">
          <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.4" /><path d="M8 7.2v3.6M8 5.2v.1" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg>
          <span>
            <strong>One audience matches your brief exactly.</strong>
            It fixes every targeting detail, so there was nothing to vary without changing what you asked for. To compare
            options, leave something open, for example “in the last 30 to 60 days”, then regenerate.
          </span>
        </p>
        <ul v-if="result.warnings.length" class="warnings">
          <li v-for="w in result.warnings" :key="w">{{ w }}</li>
        </ul>
        <div class="slider" :class="{ scrollable: canScroll }">
          <!-- Keyed per response so the cards replay their entrance animation. -->
          <div
            :key="resultKey"
            ref="track"
            class="alts track"
            :class="[`count-${ordered.length}`, { stale: loading, ghosted: single }]"
            role="radiogroup"
            aria-label="Audience alternatives"
            @scroll.passive="measure"
          >
            <!-- Single result: static placeholders keep the 3-card layout. Not skeletons (no shimmer),
                 since nothing more is loading. Decorative; the note below carries the text for AT. -->
            <div v-if="single" class="ghost" aria-hidden="true">
              <span class="ghost-icon">
                <svg viewBox="0 0 16 16"><path d="M3 8h10" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg>
              </span>
              <p class="ghost-title">No broader option</p>
              <p>Your brief fixes every targeting detail, so widening it would change what you asked for.</p>
            </div>
            <AlternativeCard
              v-for="(a, i) in ordered"
              :key="a.id"
              :alternative="a"
              :index="single ? 1 : i"
              :total="result.total_customers"
              :selected="a.id === selectedId"
              :badge="badges[a.id]"
              @select="choose(a)"
              @preview="openPreview(a)"
            />
            <div v-if="single" class="ghost" aria-hidden="true">
              <span class="ghost-icon">
                <svg viewBox="0 0 16 16"><path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg>
              </span>
              <p class="ghost-title">Want options to compare?</p>
              <p>Leave something open, such as “in the last 30 to 60 days”, and regenerate.</p>
            </div>
          </div>
          <div v-if="canScroll" class="controls">
            <button type="button" class="nav" aria-label="Previous option" :disabled="active === 0" @click="go(active - 1)">
              <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m10 3-5 5 5 5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
            <span class="dots">
              <button
                v-for="(a, i) in ordered"
                :key="a.id"
                type="button"
                class="dot"
                :class="{ on: i === active }"
                :aria-label="`Show option ${i + 1}: ${a.name}`"
                :aria-current="i === active ? 'true' : undefined"
                @click="go(i)"
              />
            </span>
            <button type="button" class="nav" aria-label="Next option" :disabled="active === ordered.length - 1" @click="go(active + 1)">
              <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 3 5 5-5 5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
          </div>
        </div>
      </template>
    </div>
    <PreviewSheet
      :alternative="previewFor"
      :customers="previewRows"
      :loading="previewLoading"
      :error="previewError"
      :selected="!!previewFor && previewFor.id === selectedId"
      @close="closePreview"
      @select="previewFor && choose(previewFor)"
    />
  </section>
</template>

<style scoped>
.composer {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 18px;
  margin-bottom: 24px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
}
.label {
  font-weight: 600;
}
textarea {
  width: 100%;
  padding: 10px 12px;
  font: inherit;
  color: inherit;
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: 8px;
  resize: vertical;
  box-sizing: border-box;
}
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.count {
  font-size: 12px;
  color: var(--text-muted);
}
.count.low {
  color: var(--danger);
}
.actions {
  display: flex;
  gap: 8px;
}
button {
  font: inherit;
  cursor: pointer;
}
.primary {
  padding: 8px 16px;
  color: #fff;
  background: var(--accent);
  border: 0;
  border-radius: 8px;
}
.primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.ghost {
  padding: 8px 14px;
  color: inherit;
  background: none;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.examples {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  font-size: 12px;
  color: var(--text-muted);
}
.example {
  max-width: 100%;
  padding: 4px 10px;
  font-size: 12px;
  text-align: left;
  color: inherit;
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: 999px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status,
.meta {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}
.notice {
  margin: 0;
  padding: 8px 12px;
  font-size: 13px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  background: var(--surface-muted);
}
.alert {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  border: 1px solid var(--danger);
  border-radius: 8px;
  color: var(--danger);
}
.alert ul,
.warnings {
  margin: 4px 0 0;
  padding-left: 18px;
  font-size: 12px;
}
.warnings {
  color: var(--text-muted);
}
.alts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 16px;
}
.alts.stale {
  opacity: 0.5;
  transition: opacity 0.2s;
}
/* Card row: all options side by side when they fit (easy comparison);
   otherwise a swipeable, snapping slider. */
.track {
  position: relative; /* offsetParent for the cards' offsetLeft */
  grid-template-columns: none;
  grid-auto-flow: column;
  grid-auto-columns: minmax(min(290px, 84%), 1fr);
  align-items: stretch;
  /* Room for hover lift and shadows, which overflow:auto would clip. */
  /* Room for the scaled-up recommended card, its glow and hover lift. */
  margin: -26px -14px -34px;
  padding: 26px 14px 38px;
  scroll-behavior: smooth;
  scrollbar-width: none;
}
.single {
  display: flex;
  gap: 10px;
  margin: 0 0 8px;
  padding: 12px 14px;
  font-size: 13px;
  color: var(--text-muted);
  border-radius: var(--radius-control);
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.single svg {
  flex: none;
  width: 16px;
  height: 16px;
  margin-top: 2px;
  color: var(--accent);
}
.single strong {
  color: var(--text);
}
/* Single result with placeholders: three equal columns that never overflow.
   The note is redundant with the placeholders on wide screens; visually hide it
   there (still read by screen readers, since the placeholders are aria-hidden). */
.track.ghosted {
  grid-auto-columns: minmax(0, 1fr);
}
.ghost {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 24px;
  text-align: center;
  border: 1.5px dashed #c9d5c4;
  border-radius: 20px;
  background: rgb(255 255 255 / 0.4);
  transform: scale(0.96);
  animation: ghost-in 0.45s ease-out backwards;
}
.ghost:last-child {
  animation-delay: 0.18s;
}
.ghost p {
  margin: 0;
  max-width: 240px;
  font-size: 13px;
  color: var(--text-muted);
}
.ghost .ghost-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}
.ghost-icon {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  margin-bottom: 4px;
  border-radius: 50%;
  color: var(--text-muted);
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.ghost-icon svg {
  width: 16px;
  height: 16px;
}
@keyframes ghost-in {
  from {
    opacity: 0;
    transform: scale(0.92);
  }
}
@media (min-width: 861px) {
  .single {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
}
@media (max-width: 860px) {
  .track.ghosted .ghost {
    display: none;
  }
}
/* One or two options: centred at a readable width instead of stretched. */
.track.count-1 {
  grid-auto-columns: minmax(0, 560px);
  justify-content: center;
}
.track.count-2 {
  grid-auto-columns: minmax(min(290px, 84%), 480px);
  justify-content: center;
}
/* Clip only in slider mode; otherwise shadows would be cut off. */
.scrollable .track {
  overflow-x: auto;
  scroll-snap-type: x mandatory;
}
.track::-webkit-scrollbar {
  display: none;
}
.track > * {
  scroll-snap-align: center;
}
.scrollable .track {
  mask-image: linear-gradient(to right, transparent, #000 18px, #000 calc(100% - 18px), transparent);
}
.controls {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  margin-top: 10px;
}
.nav {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  padding: 0;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 50%;
  transition: background-color 0.15s;
}
.nav svg {
  width: 16px;
  height: 16px;
}
.nav:hover:not(:disabled) {
  background: var(--surface-muted);
}
.nav:disabled {
  opacity: 0.35;
  cursor: default;
}
.dots {
  display: flex;
  gap: 4px;
}
.dot {
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  background: none;
  position: relative;
}
.dot::after {
  content: '';
  position: absolute;
  inset: 9px;
  border-radius: 99px;
  background: #b9c8b4;
  transition: inset 0.2s, background-color 0.2s;
}
.dot.on::after {
  inset: 9px 2px;
  background: var(--accent);
}
@media (prefers-reduced-motion: reduce) {
  .track {
    scroll-behavior: auto;
  }
}
.skeleton {
  height: 230px;
  border-radius: var(--radius);
  background: linear-gradient(90deg, var(--surface-muted), var(--border), var(--surface-muted));
  background-size: 200% 100%;
  animation: shimmer 1.4s infinite;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
.outcome {
  margin: 0;
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-left: 4px solid var(--accent);
  border-radius: 8px;
  background: var(--surface-muted);
}
.outcome.unsupported {
  border-left-color: var(--danger);
}
.outcome.empty {
  border-left-color: var(--text-muted);
}
.outcome h3 {
  margin: 0 0 6px;
  font-size: 15px;
}
.outcome p {
  margin: 0 0 6px;
}
.outcome .sub {
  margin-top: 8px;
  font-size: 13px;
  color: var(--text-muted);
}
.outcome ul {
  margin: 4px 0 10px;
  padding-left: 18px;
}
.results {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
</style>
