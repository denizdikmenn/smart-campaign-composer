<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { approveCampaign, CampaignError, formatTokens, formatUSD, reviewCampaign, type Issue, type ResolvedMessage, type Review } from '../api'
import { useCampaign } from '../campaign'
import { deliveryHref } from '../router'
import AiProgress from './AiProgress.vue'
import LangChip from './LangChip.vue'

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
// Previews: variant/group -> channel -> language. The backend samples one message per
// variant/group × language × channel; languages are versions of the same message, so
// they are a switch inside a channel card, never a card of their own.
interface PreviewChannel {
  channel: 'sms' | 'email'
  byLang: Record<string, ResolvedMessage>
  langs: string[]
}
interface PreviewBucket {
  key: string
  title: string
  kind: 'variant' | 'group' | 'single'
  meta: string
  channels: PreviewChannel[]
}
const langOrder = (l: string) => {
  const i = campaign.value.languages.indexOf(l)
  return i < 0 ? 99 : i
}
const previews = computed<PreviewBucket[]>(() => {
  const buckets = new Map<string, PreviewBucket>()
  for (const m of review.value?.plan.samples ?? []) {
    const group = m.group_position != null && m.group_position >= 0 ? view.value.groups.find((g) => g.position === m.group_position) : undefined
    const key = `${m.variant_position ?? 0}/${group ? group.position : '-'}`
    let b = buckets.get(key)
    if (!b) {
      const variant = m.variant_position ? campaign.value.variants.find((v) => v.position === m.variant_position) : undefined
      b = variant
        ? { key, kind: 'variant', title: `Variant ${variant.label}`, meta: `Draft ${variant.draft_version}`, channels: [] }
        : group
          ? { key, kind: 'group', title: group.name, meta: 'Behavioral group', channels: [] }
          : { key, kind: 'single', title: campaign.value.personalization === 'individual' ? 'Individual messages' : 'Single message', meta: `Draft ${version.value}`, channels: [] }
      if (variant && group) b.meta += ` · ${group.name}`
      buckets.set(key, b)
    }
    let ch = b.channels.find((c) => c.channel === m.channel)
    if (!ch) b.channels.push((ch = { channel: m.channel, byLang: {}, langs: [] }))
    if (!ch.byLang[m.language]) {
      ch.byLang[m.language] = m
      ch.langs.push(m.language)
      ch.langs.sort((a, b) => langOrder(a) - langOrder(b))
    }
  }
  const list = [...buckets.values()]
  for (const b of list) b.channels.sort((a, c) => (a.channel === 'sms' ? -1 : 1) - (c.channel === 'sms' ? -1 : 1))
  return list
})
// Language chosen per channel card; defaults to the base language.
const previewLang = ref<Record<string, string>>({})
const shownLang = (b: PreviewBucket, ch: PreviewChannel) => {
  const picked = previewLang.value[`${b.key}/${ch.channel}`]
  if (picked && ch.byLang[picked]) return picked
  return ch.byLang[campaign.value.base_language] ? campaign.value.base_language : ch.langs[0]!
}
const multiLang = computed(() => previews.value.some((b) => b.channels.some((c) => c.langs.length > 1)))
</script>

