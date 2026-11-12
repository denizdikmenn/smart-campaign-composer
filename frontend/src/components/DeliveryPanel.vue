<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  getDelivery,
  languageName,
  retryFailed,
  reviewCampaign,
  sendCampaign,
  type CampaignView,
  type DeliveryCounts,
  type DeliveryReport,
  type MessageFilter,
  type Review,
} from '../api'
import AiProgress from './AiProgress.vue'
import DeliveryMessages from './DeliveryMessages.vue'

/**
 * Sending and live monitoring of an approved campaign. Before sending: what
 * Send will queue (recomputed now from the approved content) and the explicit
 * Send action. After: delivery progress polled from the database about once
 * a second until every message is finished.
 */
const props = defineProps<{ view: CampaignView }>()
const campaign = computed(() => props.view.campaign)
const number = new Intl.NumberFormat('en-US')

const report = ref<DeliveryReport | null>(null)
const plan = ref<Review['plan'] | null>(null)
const error = ref('')
const confirming = ref(false)
const sending = ref(false)
const filter = ref<MessageFilter>('') // messages table, also set by the tiles
const tick = ref(0) // bumped on every refresh so the messages table follows
const retryNote = ref('')
const retrying = ref(false)

const notSent = computed(() => report.value?.status === 'not_sent')
const finished = computed(() => report.value?.status === 'completed' || report.value?.status === 'completed_with_failures')

// --- polling -------------------------------------------------------------------------
let timer: ReturnType<typeof setTimeout> | undefined
let ctl: AbortController | null = null
let stopped = false

async function refresh() {
  ctl?.abort()
  ctl = new AbortController()
  try {
    report.value = await getDelivery(campaign.value.id, ctl.signal)
    error.value = ''
    tick.value++
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    error.value = e instanceof Error ? e.message : 'Could not load delivery status'
  }
}

async function poll() {
  clearTimeout(timer)
  await refresh()
  // Keep polling while messages are pending (and retry after a failed request).
  if (!stopped && (error.value || (report.value && !notSent.value && !finished.value))) {
    timer = setTimeout(() => void poll(), 1000)
  }
}

async function loadPlan() {
  try {
    plan.value = (await reviewCampaign(campaign.value.id, 0)).plan
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the send summary'
  }
}

onMounted(async () => {
  await poll()
  if (notSent.value) await loadPlan()
})
onUnmounted(() => {
  stopped = true
  clearTimeout(timer)
  ctl?.abort()
})

async function send() {
  confirming.value = false
  sending.value = true
  error.value = ''
  try {
    report.value = await sendCampaign(campaign.value.id)
    void poll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not start sending'
  } finally {
    sending.value = false
  }
}

async function retryAllFailed() {
  retrying.value = true
  retryNote.value = ''
  try {
    const r = await retryFailed(campaign.value.id)
    retryNote.value =
      `${number.format(r.requeued)} queued again.` +
      (r.permanent ? ` ${number.format(r.permanent)} rejected permanently by the provider can't be retried.` : '')
    void poll()
  } catch (e) {
    retryNote.value = e instanceof Error ? e.message : 'Retry failed'
  } finally {
    retrying.value = false
  }
}

// --- labels --------------------------------------------------------------------------
const approvedDraft = computed(() => {
  const versions = campaign.value.variants.length ? campaign.value.variants.map((v) => v.draft_version) : [campaign.value.approved_version]
  return props.view.drafts.find((d) => versions.includes(d.version)) ?? null
})
const channels = computed(() => {
  const d = approvedDraft.value
  return d ? [d.sms_enabled && 'SMS', d.email_enabled && 'Email'].filter(Boolean).join(' + ') : ''
})
const strategy = computed(() => {
  const c = campaign.value
  if (c.variants.length) return `A/B test (${c.variants.map((v) => `${v.label} = Draft ${v.draft_version}`).join(', ')})`
  return { standard: `Standard (Draft ${c.approved_version})`, groups: 'Behavioral groups', individual: 'Individual AI personalization' }[c.personalization]
})
const variantLabel = (pos: number) => `Variant ${campaign.value.variants.find((v) => v.position === pos)?.label ?? 'ABC'[pos - 1]}`
const groupLabel = (pos: number) => props.view.groups.find((g) => g.position === pos)?.name ?? (pos === 0 ? 'Everyone else' : `Group ${pos}`)

