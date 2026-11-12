<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  applyIndividual,
  formatTokens,
  formatUSD,
  individualCandidates,
  individualEstimate,
  individualSample,
  languageName,
  stopIndividual,
  type Candidate,
  type SampleResult,
} from '../api'
import { useCampaign } from '../campaign'
import AiProgress from './AiProgress.vue'
import Avatar from './Avatar.vue'
import LangChip from './LangChip.vue'

const store = useCampaign()
const { view, approved } = store
const number = new Intl.NumberFormat('en-US')

const progress = computed(() => view.value.individual)
const running = computed(() => view.value.jobs.individual)
const candidates = ref<Candidate[]>([])
const customerId = ref(0)
const sample = ref<SampleResult | null>(null)
const estimate = ref<SampleResult | null>(null) // remaining work and cost, without a new sample
const confirming = ref(false)

onMounted(async () => {
  try {
    candidates.value = await individualCandidates(store.id)
    customerId.value = candidates.value.find((c) => c.has_history)?.id ?? candidates.value[0]?.id ?? 0
    if (progress.value.generated > 0) estimate.value = await individualEstimate(store.id)
  } catch (e) {
    store.error.value = e instanceof Error ? e.message : 'Could not load customers'
  }
})

// When a run finishes or stops, refresh what is still left to do.
watch(running, async (now, before) => {
  if (before && !now) estimate.value = await individualEstimate(store.id).catch(() => estimate.value)
})

async function generateSample() {
  const res = await store.act('Generating sample…', () => individualSample(store.id, customerId.value))
  if (res) {
    sample.value = res
    estimate.value = res
  }
}
async function apply() {
  confirming.value = false
  await store.act('Starting…', () => applyIndividual(store.id))
}
async function stop() {
  await store.act('Stopping…', () => stopIndividual(store.id))
}

const work = computed(() => estimate.value)
const done = computed(() => progress.value.generated + progress.value.failed + progress.value.fallback)
const pct = computed(() => (progress.value.total ? Math.round((done.value / progress.value.total) * 100) : 0))
const days = (n: number | null) => (n == null ? 'never' : `${n} days ago`)
</script>

