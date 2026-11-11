<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
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

const emit = defineEmits<{ select: [alternative: AudienceAlternative | null] }>()

const brief = ref('')
const loading = ref(false)
const stage = ref(0)
const error = ref('')
const errorWarnings = ref<string[]>([])
const result = ref<SuggestionResponse | null>(null)
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

const selected = computed(() => result.value?.alternatives.find((a) => a.id === selectedId.value) ?? null)

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
    const res = await suggestAudiences(brief.value.trim(), controller.signal)
    result.value = res
    selectedId.value = ''
    emit('select', null)
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
  emit('select', a)
}

onUnmounted(() => {
  controller?.abort()
  clearInterval(timer)
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
          {{ result.message ? result.message + ' ' : '' }}Choose an audience to continue.
        </p>
        <ul v-if="result.warnings.length" class="warnings">
          <li v-for="w in result.warnings" :key="w">{{ w }}</li>
        </ul>
      <div class="alts" :class="{ stale: loading }" role="radiogroup" aria-label="Audience alternatives">
        <AlternativeCard
          v-for="a in result.alternatives"
          :key="a.id"
          :alternative="a"
          :total="result.total_customers"
          :selected="a.id === selectedId"
          :badge="badges[a.id]"
          @select="choose(a)"
          @preview="openPreview(a)"
        />
      </div>
      </template>
      <p v-if="selected" class="chosen" role="status">
        Selected <strong>{{ selected.name }}</strong>:
        {{ selected.counts.email_eligible.toLocaleString() }} reachable by email,
        {{ selected.counts.sms_eligible.toLocaleString() }} by SMS.
      </p>
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
  gap: 14px;
}
.alts.stale {
  opacity: 0.5;
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
.chosen {
  margin: 4px 0 0;
  padding: 10px 14px;
  border-radius: 8px;
  background: var(--accent-soft);
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