const STATUS: Record<DeliveryReport['status'], { label: string; tone: string }> = {
  not_sent: { label: 'Not sent', tone: '' },
  queued: { label: 'Queued', tone: 'accent' },
  sending: { label: 'Sending', tone: 'accent' },
  completed: { label: 'Completed', tone: 'ok' },
  completed_with_failures: { label: 'Completed with failures', tone: 'bad' },
}

const done = (c: DeliveryCounts) => c.sent + c.failed + c.skipped
const progress = computed(() => (report.value && report.value.totals.total ? done(report.value.totals) / report.value.totals.total : 0))
const rate = (c: DeliveryCounts) => {
  const attempted = c.sent + c.failed
  return attempted ? `${Math.round((c.sent / attempted) * 1000) / 10}%` : '–'
}
const time = (iso: string | null) => (iso ? new Date(iso).toLocaleTimeString('en-GB') : '')
const buckets = computed(() => {
  const r = report.value
  if (!r) return []
  return [...r.variants.map((b) => ({ ...b, label: variantLabel(b.position) })), ...r.groups.map((b) => ({ ...b, label: groupLabel(b.position) }))]
})
// The heartbeat is written every 5s; quiet for longer while work is pending means no worker is running.
const workerStale = computed(() => {
  const r = report.value
  if (!r || finished.value || notSent.value) return false
  return !r.worker_seen_at || Date.now() - new Date(r.worker_seen_at).getTime() > 20_000
})
const errorLabel = { retrying: 'Retrying', failed: 'Failed', skipped: 'Skipped' }
const tiles = computed(() => {
  const t = report.value?.totals
  if (!t) return []
  return [
    { f: '' as MessageFilter, label: 'Total', n: t.total, tone: '' },
    { f: 'pending' as MessageFilter, label: 'Queued', n: t.queued + t.sending, tone: '' },
    { f: 'retrying' as MessageFilter, label: 'Retrying', n: t.retrying, tone: t.retrying ? 'warn-tone' : '' },
    { f: 'sent' as MessageFilter, label: 'Sent', n: t.sent, tone: 'ok' },
    { f: 'failed' as MessageFilter, label: 'Failed', n: t.failed, tone: t.failed ? 'bad' : '' },
    { f: 'skipped' as MessageFilter, label: 'Skipped', n: t.skipped, tone: t.skipped ? 'warn-tone' : '' },
  ]
})
</script>

