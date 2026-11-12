<script setup lang="ts">
import { onActivated, onMounted, ref } from 'vue'
import { fetchSummary, listCampaigns, type CampaignSummary, type Summary } from '../api'
import { deliveryHref, href } from '../router'

const number = new Intl.NumberFormat('en-US')
const pct = (part: number, whole: number) => (whole ? Math.round((part / whole) * 100) : 0)

const summary = ref<Summary | null>(null)
onMounted(async () => {
  try {
    summary.value = await fetchSummary()
  } catch {
    summary.value = null // stats are context only; the page works without them
  }
})

// Recent campaigns; refreshed whenever the page is shown again (KeepAlive).
const campaigns = ref<CampaignSummary[]>([])
async function loadCampaigns() {
  try {
    campaigns.value = await listCampaigns()
  } catch {
    // the list is a convenience; starting a campaign still works
  }
}
onActivated(loadCampaigns) // also runs on first mount
const STATUS: Record<CampaignSummary['delivery_status'], { label: string; tone: string }> = {
  not_sent: { label: 'Approved · not sent', tone: '' },
  queued: { label: 'Queued', tone: 'accent' },
  sending: { label: 'Sending', tone: 'accent' },
  completed: { label: 'Sent', tone: 'ok' },
  completed_with_failures: { label: 'Sent with failures', tone: 'bad' },
}
const date = (iso: string) => new Date(iso).toLocaleDateString('en-GB', { dateStyle: 'medium' })

// The guide can be hidden; remembered per browser only (a convenience).
const GUIDE_KEY = 'campaigns.guideHidden'
const read = () => {
  try {
    return localStorage.getItem(GUIDE_KEY) === '1'
  } catch {
    return false
  }
}
const guideHidden = ref(read())
function setGuide(hidden: boolean) {
  guideHidden.value = hidden
  try {
    localStorage.setItem(GUIDE_KEY, hidden ? '1' : '0')
  } catch {
    // storage unavailable: only this visit is affected
  }
}

const STEPS = [
  { title: 'Describe', text: 'Write who you want to reach and why, in plain language.', ready: true },
  { title: 'Compare audiences', text: 'Get 2–3 audience options with real customer and channel counts.', ready: true },
  { title: 'Write messages', text: 'Claude drafts SMS and email copy with personalization; edit or regenerate.', ready: true },
  { title: 'Review & approve', text: 'Preview for real recipients and approve. Nothing is sent yet.', ready: true },
  { title: 'Send & monitor', text: 'Send when ready and watch delivery, retries and failures live.', ready: true },
]
</script>

