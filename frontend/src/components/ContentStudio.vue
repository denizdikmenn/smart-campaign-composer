<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { createDraft, languageName, localizeDraft, type CampaignView, type Version } from '../api'
import { createCampaignStore, draftLabel, ORIGIN_LABELS, provideCampaign, type Step, type StudioPhase } from '../campaign'
import AiProgress from './AiProgress.vue'
import ChannelEditor from './ChannelEditor.vue'
import RecipientPreview from './RecipientPreview.vue'
import ReviewPanel from './ReviewPanel.vue'
import StrategyPanel from './StrategyPanel.vue'

export type { Step, StudioPhase }

const props = defineProps<{ initial: CampaignView }>()
const emit = defineEmits<{ phase: [phase: StudioPhase, draft: number]; step: [step: Step] }>()

const store = createCampaignStore(props.initial, (p, d) => emit('phase', p, d))
provideCampaign(store)
watch(store.step, (s) => emit('step', s), { immediate: true })

async function go(s: Step) {
  if (!(await store.flushSave())) return
  store.step.value = s
  window.scrollTo({ top: 0, behavior: 'smooth' })
}
defineExpose({ go })
const { campaign, drafts, current, currentVersion, approved, live } = store

const number = new Intl.NumberFormat('en-US')
const options = props.initial.options
const audience = computed(() => campaign.value.audience)
const label = (v: string) => (v.charAt(0).toUpperCase() + v.slice(1)).replaceAll('_', ' ')
const channel = ref<'sms' | 'email'>('sms')

// --- language being edited ----------------------------------------------------------
const lang = ref(campaign.value.base_language)
const languages = computed(() => campaign.value.languages)
const langStatus = (l: string) => current.value?.localization.find((s) => s.language === l)?.status ?? 'missing'

/** The text object bound to the editors: the draft itself or one translation. */
const text = computed<Version | null>(() => {
  const d = current.value
  if (!d) return null
  if (lang.value === d.language) return d
  return d.translations[lang.value] ?? null
})
// Writing a missing translation by hand creates it (saved like any edit).
function ensureTranslation() {
  const d = current.value
  if (d && lang.value !== d.language && !d.translations[lang.value]) {
    d.translations[lang.value] = { sms_body: '', email_subject: '', email_body: '' }
  }
}
watch([currentVersion, lang], ensureTranslation, { immediate: true })

const issuesFor = (ch: 'sms' | 'email') =>
  (current.value?.issues ?? []).filter(
    (i) => i.language === lang.value && (ch === 'sms' ? i.field === 'sms_body' : i.field.startsWith('email_')),
  )
const channelIssues = computed(() => (current.value?.issues ?? []).filter((i) => i.field === 'channels' || i.field === 'audience'))
const translationIssue = computed(() =>
  (current.value?.issues ?? []).find((i) => i.field === 'translations' && i.language === lang.value),
)
const needsTranslation = computed(() => languages.value.some((l) => ['missing', 'stale'].includes(langStatus(l))))

async function updateTranslations() {
  const d = current.value
  if (!d) return
  await store.act('Translating…', async () => store.upsertDraft(await localizeDraft(store.id, d.version)))
}

// --- live (streaming) view ------------------------------------------------------------
const showLive = computed(() => live.active)
const liveText = computed<Version>(() => ({
  sms_body: live.fields.sms_body ?? '',
  email_subject: live.fields.email_subject ?? '',
  email_body: live.fields.email_body ?? '',
}))
const readonly = computed(() => approved.value || live.active)

// Fields stream in order (settings, SMS, subject, body, then translations), so the
// furthest field received tells which stage Claude is on.
const liveStages = computed(() => {
  const s = ['Choosing tone & settings', 'Writing SMS', 'Writing email subject', 'Writing email body']
  if (languages.value.length > 1) s.push('Translating')
  return s
})
const liveStage = computed(() => {
  if (live.localizing.length) return liveStages.value.length - 1
  if (live.fields.email_body) return 3
  if (live.fields.email_subject) return 2
  if (live.fields.sms_body) return 1
  return 0
})
const liveTitle = computed(() => {
  const n = store.nextVersion.value
  if (live.localizing.length) return `Translating Draft ${n}`
  return `Claude is writing Draft ${n}`
})
const liveDetail = computed(() =>
  live.localizing.length
    ? `Into ${live.localizing.map(languageName).join(', ')}. The base text is done.`
    : `${live.intent === 'candidate' ? 'An A/B candidate · ' : ''}${languageName(campaign.value.base_language)}${live.feedback ? ` · applying “${live.feedback}”` : ''}`,
)
// Follow the stream: show the channel being written.
watch(liveStage, (s) => {
  if (live.active && s >= 2 && s <= 3) channel.value = 'email'
  else if (live.active && s === 1) channel.value = 'sms'
})