<template>
  <section class="panel delivery" aria-labelledby="delivery-title">
    <header>
      <h2 id="delivery-title">{{ notSent ? 'Send campaign' : 'Delivery' }}</h2>
      <span v-if="report" class="chip" :class="STATUS[report.status].tone" role="status">
        <span v-if="!finished && !notSent" class="spinner" aria-hidden="true" />
        {{ STATUS[report.status].label }}
      </span>
    </header>

    <div v-if="error" class="fail" role="alert"><span class="state failed">Failed</span><p class="msg">{{ error }}</p></div>
    <div v-if="!report && !error" class="loading-boxes" aria-label="Loading delivery status" role="status">
      <div v-for="n in 3" :key="n" class="skeleton" />
    </div>

    <!-- Before sending: what Send will queue now -->
    <template v-if="notSent">
      <AiProgress v-if="!plan && !error" kind="task" title="Counting recipients" detail="Rechecking consent and channels for everyone in the audience." compact />
      <template v-if="plan">
        <div class="summary">
          <div class="box">
            <h3>Audience</h3>
            <p>{{ campaign.audience.name }}</p>
            <p class="muted">{{ number.format(plan.recipients) }} people can be reached now</p>
          </div>
          <div class="box">
            <h3>Channels</h3>
            <p>{{ channels }}</p>
            <p class="muted">SMS {{ number.format(plan.by_channel.sms ?? 0) }} · Email {{ number.format(plan.by_channel.email ?? 0) }}</p>
          </div>
          <div class="box">
            <h3>Content</h3>
            <p>{{ strategy }}</p>
            <p class="muted">
              <template v-for="(n, l, i) in plan.by_language" :key="l">{{ i ? ' · ' : '' }}{{ languageName(String(l)) }} {{ number.format(n) }}</template>
            </p>
          </div>
        </div>
        <table v-if="plan.variants.length || plan.groups.length" class="table">
          <thead>
            <tr>
              <th>{{ plan.variants.length ? 'Variant' : 'Group' }}</th>
              <th>Recipients</th>
              <th>SMS</th>
              <th>Email</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="b in [...plan.variants, ...plan.groups]" :key="b.label">
              <td>{{ b.label }}</td>
              <td>{{ number.format(b.recipients) }}</td>
              <td>{{ number.format(b.sms) }}</td>
              <td>{{ number.format(b.email) }}</td>
            </tr>
          </tbody>
        </table>
        <div class="send">
          <p v-if="!confirming" class="muted">
            Counts are rechecked against current consent when you send. Messages go out in the background; you can leave this page.
          </p>
          <p v-else><strong>Send {{ number.format(plan.messages) }} messages now?</strong> This can't be undone.</p>
          <div class="actions">
            <template v-if="confirming">
              <button type="button" class="btn" @click="confirming = false">Cancel</button>
              <button type="button" class="btn primary" :disabled="sending" :aria-busy="sending" @click="send">{{ sending ? 'Queuing messages' : 'Confirm and send' }}</button>
            </template>
            <button v-else type="button" class="btn primary" :disabled="sending || !plan.messages" @click="confirming = true">
              Send campaign
            </button>
          </div>
        </div>
      </template>
    </template>

    <!-- After sending: live progress -->
    <template v-else-if="report">
      <div class="progress" role="progressbar" :aria-valuenow="Math.round(progress * 100)" aria-valuemin="0" aria-valuemax="100">
        <span :style="{ width: `${progress * 100}%` }" />
      </div>
      <p class="muted">
        {{ number.format(done(report.totals)) }} of {{ number.format(report.totals.total) }} messages finished
        · started {{ time(report.dispatched_at) }}<template v-if="report.finished_at"> · finished {{ time(report.finished_at) }}</template>
      </p>
      <p v-if="workerStale" class="box warn" role="alert">
        The delivery worker isn't responding{{ report.worker_seen_at ? ` (last seen ${time(report.worker_seen_at)})` : '' }}. Messages stay
        queued and are sent once it runs again.
      </p>

      <!-- Each tile filters the messages table below. -->
      <ul class="tiles" aria-label="Messages by status">
        <li v-for="t in tiles" :key="t.label">
          <button type="button" :class="[t.tone, { active: filter === t.f }]" :aria-pressed="filter === t.f" @click="filter = t.f">
            <span class="dt">{{ t.label }}</span>
            <span class="dd">{{ number.format(t.n) }}</span>
          </button>
        </li>
        <li class="static">
          <span class="dt">Retries made</span>
          <span class="dd">{{ number.format(report.totals.retries) }}</span>
        </li>
      </ul>

      <table class="table">
        <thead>
          <tr>
            <th>Breakdown</th>
            <th>Messages</th>
            <th>Pending</th>
            <th>Sent</th>
            <th>Failed</th>
            <th>Skipped</th>
            <th>Success</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in [
              ...Object.entries(report.channels).map(([ch, c]) => ({ ...c, label: ch === 'sms' ? 'SMS' : 'Email', key: ch })),
              ...buckets.map((b) => ({ ...b, key: b.label })),
            ]"
            :key="row.key"
          >
            <td>{{ row.label }}</td>
            <td>{{ number.format(row.total) }}</td>
            <td>{{ number.format(row.queued + row.sending + row.retrying) }}</td>
            <td>{{ number.format(row.sent) }}</td>
            <td>{{ number.format(row.failed) }}</td>
            <td>{{ number.format(row.skipped) }}</td>
            <td>{{ rate(row) }}</td>
          </tr>
        </tbody>
      </table>

      <div v-if="report.errors.length || report.totals.retrying || report.retryable_failed" class="box issues">
        <div class="issues-head">
          <h3>Retries, failures and skips</h3>
          <button v-if="report.retryable_failed" type="button" class="btn primary" :disabled="retrying" @click="retryAllFailed">
            Retry {{ number.format(report.retryable_failed) }} failed
          </button>
        </div>
        <p v-if="retryNote" class="muted" role="status">{{ retryNote }}</p>
        <p v-if="report.totals.failed > report.retryable_failed" class="muted">
          {{ number.format(report.totals.failed - report.retryable_failed) }} failed permanently (the provider rejected the number or
          address) and can't be retried.
        </p>
        <p v-if="report.totals.skipped" class="muted">
          Skipped messages were never sent: the recipient is in the audience but hasn't consented to that channel, or withdrew
          consent before sending.
        </p>
        <p v-if="report.totals.retrying" class="muted">
          {{ number.format(report.totals.retrying) }} waiting to retry<template v-if="report.next_retry_at">, next at {{ time(report.next_retry_at) }}</template>.
          Each message gets up to 4 attempts.
        </p>
        <ul>
          <li v-for="e in report.errors" :key="e.status + e.reason">
            <span class="chip" :class="{ bad: e.status === 'failed' }">{{ errorLabel[e.status] }}</span>
            {{ e.reason }} <span class="muted">× {{ number.format(e.count) }}</span>
          </li>
        </ul>
      </div>
      <DeliveryMessages v-model:status="filter" :view="view" :tick="tick" @retried="poll" />

      <p class="muted note">“Sent” means the provider accepted the message; delivery to the phone or inbox isn't reported.</p>
    </template>
  </section>