<template>
  <div class="individual">
    <p class="muted">Try one customer first. Customers without history get the base draft.</p>

    <!-- 1. Sample -->
    <div v-if="!approved" class="row">
      <label class="pick">
        Sample customer
        <select v-model.number="customerId" :disabled="!candidates.length">
          <option v-for="c in candidates" :key="c.id" :value="c.id">
            {{ c.first_name }} · {{ c.city }} · {{ languageName(c.language) }} · {{ c.order_count }} orders{{ c.has_history ? '' : ' · no history' }}
          </option>
        </select>
      </label>
      <button type="button" class="btn" :disabled="!customerId || !!store.busy.value || running" :aria-busy="store.busy.value === 'Generating sample…'" @click="generateSample">
        {{ store.busy.value === 'Generating sample…' ? 'Generating' : 'Generate sample' }}
      </button>
    </div>

    <AiProgress
      v-if="store.busy.value === 'Generating sample…'"
      title="Claude is writing a sample message"
      detail="Using this customer's orders and cart history."
      :stages="['Reading history', 'Writing message']"
      :current="1"
      compact
    />

    <div v-if="sample" class="sample">
      <div class="box">
        <h3>Data sent to Claude</h3>
        <template v-if="sample.input">
          <p class="muted">
            {{ sample.input.first_name }}, {{ sample.input.city }} · {{ sample.input.order_count }} orders · last order
            {{ days(sample.input.days_since_last_order) }}
          </p>
          <ul class="muted">
            <li v-for="(o, i) in sample.input.recent_orders" :key="'o' + i">Ordered {{ o.category }} ({{ number.format(o.amount_try) }} TRY), {{ o.days_ago }} days ago</li>
            <li v-for="(c, i) in sample.input.recent_cart_additions" :key="'c' + i">Added {{ c.category }} to cart, {{ c.days_ago }} days ago</li>
          </ul>

        </template>
        <p v-else class="muted">Nothing: this customer has too little history, so no AI call was made.</p>
      </div>
      <div class="box">
        <h3>
          Result <span class="chip" :class="{ ok: sample.message.status === 'generated', bad: sample.message.status === 'failed' }">{{ sample.message.status }}</span>
          <LangChip :lang="sample.message.language" />
        </h3>
        <p v-if="sample.message.reason" class="muted">{{ sample.message.reason }}</p>
        <template v-if="sample.message.status === 'generated'">
          <p class="pre bubble">{{ sample.message.content.sms_body }}</p>
          <p><strong>{{ sample.message.content.email_subject }}</strong></p>
          <p class="pre body">{{ sample.message.content.email_body }}</p>
        </template>
      </div>
    </div>

    <!-- 2. Cost and confirmation -->
    <div v-if="work && !approved && !running && work.remaining > 0" class="box cost">
      <div>
        <p>
          <strong>{{ number.format(work.remaining) }} recipients</strong> · {{ number.format(work.estimate.calls) }} AI calls ·
          ~{{ formatTokens(work.estimate.input_tokens + work.estimate.output_tokens) }} tokens ·
          <strong>≈ {{ formatUSD(work.estimate.usd) }}</strong> <span class="muted">projected</span>
        </p>
        <p v-if="work.fallbacks" class="muted">{{ number.format(work.fallbacks) }} more get the base draft.</p>
      </div>
      <div v-if="confirming" class="confirm">
        <button type="button" class="btn" @click="confirming = false">Cancel</button>
        <button type="button" class="btn primary" :disabled="!!store.busy.value" @click="apply">Confirm: generate {{ number.format(work.remaining) }} messages</button>
      </div>
      <button v-else type="button" class="btn cta" :disabled="progress.generated === 0" :title="progress.generated === 0 ? 'Generate and review a sample first' : undefined" @click="confirming = true">
        {{ progress.applied ? 'Resume' : 'Apply to all' }}
      </button>
    </div>

    <!-- 3. Progress -->
    <AiProgress
      v-if="running"
      title="Claude is writing individual messages"
      :detail="`${number.format(done)} of ${number.format(progress.total)} recipients · ${pct}%`"
      :progress="progress.total ? done / progress.total : undefined"
    >
      <button type="button" class="btn" :disabled="!!store.busy.value" :aria-busy="store.busy.value === 'Stopping…'" @click="stop">Stop</button>
    </AiProgress>
    <div v-if="progress.applied || running" class="box">
      <p v-if="!running">
        <span v-if="progress.pending" class="state dirty">Stopped · {{ number.format(progress.pending) }} pending</span>
        <span v-else-if="progress.failed" class="state failed">Done with {{ number.format(progress.failed) }} failed</span>
        <span v-else class="state done">Individual messages ready</span>
      </p>
      <div v-if="!running" class="bar" role="progressbar" :aria-valuenow="pct" aria-valuemin="0" aria-valuemax="100"><span :style="{ width: pct + '%' }" /></div>
      <p class="muted">
        {{ number.format(progress.generated) }} generated · {{ number.format(progress.failed) }} failed ·
        {{ number.format(progress.fallback) }} base draft · {{ number.format(progress.pending) }} pending ·
        ≈ {{ formatUSD(view.cost.by_kind.individual?.usd) }} projected
      </p>
    </div>

    <!-- 4. Representative outputs -->
    <details v-if="progress.samples.length" class="outputs">
      <summary>Review generated messages ({{ progress.samples.length }} most recent)</summary>
      <ul>
        <li v-for="m in progress.samples" :key="m.customer_id" class="box">
          <p>
            <Avatar :name="m.first_name" :size="26" /> <strong>{{ m.first_name }}</strong> <LangChip :lang="m.language" />
            <span class="chip" :class="{ ok: m.status === 'generated', bad: m.status === 'failed' }">{{ m.status }}</span>
          </p>
          <p v-if="m.reason" class="muted">{{ m.reason }}</p>
          <p v-if="m.status === 'generated'" class="pre">{{ m.content.sms_body }}</p>
        </li>
      </ul>
    </details>
  </div>
</template>

<style scoped>
.individual {
  display: grid;
  gap: 12px;
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 10px;
}
.pick {
  display: grid;
  gap: 4px;
  font-size: 12px;
  color: var(--text-muted);
}
.sample {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 10px;
}
.sample .box {
  display: grid;
  gap: 6px;
  align-content: start;
}
.sample ul,
.outputs ul {
  margin: 0;
  padding-left: 18px;
}
.outputs ul {
  display: grid;
  gap: 8px;
  padding: 0;
  list-style: none;
  margin-top: 8px;
}
.bubble,
.body {
  padding: 8px 10px;
  border-radius: var(--radius-control);
  background: var(--surface);
  border: 1px solid var(--border);
  font-size: 13px;
}
.cost {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.cost > div:first-child {
  display: grid;
  gap: 4px;
}
.confirm {
  display: flex;
  gap: 8px;
}
.bar {
  height: 8px;
  margin: 8px 0;
  border-radius: 99px;
  background: var(--border);
  overflow: hidden;
}
.bar span {
  display: block;
  height: 100%;
  background: var(--accent);
  transition: width 0.4s;
}
</style>