<template>
  <section aria-labelledby="campaigns-title">
    <header class="head">
      <div>
        <h1 id="campaigns-title">Campaigns</h1>
        <p>Plan targeted SMS and email campaigns from a short brief.</p>
      </div>
      <button v-if="guideHidden" type="button" class="link" @click="setGuide(false)">Show guide</button>
    </header>

    <dl v-if="summary" class="stats" aria-label="Your customers">
      <div>
        <dt>Customers</dt>
        <dd>{{ number.format(summary.total) }}</dd>
      </div>
      <div>
        <dt>Email reachable</dt>
        <dd>{{ number.format(summary.email_consent) }} <small>{{ pct(summary.email_consent, summary.total) }}%</small></dd>
      </div>
      <div>
        <dt>SMS reachable</dt>
        <dd>{{ number.format(summary.sms_consent) }} <small>{{ pct(summary.sms_consent, summary.total) }}%</small></dd>
      </div>
      <a class="explore" :href="href('/explorer')">Explore customers →</a>
    </dl>

    <section v-if="campaigns.length" class="recent" aria-labelledby="recent-title">
      <h2 id="recent-title">Recent campaigns</h2>
      <ul>
        <li v-for="c in campaigns" :key="c.id">
          <component :is="c.status === 'approved' ? 'a' : 'div'" class="row" :href="c.status === 'approved' ? deliveryHref(c.id) : undefined">
            <span class="what">
              <strong>{{ c.audience || 'Campaign' }}</strong>
              <span class="brief">{{ c.brief }}</span>
            </span>
            <span class="meta">
              <span v-if="c.dispatched_at" class="count">{{ number.format(c.sent) }} / {{ number.format(c.messages) }} sent</span>
              <span class="chip" :class="c.status === 'approved' ? STATUS[c.delivery_status].tone : ''">
                {{ c.status === 'approved' ? STATUS[c.delivery_status].label : 'Draft' }}
              </span>
              <span class="muted">{{ date(c.created_at) }}</span>
            </span>
          </component>
        </li>
      </ul>
      <p class="muted">Approved campaigns open their delivery page. Resuming drafts isn't available yet.</p>
    </section>

    <div v-else class="empty">
      <svg class="art" viewBox="0 0 64 64" aria-hidden="true">
        <path d="M32 58V30" stroke="currentColor" stroke-width="3" stroke-linecap="round" fill="none" />
        <path d="M32 34c0-12 9-20 22-20 0 13-9 20-22 20Z" fill="var(--accent-soft)" stroke="currentColor" stroke-width="2.5" />
        <path d="M32 42c0-10-7-16-18-16 0 10 7 16 18 16Z" fill="var(--selected-surface)" stroke="currentColor" stroke-width="2.5" />
      </svg>
      <h2>No campaigns yet</h2>
      <p>
        Start with a short brief. We'll suggest audiences and count them against your customer data, with email and SMS
        reach shown separately.
      </p>
      <a class="primary" :href="href('/campaigns/new')">Start a campaign</a>
    </div>

    <section v-if="!guideHidden" class="guide" aria-labelledby="guide-title">
      <div class="guide-head">
        <h2 id="guide-title">How it works</h2>
        <button type="button" class="link" @click="setGuide(true)">Hide guide</button>
      </div>
      <ol class="steps">
        <li v-for="(s, i) in STEPS" :key="s.title" :class="{ soon: !s.ready }">
          <span class="num" aria-hidden="true">{{ i + 1 }}</span>
          <h3>{{ s.title }}</h3>
          <p>{{ s.text }}</p>
          <span class="tag">{{ s.ready ? 'Available' : 'Coming soon' }}</span>
        </li>
      </ol>

      <div class="tips">
        <div>
          <h3>Writing a good brief</h3>
          <ul>
            <li><strong>Who:</strong> city, purchase or cart activity, spend, categories.</li>
            <li><strong>When:</strong> a timeframe, such as “in the last 30 days”.</li>
            <li><strong>Why:</strong> the offer or goal, and the tone you want.</li>
          </ul>
        </div>
        <blockquote>
          “Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but
          haven't purchased since.”
        </blockquote>
      </div>
    </section>

    <a class="fab" :href="href('/campaigns/new')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" /></svg>
      New campaign
    </a>
  </section>
</template>