</template>

<style scoped>
.loading-boxes {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px;
}
.loading-boxes .skeleton {
  height: 84px;
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
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.table th,
.table td {
  padding: 6px 8px;
  text-align: left;
  border-bottom: 1px solid var(--border);
}
.table th {
  font-size: 12px;
  color: var(--text-muted);
  font-weight: 600;
}
.send {
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
.actions {
  display: flex;
  gap: 8px;
}
.progress {
  height: 8px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.progress span {
  display: block;
  height: 100%;
  background: var(--success);
  transition: width 0.4s ease;
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.tiles button,
.tiles .static {
  display: grid;
  gap: 2px;
  width: 100%;
  height: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface-muted);
  color: inherit;
  font: inherit;
  text-align: left;
}
.tiles button {
  cursor: pointer;
}
.tiles button:hover,
.tiles button.active {
  border-color: var(--accent);
}
.tiles button.active {
  background: var(--selected-surface);
}
.tiles .dt {
  font-size: 12px;
  color: var(--text-muted);
}
.tiles .dd {
  font-size: 20px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.tiles .ok .dd {
  color: var(--success);
}
.tiles .bad .dd {
  color: var(--danger);
}
.tiles .warn-tone .dd {
  color: var(--accent);
}
.issues-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.issues p {
  margin-top: 6px;
}
.warn {
  border-color: var(--danger);
  background: var(--danger-soft);
}
.issues ul {
  display: grid;
  gap: 6px;
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
  font-size: 13px;
}
.note {
  font-size: 12px;
}
@media (max-width: 640px) {
  .table {
    display: block;
    overflow-x: auto;
  }
}
</style>