// --- drafts ----------------------------------------------------------------------------
const feedback = ref('')
async function regenerate() {
  const d = current.value
  if (!d) return
  if (await store.generate(d.version, 'regenerate', feedback.value)) feedback.value = ''
}
async function newDraft(from = 0) {
  await store.act(from ? 'Duplicating…' : 'Creating…', async () => {
    const d = await createDraft(store.id, from)
    store.upsertDraft(d)
    currentVersion.value = d.version
  })
}

// Compare: another draft side by side (read only), in the same language and channel.
const compareWith = ref(0)
const compared = computed(() => drafts.value.find((d) => d.version === compareWith.value) ?? null)
const comparedText = computed<Version | null>(() => {
  const d = compared.value
  if (!d) return null
  return lang.value === d.language ? d : (d.translations[lang.value] ?? null)
})

onMounted(() => {
  if (!drafts.value.length) void store.generate(0)
  else emit('phase', approved.value ? 'approved' : 'editing', currentVersion.value)
})
</script>

<template>
  <section class="studio" aria-label="Campaign content">
    <div v-if="store.error.value" class="fail" role="alert"><span class="state failed">Failed</span><p class="msg">{{ store.error.value }}</p><button type="button" class="ghost small" @click="store.error.value = ''">Dismiss</button></div>
    <template v-if="store.step.value === 'content'">
    <!-- Drafts: working versions. Variants are drafts promoted in the strategy step. -->
    <div class="drafts" role="tablist" aria-label="Drafts">
      <button
        v-for="d in drafts"
        :key="d.version"
        type="button"
        role="tab"
        class="draft-tab"
        :aria-selected="!showLive && d.version === currentVersion"
        :title="`${ORIGIN_LABELS[d.origin]}${d.parent_version ? ' from Draft ' + d.parent_version : ''}${d.feedback ? ' · Feedback: ' + d.feedback : ''}`"
        @click="store.selectDraft(d.version)"
      >
        Draft {{ d.version }}
        <span v-if="store.variantOf(d.version)" class="tag variant">Variant {{ store.variantOf(d.version)!.label }}</span>
        <span v-if="campaign.approved_version === d.version && !campaign.variants.length" class="tag ok">Approved</span>
      </button>
      <span v-if="live.active" role="tab" aria-selected="true" class="draft-tab live">
        Draft {{ store.nextVersion.value }} <span class="state live">{{ live.localizing.length ? 'Translating' : 'Writing' }}</span>
      </span>
      <span class="save" role="status" aria-live="polite">
        <span v-if="approved" class="state locked">Locked · approved</span>
        <span v-else-if="store.saveState.value === 'saving'" class="state saving">Saving</span>
        <span v-else-if="store.saveState.value === 'dirty'" class="state dirty">Unsaved changes</span>
        <span v-else-if="store.saveState.value === 'error'" class="state failed" :title="store.saveError.value">Not saved: {{ store.saveError.value }}</span>
        <span v-else-if="current" class="state saved">Saved</span>
      </span>
    </div>

    <div v-if="current && !approved" class="toolbar">
      <span class="origin">
        {{ ORIGIN_LABELS[current.origin] }}<template v-if="current.parent_version"> from Draft {{ current.parent_version }}</template>
        <template v-if="current.feedback"> · “{{ current.feedback }}”</template>
      </span>
      <button type="button" class="ghost small" :disabled="!!store.busy.value || live.active" :aria-busy="store.busy.value === 'Creating…'" @click="newDraft(0)">New blank draft</button>
      <button type="button" class="ghost small" :disabled="!!store.busy.value || live.active" :aria-busy="store.busy.value === 'Duplicating…'" @click="newDraft(current.version)">
        Duplicate Draft {{ current.version }}
      </button>
      <label class="compare">
        Compare with
        <select v-model.number="compareWith">
          <option :value="0">—</option>
          <option v-for="d in drafts.filter((x) => x.version !== currentVersion)" :key="d.version" :value="d.version">
            {{ draftLabel(store, d.version) }}
          </option>
        </select>
      </label>
    </div>

    <div v-if="live.error" class="fail" role="alert">
      <span class="state failed">{{ live.error.startsWith('Generation stopped') ? 'Stopped' : 'Generation failed' }}</span>
      <p class="msg">{{ live.error }}</p>
      <button type="button" class="ghost" @click="store.generate(live.from, live.intent, live.feedback)">
        {{ live.error.startsWith('Generation stopped') ? 'Start again' : 'Retry' }}
      </button>
    </div>
    <p v-if="live.notice" class="notice" role="status">{{ live.notice }}</p>

    <div v-if="!current && !live.active" class="empty">
      <template v-if="live.error">
        <strong>No draft yet</strong>
        <p>Retry above to have Claude write the first draft.</p>
      </template>
      <AiProgress v-else title="Preparing your first draft" detail="Saving your settings and connecting to Claude." />
    </div>

    <div v-else class="grid">
      <div class="main">
        <AiProgress
          v-if="showLive"
          :title="liveTitle"
          :detail="liveDetail"
          :stages="liveStages"
          :current="liveStage"
          :progress="(liveStage + 0.5) / liveStages.length"
          compact
        >
          <button type="button" class="ghost small" @click="store.stopGeneration">Stop</button>
        </AiProgress>

        <!-- Languages present in the audience -->
        <div v-if="!showLive && languages.length > 1" class="langs" role="tablist" aria-label="Language">
          <button
            v-for="l in languages"
            :key="l"
            type="button"
            role="tab"
            class="lang"
            :aria-selected="lang === l"
            @click="lang = l"
          >
            {{ languageName(l) }}
            <span class="count">{{ number.format(audience.languages?.[l] ?? 0) }}</span>
            <span v-if="langStatus(l) === 'base'" class="tag">base</span>
            <span v-else-if="langStatus(l) === 'stale'" class="tag warn">out of date</span>
            <span v-else-if="langStatus(l) === 'missing'" class="tag bad">missing</span>
          </button>
          <button
            v-if="needsTranslation && !approved"
            type="button"
            class="ghost small"
            :disabled="!!store.busy.value"
            :aria-busy="store.busy.value === 'Translating…'"
            @click="updateTranslations"
          >
            {{ store.busy.value === 'Translating…' ? 'Translating' : 'Update translations' }}
          </button>
        </div>
        <p v-if="translationIssue && !showLive" class="note" :class="{ danger: translationIssue.blocking }">{{ translationIssue.message }}</p>

        <!-- Channels -->
        <div class="channels" role="tablist" aria-label="Channel">
          <div v-for="ch in (['sms', 'email'] as const)" :key="ch" class="channel" :class="{ on: channel === ch }">
            <button type="button" role="tab" :aria-selected="channel === ch" class="channel-tab" @click="channel = ch">
              <strong>{{ ch === 'sms' ? 'SMS' : 'Email' }}</strong>
              <span>{{ number.format(ch === 'sms' ? audience.counts.sms_eligible : audience.counts.email_eligible) }} reachable</span>
              <span v-if="issuesFor(ch).some((i) => i.blocking)" class="dot" aria-label="has problems" />
            </button>
            <label class="toggle">
              <input v-if="current && !showLive" v-model="current[ch === 'sms' ? 'sms_enabled' : 'email_enabled']" type="checkbox" :disabled="readonly" />
              <input v-else type="checkbox" checked disabled />
              Send
            </label>
          </div>
        </div>

        <!-- Both editors stay mounted, so switching channels never loses edits. -->
        <template v-if="current && text && !showLive">
          <div v-show="channel === 'sms'" :class="{ disabled: !current.sms_enabled }">
            <ChannelEditor
              v-model:body="text.sms_body"
              channel="sms"
              :issues="issuesFor('sms')"
              :max-name-len="audience.max_first_name_len"
              :readonly="readonly"
            />
          </div>
          <div v-show="channel === 'email'" :class="{ disabled: !current.email_enabled }">
            <ChannelEditor
              v-model:body="text.email_body"
              v-model:subject="text.email_subject"
              channel="email"
              :issues="issuesFor('email')"
              :max-name-len="audience.max_first_name_len"
              :readonly="readonly"
            />
          </div>
        </template>
        <template v-else-if="showLive">
          <ChannelEditor v-if="channel === 'sms'" :body="liveText.sms_body" channel="sms" :issues="[]" :max-name-len="audience.max_first_name_len" readonly streaming />
          <ChannelEditor
            v-else
            :body="liveText.email_body"
            :subject="liveText.email_subject"
            channel="email"
            :issues="[]"
            :max-name-len="audience.max_first_name_len"
            readonly
            streaming
          />
        </template>

        <ul v-if="channelIssues.length" class="issues">
          <li v-for="i in channelIssues" :key="i.code + i.message">{{ i.message }}</li>
        </ul>

        <!-- Settings + regeneration -->
        <fieldset class="settings" :disabled="readonly">
          <legend>Settings</legend>
          <div class="selects">
            <label v-for="key in (['tone', 'occasion', 'length', 'cta'] as const)" :key="key">
              <span>{{ key === 'cta' ? 'Call to action' : label(key) }}</span>
              <select v-if="current && !showLive" v-model="current.settings[key]">
                <option v-for="o in options[key]" :key="o" :value="o">{{ label(o) }}</option>
              </select>
              <select v-else disabled>
                <option>{{ live.fields[key] ? label(live.fields[key]!) : '…' }}</option>
              </select>
            </label>
          </div>
          <template v-if="current && !approved">
            <label class="feedback">
              <span>Feedback for the next draft (optional)</span>
              <textarea v-model="feedback" rows="2" maxlength="1000" placeholder="e.g. Mention free shipping, make the email warmer…" />
            </label>
            <div class="regen">
              <p>Creates Draft {{ store.nextVersion.value }}; Draft {{ current.version }} is kept.</p>
              <button type="button" class="secondary" :disabled="live.active" :aria-busy="live.active" @click="regenerate">
                {{ live.active ? `Writing Draft ${store.nextVersion.value}` : `Regenerate as Draft ${store.nextVersion.value}` }}
              </button>
            </div>
          </template>
        </fieldset>
      </div>

      <aside class="side">
        <div v-if="compared && !showLive" class="compare-panel">
          <h3>{{ draftLabel(store, compared.version) }} <span>· {{ ORIGIN_LABELS[compared.origin] }}</span></h3>
          <p v-if="!comparedText" class="note">No {{ languageName(lang) }} version.</p>
          <template v-else-if="channel === 'sms'">
            <p class="bubble">{{ comparedText.sms_body || '—' }}</p>
          </template>
          <template v-else>
            <p class="subject">{{ comparedText.email_subject || '—' }}</p>
            <p class="body">{{ comparedText.email_body }}</p>
          </template>
          <div class="compare-actions">
            <button type="button" class="ghost small" @click="store.selectDraft(compared.version)">Edit this draft</button>
            <button type="button" class="link" @click="compareWith = 0">Close</button>
          </div>
        </div>
        <RecipientPreview
          v-else
          :audience="audience"
          :channel="channel"
          :language="showLive ? campaign.base_language : lang"
          :sms-body="(showLive ? liveText : text)?.sms_body ?? ''"
          :email-subject="(showLive ? liveText : text)?.email_subject ?? ''"
          :email-body="(showLive ? liveText : text)?.email_body ?? ''"
        />
      </aside>
    </div>

    </template>

    <StrategyPanel v-else-if="store.step.value === 'strategy'" />
    <ReviewPanel v-else />

    <nav v-if="drafts.length && !(approved && store.step.value === 'review')" class="step-nav" aria-label="Steps">
      <button v-if="store.step.value !== 'content'" type="button" class="ghost" @click="go(store.step.value === 'review' ? 'strategy' : 'content')">
        ← {{ store.step.value === 'review' ? 'Strategy' : 'Content' }}
      </button>
      <span />
      <button v-if="store.step.value !== 'review'" type="button" class="secondary" :disabled="live.active || !!store.busy.value" @click="go(store.step.value === 'content' ? 'strategy' : 'review')">
        Continue to {{ store.step.value === 'content' ? 'strategy' : 'review' }} →
      </button>
    </nav>
  </section>
