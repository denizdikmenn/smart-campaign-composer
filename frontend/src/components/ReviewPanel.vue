<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { approveCampaign, CampaignError, formatTokens, formatUSD, languageName, reviewCampaign, type Issue, type Review } from '../api'
import { useCampaign } from '../campaign'

/**
 * Final review: the backend's consistency gate, who receives what (resolved
 * exactly as delivery will), AI cost, and the explicit approval.
 */
const store = useCampaign()
const { view, campaign, current, approved } = store
const number = new Intl.NumberFormat('en-US')

const review = ref<Review | null>(null)
const loading = ref(false)
const confirming = ref(false)
const approveIssues = ref<Issue[]>([])

const standard = computed(() => campaign.value.personalization === 'standard' && !campaign.value.variants.length)
const version = computed(() => campaign.value.selected_version ?? current.value?.version ?? 0)

// The review always reflects the current configuration: it runs on entry and
// again (debounced) after any change; a newer run supersedes an older one.
let ctl: AbortController | null = null
let timer: ReturnType<typeof setTimeout> | undefined
async function run() {
  if (approved.value) return
  if (!(await store.flushSave())) return
  ctl?.abort()
  ctl = new AbortController()
  loading.value = true
  approveIssues.value = []
  try {
    review.value = await reviewCampaign(store.id, standard.value ? version.value : 0, ctl.signal)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    store.error.value = e instanceof Error ? e.message : 'Review failed'
  } finally {
    if (!ctl.signal.aborted) loading.value = false
  }
}
watch(
  () => JSON.stringify([view.value.drafts, view.value.campaign, view.value.groups, view.value.individual]),
  () => {
    confirming.value = false
    clearTimeout(timer)
    timer = setTimeout(run, 600)
  },
)
onMounted(() => {
  store.onPhase(approved.value ? 'approved' : 'reviewing', version.value)
  void run()
})
onUnmounted(() => {
  ctl?.abort()
  clearTimeout(timer)
})

const issues = computed(() => [...(review.value?.issues ?? []), ...approveIssues.value])
const blocking = computed(() => issues.value.filter((i) => i.blocking))
const warnings = computed(() => issues.value.filter((i) => !i.blocking))
const ready = computed(() => !!review.value?.ready && !loading.value && !approveIssues.value.some((i) => i.blocking))

async function approve() {
  confirming.value = false
  await store.act(
    'Approving…',
    async () => {
      try {
        store.apply(await approveCampaign(store.id, standard.value ? version.value : 0))
        store.onPhase('approved', campaign.value.approved_version ?? version.value)
        review.value = null
      } catch (e) {
        if (e instanceof CampaignError && e.issues.length) approveIssues.value = e.issues
        throw e
      }
    },
    { refresh: false },
  )
}

const modeLabel = { standard: 'Standard', groups: 'Behavioral groups', individual: 'Individual AI personalization' }
const sourceLabel: Record<string, string> = { template: 'template', group: 'group message', individual: 'individual message', fallback: 'base draft (fallback)' }
const pct = (n: number) => `${Math.round(n * 1000) / 10}%`
const approvedAt = computed(() =>
  campaign.value.approved_at ? new Date(campaign.value.approved_at).toLocaleString('en-GB', { dateStyle: 'medium', timeStyle: 'short' }) : '',
)
const sampleLabel = (m: Review['plan']['samples'][number]) => {
  const parts = [m.channel === 'sms' ? 'SMS' : 'Email', languageName(m.language)]
  if (m.variant_position) parts.unshift(`Variant ${'ABC'[m.variant_position - 1]}`)
  const g = view.value.groups.find((x) => x.position === m.group_position)
  if (g && m.group_position != null) parts.unshift(g.name)
  parts.push(sourceLabel[m.source] ?? m.source)
  return parts.join(' · ')
}
</script>

