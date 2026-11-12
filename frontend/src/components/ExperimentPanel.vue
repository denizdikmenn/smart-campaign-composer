<script setup lang="ts">
import { computed, ref } from 'vue'
import { setVariants } from '../api'
import { ORIGIN_LABELS, useCampaign } from '../campaign'
import AiProgress from './AiProgress.vue'

/** A/B test: 2-3 drafts explicitly promoted to variants. */
const store = useCampaign()
const { campaign, drafts, current, approved, live } = store
const LABELS = ['A', 'B', 'C']

const stored = computed(() => campaign.value.variants.map((v) => v.draft_version))
// Before two variants exist, A is held locally (the backend stores 2-3).
const pendingA = ref(campaign.value.selected_version ?? current.value?.version ?? 0)
const slots = computed(() => (stored.value.length ? stored.value : pendingA.value ? [pendingA.value] : []))
const free = computed(() => drafts.value.filter((d) => !slots.value.includes(d.version)))
const canAdd = computed(() => slots.value.length < 3 && !approved.value)
const share = computed(() => Math.round(1000 / Math.max(slots.value.length, 2)) / 10)
const busy = computed(() => !!store.busy.value || live.active)

async function save(versions: number[]) {
  await store.act('Saving variants…', () => setVariants(store.id, versions))
}
function replace(i: number, version: number) {
  if (!stored.value.length) {
    pendingA.value = version
    return
  }
  const next = [...slots.value]
  next[i] = version
  void save(next)
}
function add(version: number) {
  if (version) void save([...slots.value, version])
}
function remove(i: number) {
  const next = slots.value.filter((_, j) => j !== i)
  if (next.length < 2) {
    if (!confirm('An A/B test needs two variants. Remove this one and pause the test? Drafts are kept.')) return
    pendingA.value = next[0] ?? 0
    void save([])
    return
  }
  void save(next)
}

// --- candidate: one Claude call, a different strategy, starts as a draft ---
const guidance = ref('')
const candidate = ref(0)
const writingCandidate = computed(() => live.active && live.intent === 'candidate')
const candidateDraft = computed(() => drafts.value.find((d) => d.version === candidate.value) ?? null)
async function generateCandidate() {
  const ref0 = slots.value[0] ?? current.value?.version
  if (!ref0) return
  candidate.value = 0
  const d = await store.generate(ref0, 'candidate', guidance.value)
  if (d) {
    candidate.value = d.version
    guidance.value = ''
  }
}
async function acceptCandidate() {
  const v = candidate.value
  await save([...slots.value, v])
  if (!store.error.value) candidate.value = 0
}
function editCandidate() {
  void store.selectDraft(candidate.value)
  store.step.value = 'content'
}
</script>

<template>
  <div class="ab">
    <ol class="slots">
      <li v-for="(version, i) in slots" :key="`${i}-${version}`" class="slot">
        <span class="label">Variant {{ LABELS[i] }}</span>
        <select :value="version" :disabled="approved || busy" :aria-label="`Draft for variant ${LABELS[i]}`" @change="replace(i, Number(($event.target as HTMLSelectElement).value))">
          <option :value="version">Draft {{ version }}</option>
          <option v-for="d in free" :key="d.version" :value="d.version">Draft {{ d.version }} · {{ ORIGIN_LABELS[d.origin] }}</option>
        </select>
        <span class="muted">{{ share }}%</span>
        <button v-if="!approved && stored.length" type="button" class="btn remove" :disabled="busy" @click="remove(i)">Remove</button>
      </li>
      <li v-if="canAdd && free.length" class="slot add">
        <span class="label">Variant {{ LABELS[slots.length] }}</span>
        <select :key="slots.join()" :disabled="busy" aria-label="Add an existing draft" @change="add(Number(($event.target as HTMLSelectElement).value))">
          <option value="0">Add an existing draft…</option>
          <option v-for="d in free" :key="d.version" :value="d.version">Draft {{ d.version }} · {{ ORIGIN_LABELS[d.origin] }}</option>
        </select>
      </li>
    </ol>
    <p class="muted">
      {{ stored.length ? 'Equal random split; each customer always gets the same variant on every channel.' : 'Add a second variant to start the test.' }}
    </p>

    <!-- New candidate -->
    <div v-if="canAdd" class="box candidate" :aria-busy="writingCandidate">
      <template v-if="writingCandidate">
        <AiProgress
          :title="`Claude is writing Draft ${store.nextVersion.value}`"
          detail="A different approach, so the A/B test compares something real."
          :stages="['SMS', 'Email', 'Translations']"
          :current="live.localizing.length ? 2 : live.fields.email_subject ? 1 : 0"
          compact
        >
          <button type="button" class="btn" @click="store.stopGeneration">Stop</button>
        </AiProgress>
        <p class="pre live">{{ live.fields.sms_body || '…' }}</p>
        <p v-if="live.fields.email_subject" class="live"><strong>{{ live.fields.email_subject }}</strong></p>
      </template>

      <template v-else-if="candidateDraft">
        <p><strong>Draft {{ candidateDraft.version }} is ready</strong> <span class="muted">· saved as a draft, not a variant yet</span></p>
        <p class="pre live">{{ candidateDraft.sms_body }}</p>
        <p class="live"><strong>{{ candidateDraft.email_subject }}</strong></p>
        <div class="row">
          <button type="button" class="btn primary" :disabled="busy" :aria-busy="store.busy.value === 'Saving variants…'" @click="acceptCandidate">
            {{ store.busy.value === 'Saving variants…' ? 'Adding' : `Add as Variant ${LABELS[slots.length]}` }}
          </button>
          <button type="button" class="btn" :disabled="busy" @click="editCandidate">Edit first</button>
          <button type="button" class="btn" :disabled="busy" @click="candidate = 0">Not now</button>
        </div>
      </template>

      <template v-else>
        <p><strong>Generate a candidate</strong> <span class="muted">· 1 AI call, same facts and offer, different angle</span></p>
        <textarea v-model="guidance" rows="2" maxlength="1000" :disabled="busy" placeholder="Optional direction, e.g. lead with urgency" />
        <div v-if="live.error && live.intent === 'candidate'" class="fail" role="alert"><span class="state failed">Failed</span><p class="msg">{{ live.error }}</p></div>
        <div class="row">
          <button type="button" class="btn primary" :disabled="busy || !slots.length" @click="generateCandidate">Generate candidate</button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ab {
  display: grid;
  gap: 10px;
}
.slots {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.slot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}
.slot.add {
  border-style: dashed;
}
.label {
  min-width: 70px;
  font-weight: 600;
}
.remove {
  margin-left: auto;
}
.candidate {
  display: grid;
  gap: 8px;
}
.candidate[aria-busy='true'] {
  border-color: var(--accent);
  background: var(--selected-surface);
}
.status {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}
.live {
  font-size: 13px;
}
.row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