</template>

<style scoped>
.studio {
  display: grid;
  gap: 14px;
}
button {
  font: inherit;
  cursor: pointer;
}
button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.drafts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.draft-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 14px;
  border: 1px solid var(--border);
  border-radius: 99px;
  background: var(--surface);
  color: var(--text-muted);
  font-weight: 600;
  transition: background-color 0.15s;
}
.draft-tab[aria-selected='true'] {
  color: var(--text);
  background: var(--selected-surface);
  border-color: var(--accent);
}
.draft-tab.live {
  color: var(--accent);
  border-color: transparent;
  background:
    linear-gradient(var(--selected-surface), var(--selected-surface)) padding-box,
    linear-gradient(90deg, #dde8b5, #204d3c, #dde8b5) border-box;
  background-size: 100% 100%, 200% 100%;
  animation: flow 1.6s linear infinite;
}
@keyframes flow {
  to {
    background-position: 0 0, -200% 0;
  }
}
.tag {
  padding: 0 7px;
  border-radius: 99px;
  font-size: 11px;
  font-weight: 600;
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.tag.ok {
  color: var(--success);
  background: var(--success-soft);
  border-color: transparent;
}
.tag.variant {
  color: var(--accent);
  background: var(--accent-soft);
  border-color: transparent;
}
.tag.warn {
  color: #8a5a00;
  background: #fff4dc;
  border-color: transparent;
}
.tag.bad {
  color: var(--danger);
  background: var(--danger-soft);
  border-color: transparent;
}
.save {
  margin-left: auto;
  font-size: 12px;
  color: var(--text-muted);
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.origin {
  margin-right: auto;
  font-size: 12px;
  color: var(--text-muted);
}
.compare {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted);
}
.danger {
  color: var(--danger);
}
.alert {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0;
  padding: 10px 14px;
  border: 1px solid var(--danger);
  border-radius: var(--radius-control);
  color: var(--danger);
  background: var(--danger-soft);
}
.notice,
.note {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.notice {
  padding: 8px 12px;
  border: 1px dashed var(--border);
  border-radius: var(--radius-control);
}
.empty {
  display: grid;
  gap: 4px;
  padding: 40px;
  text-align: center;
  color: var(--text-muted);
  border: 1px dashed var(--border);
  border-radius: var(--radius);
}
.empty p {
  margin: 0;
}
.empty:has(.ai-progress) {
  padding: 0;
  text-align: left;
  border: 0;
}
.grid {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}
.main {
  display: grid;
  gap: 12px;
  padding: 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow);
}
.side {
  position: sticky;
  top: 140px;
}
.compare-panel {
  display: grid;
  gap: 8px;
  padding: 16px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-muted);
}
.compare-panel h3 {
  margin: 0;
  font-size: 13px;
}
.compare-panel h3 span {
  font-weight: 400;
  color: var(--text-muted);
}
.bubble,
.body,
.subject {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.bubble,
.body {
  padding: 10px 12px;
  border-radius: var(--radius-control);
  background: var(--surface);
  border: 1px solid var(--border);
}
.subject {
  font-weight: 600;
}
.compare-actions {
  display: flex;
  justify-content: space-between;
}
.link {
  margin-left: 6px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent);
  font-weight: 600;
  text-decoration: underline;
}
.langs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.lang {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface-muted);
  color: var(--text-muted);
  font-weight: 600;
  font-size: 13px;
}
.lang[aria-selected='true'] {
  color: var(--text);
  border-color: var(--accent);
  background: var(--selected-surface);
}
.lang .count {
  font-weight: 400;
  font-size: 12px;
}
.channels {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.channel {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 4px 12px 4px 4px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface-muted);
}
.channel.on {
  border-color: var(--accent);
  background: var(--selected-surface);
}
.channel-tab {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 8px;
  padding: 8px 10px;
  border: 0;
  background: none;
  text-align: left;
}
.channel-tab span {
  font-size: 12px;
  color: var(--text-muted);
}
.channel-tab .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--danger);
}
.toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
}
.disabled {
  opacity: 0.55;
}
.issues {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--danger);
}
.settings {
  display: grid;
  gap: 10px;
  margin: 6px 0 0;
  padding: 14px 0 0;
  border: 0;
  border-top: 1px solid var(--border);
}
legend {
  padding: 0;
  font-weight: 600;
}
legend span {
  font-weight: 400;
  font-size: 12px;
  color: var(--text-muted);
}
.selects {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 10px;
}
.selects label,
.feedback {
  display: grid;
  gap: 4px;
  font-size: 12px;
  color: var(--text-muted);
}
select,
.feedback textarea {
  padding: 8px 10px;
  font: inherit;
  font-size: 14px;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}
.compare select {
  padding: 4px 8px;
  font-size: 13px;
}
.feedback textarea {
  resize: vertical;
}
.regen {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.regen p {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.secondary,
.ghost {
  padding: 9px 16px;
  border-radius: var(--radius-control);
  white-space: nowrap;
}
.small {
  padding: 5px 12px;
  font-size: 13px;
}
.secondary {
  border: 0;
  background: var(--accent);
  color: #fff;
  font-weight: 600;
}
.ghost {
  border: 1px solid var(--border);
  background: var(--surface);
  color: inherit;
}
.step-nav {
  position: sticky;
  bottom: 16px;
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow);
}
@media (max-width: 900px) {
  .grid {
    grid-template-columns: 1fr;
  }
  .side {
    position: static;
  }
}
@media (max-width: 720px) {
  .regen {
    flex-direction: column;
    align-items: stretch;
  }
  .channels {
    grid-template-columns: 1fr;
  }
}
</style>