<template>
  <section class="panel review" aria-labelledby="review-title">
    <header>
      <h2 id="review-title">{{ approved ? 'Approved' : 'Review & approve' }}</h2>
      <span v-if="loading && review" class="muted updating"><span class="spinner" aria-hidden="true" /> Updating…</span>
    </header>

    <div v-if="approved" class="box done" role="status">
      <p>
        <strong>Approved</strong> · {{ approvedAt }} ·
        {{ campaign.variants.length ? `A/B test: ${campaign.variants.map((v) => `Variant ${v.label} = Draft ${v.draft_version}`).join(', ')}` : `Draft ${campaign.approved_version}` }}
        · {{ modeLabel[campaign.personalization] }}
      </p>
      <p class="muted">Locked. Nothing has been sent.</p>
    </div>

    <p v-else-if="loading && !review" class="muted"><span class="spinner" aria-hidden="true" /> Checking the campaign…</p>

    <div v-if="review && !approved" class="result" :class="{ dim: loading }" :aria-busy="loading">
      <!-- Who receives what -->
      <div class="summary">
        <div class="box">
          <h3>Audience</h3>
          <p>{{ campaign.audience.name }}</p>
          <p class="muted">{{ number.format(campaign.audience.counts.total) }} people · {{ number.format(review.plan.recipients) }} reachable</p>
          <p class="muted">{{ campaign.audience.conditions_text.join(' · ') }}</p>
        </div>
        <div class="box">
          <h3>Messages</h3>
          <p>{{ number.format(review.plan.messages) }} messages</p>
          <p class="muted">
            SMS {{ number.format(review.plan.by_channel.sms ?? 0) }} · Email {{ number.format(review.plan.by_channel.email ?? 0) }}
            (people may get both)
          </p>
          <p class="muted">
            <template v-for="(n, l) in review.plan.by_language" :key="l">{{ languageName(String(l)) }} {{ number.format(n) }} · </template>
            <template v-for="(n, s) in review.plan.by_source" :key="s">{{ sourceLabel[String(s)] }} {{ number.format(n) }} · </template>
          </p>
        </div>
        <div class="box">
          <h3>AI generation</h3>
          <p>≈ {{ formatUSD(review.cost.usd) }} <span class="muted">projected</span></p>
          <p class="muted">{{ review.cost.calls }} calls · {{ formatTokens(review.cost.input_tokens + review.cost.output_tokens) }} tokens</p>
        </div>
      </div>

      <table v-if="review.plan.variants.length || review.plan.groups.length" class="buckets">
        <thead>
          <tr>
            <th>{{ review.plan.variants.length ? 'Variant' : 'Group' }}</th>
            <th>Recipients</th>
            <th>Share</th>
            <th>SMS</th>
            <th>Email</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="b in [...review.plan.variants, ...review.plan.groups]" :key="b.label">
            <td>{{ b.label }}</td>
            <td>{{ number.format(b.recipients) }}</td>
            <td>{{ pct(b.share) }}</td>
            <td>{{ number.format(b.sms) }}</td>
            <td>{{ number.format(b.email) }}</td>
          </tr>
        </tbody>
      </table>

      <!-- Gate -->
      <div v-if="blocking.length" class="box gate bad" role="alert">
        <h3>Fix before approving</h3>
        <ul>
          <li v-for="i in blocking" :key="i.scope + i.code + i.language + i.message">
            <strong v-if="i.scope">{{ i.scope }}<template v-if="i.language"> ({{ languageName(i.language) }})</template>:</strong> {{ i.message }}
          </li>
        </ul>
      </div>
      <div v-if="warnings.length" class="box gate">
        <h3>Check these</h3>
        <ul>
          <li v-for="i in warnings" :key="i.scope + i.code + i.language + i.message">
            <strong v-if="i.scope">{{ i.scope }}<template v-if="i.language"> ({{ languageName(i.language) }})</template>:</strong> {{ i.message }}
          </li>
        </ul>

      </div>

      <details class="samples" open>
        <summary>Message previews ({{ review.plan.samples.length }})</summary>
        <ul>
          <li v-for="m in review.plan.samples" :key="`${m.customer_id}-${m.channel}`" class="box">
            <p class="muted">{{ sampleLabel(m) }} · to {{ m.first_name }}</p>
            <p v-if="m.subject"><strong>{{ m.subject }}</strong></p>
            <p class="pre">{{ m.body }}</p>
          </li>
        </ul>
      </details>

      <p class="muted sending">
        {{ campaign.variants.length ? `A/B test: ${campaign.variants.map((v) => `${v.label} = Draft ${v.draft_version}`).join(', ')}` : campaign.personalization === 'standard' ? `Single message: Draft ${version}` : `${modeLabel[campaign.personalization]} from Draft ${version}` }}
        · <button type="button" class="linkish" @click="store.step.value = 'strategy'">change</button>
      </p>
      <div class="approve">
        <p v-if="loading" class="muted">Updating review…</p>
        <p v-else-if="ready">Ready to approve.</p>
        <p v-else class="muted">Fix the items above to approve.</p>
        <div v-if="confirming" class="row">
          <span>Lock this configuration? Nothing is sent yet.</span>
          <button type="button" class="btn" @click="confirming = false">Cancel</button>
          <button type="button" class="btn primary" :disabled="!!store.busy.value" @click="approve">
            {{ store.busy.value === 'Approving…' ? 'Approving…' : 'Confirm approval' }}
          </button>
        </div>
        <button v-else type="button" class="btn cta" :disabled="!ready || !!store.busy.value || view.jobs.groups || view.jobs.individual" @click="confirming = true">
          Approve campaign
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.review {
  border-color: var(--accent);
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 10px;
}
.pick {
  display: grid;
  gap: 4px;
  font-size: 12px;
  color: var(--text-muted);
}
.summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px;
}
.summary .box {
  display: grid;
  gap: 2px;
  align-content: start;
}
.buckets {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.buckets th,
.buckets td {
  padding: 6px 8px;
  text-align: left;
  border-bottom: 1px solid var(--border);
}
.buckets th {
  font-size: 12px;
  color: var(--text-muted);
  font-weight: 600;
}
.gate ul {
  margin: 6px 0 0;
  padding-left: 18px;
  font-size: 13px;
}
.gate.bad {
  border-color: var(--danger);
  background: var(--danger-soft);
}
.samples ul {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 8px;
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
}
.samples li {
  display: grid;
  gap: 4px;
  align-content: start;
  font-size: 13px;
}
.approve {
  position: sticky;
  bottom: 16px;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  box-shadow: var(--shadow);
}
.result {
  display: grid;
  gap: 12px;
  transition: opacity 0.2s;
}
.result.dim {
  opacity: 0.55;
}
.updating {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.linkish {
  padding: 0;
  font: inherit;
  color: var(--accent);
  background: none;
  border: 0;
  text-decoration: underline;
  cursor: pointer;
}
.done {
  border-color: var(--success);
  background: var(--success-soft);
}
@media (max-width: 720px) {
  .buckets {
    display: block;
    overflow-x: auto;
  }
}
</style>