<style scoped>
.recent {
  display: grid;
  gap: 10px;
  margin-bottom: 24px;
}
.recent h2 {
  margin: 0;
  font-size: 17px;
}
.recent ul {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.recent .row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: inherit;
  text-decoration: none;
}
.recent a.row:hover {
  border-color: var(--accent);
}
.recent .what {
  display: grid;
  gap: 2px;
  min-width: 0;
  flex: 1 1 280px;
}
.recent .brief {
  overflow: hidden;
  font-size: 13px;
  color: var(--text-muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.recent .meta {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
}
h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.3;
  letter-spacing: -0.02em;
}
.head p {
  margin: 2px 0 0;
  color: var(--text-muted);
}
h2 {
  margin: 0;
  font-size: 20px;
  line-height: 1.4;
}
h3 {
  margin: 0;
  font-size: 15px;
}
.link {
  padding: 6px 10px;
  border: 0;
  background: none;
  color: var(--text-muted);
  font-weight: 600;
  font-size: 13px;
  cursor: pointer;
  border-radius: var(--radius-control);
}
.link:hover {
  color: var(--text);
  background: var(--surface);
}

.stats {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px 32px;
  margin: 0 0 16px;
  padding: 14px 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-muted);
}
.stats dt {
  font-size: 12px;
  color: var(--text-muted);
}
.stats dd {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.stats small {
  font-size: 12px;
  font-weight: 500;
  color: var(--text-muted);
}
.explore {
  margin-left: auto;
  font-weight: 600;
  color: var(--accent);
  text-decoration: none;
}
.explore:hover {
  text-decoration: underline;
}

.empty {
  display: grid;
  justify-items: center;
  gap: 8px;
  padding: 48px 24px;
  text-align: center;
  border: 1px solid var(--border);
  border-radius: 20px;
  background: var(--surface);
  box-shadow: var(--shadow);
  animation: rise 0.25s ease-out backwards;
}
.empty p {
  max-width: 460px;
  margin: 0;
  color: var(--text-muted);
}
.art {
  width: 64px;
  height: 64px;
  color: var(--accent);
}
.primary {
  margin-top: 12px;
  padding: 12px 22px;
  border-radius: var(--radius-control);
  background: var(--accent);
  color: #fff;
  font-weight: 600;
  text-decoration: none;
  transition: transform 0.15s, box-shadow 0.15s;
}
.primary:hover {
  transform: translateY(-1px);
  box-shadow: var(--shadow);
}
.empty .note {
  margin-top: 4px;
  font-size: 12px;
}

.guide {
  margin-top: 24px;
  animation: rise 0.25s 0.05s ease-out backwards;
}
.guide-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.steps {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(220px, 100%), 1fr));
  gap: 12px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.steps li {
  position: relative;
  display: grid;
  gap: 6px;
  align-content: start;
  padding: 16px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}
.steps li.soon {
  background: var(--surface-muted);
}
.steps p {
  margin: 0;
  font-size: 13px;
  color: var(--text-muted);
}
.num {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--accent-soft);
  font-weight: 600;
  font-size: 13px;
}
.soon .num {
  background: var(--border);
}
.tag {
  justify-self: start;
  margin-top: 4px;
  padding: 2px 8px;
  border-radius: 99px;
  font-size: 11px;
  font-weight: 600;
  color: var(--success);
  background: var(--success-soft);
}
.soon .tag {
  color: var(--text-muted);
  background: var(--border);
}
.tips {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(300px, 100%), 1fr));
  gap: 16px;
  margin-top: 12px;
  padding: 18px 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}
.tips ul {
  margin: 8px 0 0;
  padding-left: 18px;
  font-size: 13px;
  color: var(--text-muted);
}
.tips li + li {
  margin-top: 4px;
}
.tips strong {
  color: var(--text);
}
blockquote {
  margin: 0;
  padding: 12px 16px;
  border-left: 3px solid var(--accent-soft);
  border-radius: 0 var(--radius-control) var(--radius-control) 0;
  background: var(--surface-muted);
  font-size: 13px;
  color: var(--text-muted);
}

.fab {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 20;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 14px 22px 14px 18px;
  border-radius: 99px;
  background: var(--accent);
  color: #fff;
  font-weight: 600;
  text-decoration: none;
  box-shadow: 0 12px 32px rgb(32 77 60 / 0.3);
  transition: transform 0.15s, box-shadow 0.15s;
  animation: rise 0.25s 0.15s ease-out backwards;
}
.fab svg {
  width: 20px;
  height: 20px;
}
.fab:hover {
  transform: translateY(-2px);
  box-shadow: 0 16px 36px rgb(32 77 60 / 0.36);
}
.fab:focus-visible {
  outline: 3px solid var(--accent-soft);
  outline-offset: 3px;
}

@keyframes rise {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
}
@media (max-width: 600px) {
  .fab {
    right: 16px;
    bottom: 16px;
  }
  .explore {
    margin-left: 0;
  }
}
</style>