<template>
  <section class="panel review" aria-labelledby="review-title">
    <header>
      <h2 id="review-title">{{ approved ? 'Approved' : 'Review & approve' }}</h2>
      <span v-if="loading && review" class="state busy updating" role="status">Re-checking</span>
      <span v-else-if="approved" class="state locked">Locked</span>
    </header>

    <div v-if="approved" class="box done" role="status">
      <p>
        <strong>Approved</strong> · {{ approvedAt }} ·
        {{ campaign.variants.length ? `A/B test: ${campaign.variants.map((v) => `Variant ${v.label} = Draft ${v.draft_version}`).join(', ')}` : `Draft ${campaign.approved_version}` }}
        · {{ modeLabel[campaign.personalization] }}
      </p>
      <p class="muted"><span class="state locked">Locked</span> Content and strategy can no longer change. Sending is a separate step.</p>
      <p><a class="btn primary" :href="deliveryHref(store.id)">Continue to sending →</a></p>
    </div>

    <AiProgress
      v-else-if="loading && !review"
      kind="task"
      title="Checking the campaign"
      detail="Consent, message length, translations and personalization for every recipient."
      compact
    />

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
            <span class="lang-row"><LangChip v-for="(n, l) in review.plan.by_language" :key="l" :lang="String(l)" :count="n" /></span>
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
            <strong v-if="i.scope">{{ i.scope }}<template v-if="i.language"> <LangChip :lang="i.language" plain /></template>:</strong> {{ i.message }}
          </li>
        </ul>
      </div>
      <div v-if="warnings.length" class="box gate">
        <h3>Check these</h3>
        <ul>
          <li v-for="i in warnings" :key="i.scope + i.code + i.language + i.message">
            <strong v-if="i.scope">{{ i.scope }}<template v-if="i.language"> <LangChip :lang="i.language" plain /></template>:</strong> {{ i.message }}
          </li>
        </ul>

      </div>

      <details class="samples" open>
        <summary>
          Message previews
          <span class="muted">
            · {{ previews.length }} {{ previews[0]?.kind === 'variant' ? 'variant' : previews[0]?.kind === 'group' ? 'group' : 'message' }}{{ previews.length === 1 ? '' : 's' }}
            <template v-if="multiLang"> · switch language inside each card</template>
          </span>
        </summary>
        <p v-if="!previews.length" class="muted">No previews: nobody in the audience can be reached.</p>
        <section v-for="b in previews" :key="b.key" class="bucket" :class="b.kind" :aria-label="`${b.title} previews`">
          <header class="bucket-head">
            <span v-if="b.kind === 'variant'" class="bucket-badge" aria-hidden="true">{{ b.title.slice(-1) }}</span>
            <h3>{{ b.title }}</h3>
            <span class="muted">{{ b.meta }}</span>
          </header>
          <div class="channels">
            <article v-for="ch in b.channels" :key="ch.channel" class="preview-card" :class="ch.channel">
              <header>
                <span class="channel-name">
                  <svg v-if="ch.channel === 'sms'" viewBox="0 0 16 16" aria-hidden="true"><path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2Z" /></svg>
                  <svg v-else viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="3.5" width="12" height="9" rx="1.5" /><path d="M2.5 4.5 8 8.5l5.5-4" /></svg>
                  {{ ch.channel === 'sms' ? 'SMS' : 'Email' }}
                </span>
                <!-- Language: same message, localized. Switching changes text only. -->
                <div v-if="ch.langs.length > 1" class="langs" role="group" :aria-label="`${b.title} ${ch.channel === 'sms' ? 'SMS' : 'email'} language`">
                  <button
                    v-for="l in ch.langs"
                    :key="l"
                    type="button"
                    :aria-pressed="shownLang(b, ch) === l"
                    @click="previewLang[`${b.key}/${ch.channel}`] = l"
                  >
                    <LangChip :lang="l" plain />
                  </button>
                </div>
                <LangChip v-else :lang="ch.langs[0]!" />
              </header>
              <Transition name="swap" mode="out-in">
                <div :key="shownLang(b, ch)" class="message" :lang="shownLang(b, ch)">
                  <template v-if="ch.channel === 'email'">
                    <p class="subject">{{ ch.byLang[shownLang(b, ch)]!.subject || '—' }}</p>
                    <p class="pre body">{{ ch.byLang[shownLang(b, ch)]!.body }}</p>
                  </template>
                  <p v-else class="pre bubble">{{ ch.byLang[shownLang(b, ch)]!.body }}</p>
                </div>
              </Transition>
              <p class="muted foot">
                To {{ ch.byLang[shownLang(b, ch)]!.first_name }} · {{ sourceLabel[ch.byLang[shownLang(b, ch)]!.source] ?? ch.byLang[shownLang(b, ch)]!.source }}
              </p>
            </article>
          </div>
        </section>
      </details>

      <p class="muted sending">
        {{ campaign.variants.length ? `A/B test: ${campaign.variants.map((v) => `${v.label} = Draft ${v.draft_version}`).join(', ')}` : campaign.personalization === 'standard' ? `Single message: Draft ${version}` : `${modeLabel[campaign.personalization]} from Draft ${version}` }}
        · <button type="button" class="linkish" @click="store.step.value = 'strategy'">change</button>
      </p>
      <div class="approve">
        <p v-if="loading"><span class="state busy">Updating review</span></p>
        <p v-else-if="ready">Ready to approve.</p>
        <p v-else class="muted">Fix the items above to approve.</p>
        <div v-if="confirming" class="row">
          <span>Lock this configuration? Nothing is sent yet.</span>
          <button type="button" class="btn" @click="confirming = false">Cancel</button>
          <button type="button" class="btn primary" :disabled="!!store.busy.value" :aria-busy="store.busy.value === 'Approving…'" @click="approve">
            {{ store.busy.value === 'Approving…' ? 'Approving' : 'Confirm approval' }}
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
/* Previews: variant (outer) > channel (card) > language (switch inside the card). */
.samples {
  display: grid;
  gap: 12px;
}
.samples summary {
  font-weight: 600;
  cursor: pointer;
}
.samples summary .muted {
  font-weight: 400;
}
.bucket {
  display: grid;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-muted);
}
.bucket.variant {
  border-left: 4px solid var(--accent);
}
.bucket-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.bucket-badge {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  font-size: 12px;
  font-weight: 700;
  border-radius: 50%;
  color: #fff;
  background: var(--accent);
}
.bucket-head h3 {
  font-size: 15px;
}
.channels {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 10px;
}
.preview-card {
  display: grid;
  grid-template-rows: auto 1fr auto;
  gap: 8px;
  padding: 12px 14px;
  font-size: 13px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
}
.preview-card > header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.channel-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}
.channel-name svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.5;
  stroke-linejoin: round;
}
/* Segmented language switch: reads as a setting of this card, not a new item. */
.lang-row {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-right: 6px;
  vertical-align: middle;
}
.langs {
  display: inline-flex;
  padding: 2px;
  border-radius: 99px;
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.langs button {
  padding: 3px 10px;
  font: inherit;
  font-size: 12px;
  font-weight: 500;
  border: 0;
  border-radius: 99px;
  color: var(--text-muted);
  background: none;
  cursor: pointer;
  transition:
    background-color 0.15s,
    color 0.15s;
}
.langs button[aria-pressed='true'] {
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
}
.lang-only {
  font-size: 12px;
  color: var(--text-muted);
}
.message {
  display: grid;
  gap: 6px;
  align-content: start;
}
.subject {
  font-weight: 600;
}
.bubble {
  justify-self: start;
  max-width: 100%;
  padding: 10px 12px;
  border-radius: 14px 14px 14px 4px;
  background: var(--selected-surface);
}
.body {
  padding-top: 6px;
  border-top: 1px solid var(--border);
}
.foot {
  font-size: 12px;
}
.swap-enter-active,
.swap-leave-active {
  transition: opacity 0.15s ease;
}
.swap-enter-from,
.swap-leave-to {
  opacity: 0;
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
  display: grid;
  gap: 8px;
  justify-items: start;
  border-color: var(--success);
  background: var(--success-soft);
}
.done p {
  margin: 0;
}
.done .btn {
  display: inline-block;
  text-decoration: none;
}
@media (max-width: 720px) {
  .buckets {
    display: block;
    overflow-x: auto;
  }
}
</style>
