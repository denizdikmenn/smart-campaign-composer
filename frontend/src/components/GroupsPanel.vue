<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  formatTokens,
  formatUSD,
  generateGroups,
  groupExamples,
  groupsEstimate,
  contentLanguage,
  proposeGroups,
  render,
  saveGroup,
  type Estimate,
  type Group,
  type Member,
  type Version,
} from '../api'
import { useCampaign } from '../campaign'
import ChannelEditor from './ChannelEditor.vue'
import AiProgress from './AiProgress.vue'
import Avatar from './Avatar.vue'
import LangChip from './LangChip.vue'

const store = useCampaign()
const { view, campaign, approved } = store
const number = new Intl.NumberFormat('en-US')

const groups = computed(() => view.value.groups)
const note = computed(() => campaign.value.groups_note)
const running = computed(() => view.value.jobs.groups)
const total = computed(() => groups.value.reduce((n, g) => n + g.size, 0))
const generated = computed(() => groups.value.length > 0 && groups.value.every((g) => g.size === 0 || g.status === 'generated'))
const base = computed(() => store.drafts.value.find((d) => d.version === campaign.value.selected_version) ?? null)

async function propose() {
  if (groups.value.length && !confirm('Propose new groups? The current groups and their messages are replaced. Drafts are kept.')) return
  await store.act('Proposing groups…', async () => store.apply(await proposeGroups(store.id)), { refresh: false })
}

// Cost estimate before the marketer commits to generating group messages.
const estimate = ref<Estimate | null>(null)
watch(
  () => groups.value.map((g) => g.position + ':' + g.size).join(','),
  async () => {
    estimate.value = groups.value.length ? await groupsEstimate(store.id).catch(() => null) : null
  },
  { immediate: true },
)
async function generate() {
  await store.act('Starting…', () => generateGroups(store.id))
}

// Representative members per group.
const examples = reactive<Record<number, { label: string; member: Member }[] | 'loading'>>({})
async function toggleExamples(g: Group) {
  if (examples[g.position]) {
    delete examples[g.position]
    return
  }
  examples[g.position] = 'loading'
  try {
    examples[g.position] = await groupExamples(store.id, g.position)
  } catch {
    delete examples[g.position]
  }
}
/** What a member receives: group content in their language, else the base draft. */
function contentFor(g: Group, m: Member): { v: Version | null; lang: string; source: string } {
  const lang = contentLanguage(m.language)
  if (g.content[lang]) return { v: g.content[lang], lang, source: 'group message' }
  const b = base.value
  if (!b) return { v: null, lang, source: '' }
  const v = lang === b.language ? b : b.translations[lang]
  return { v: v ?? b, lang, source: 'base draft (group message not written yet)' }
}
const ago = (iso: string | null) => {
  if (!iso) return 'never'
  const d = Math.round((Date.now() - new Date(iso).getTime()) / 86400000)
  return d < 1 ? 'today' : d < 60 ? `${d}d ago` : d < 730 ? `${Math.round(d / 30)}mo ago` : `${Math.round(d / 365)}y ago`
}
const share = (g: Group) => (total.value ? Math.round((g.size / total.value) * 100) : 0)
const STATUS: Record<string, { label: string; cls: string }> = {
  proposed: { label: 'No messages yet', cls: 'todo' },
  generating: { label: 'Writing', cls: 'busy' },
  generated: { label: 'Messages ready', cls: 'done' },
  failed: { label: 'Failed', cls: 'failed' },
}

// Editing a group's messages (explicit save).
const editing = ref<number | null>(null)
const editLang = ref('')
const editChannel = ref<'sms' | 'email'>('sms')
const draft = ref<Record<string, Version>>({})
function edit(g: Group) {
  editing.value = g.position
  draft.value = JSON.parse(JSON.stringify(g.content))
  editLang.value = Object.keys(g.languages)[0] ?? campaign.value.base_language
}
async function saveEdit(g: Group) {
  await store.act('Saving…', async () => store.apply(await saveGroup(store.id, g.position, draft.value)), { refresh: false })
  if (!store.error.value) editing.value = null
}
const issuesFor = (g: Group, ch: 'sms' | 'email') =>
  g.issues.filter((i) => i.language === editLang.value && (ch === 'sms' ? i.field === 'sms_body' : i.field.startsWith('email_')))
</script>

<template>
  <div class="groups">
    <div class="row">
      <p class="muted">Every reachable customer lands in exactly one group.</p>
      <button v-if="!approved" type="button" class="btn" :disabled="!!store.busy.value || running" :aria-busy="store.busy.value === 'Proposing groups…'" @click="propose">
        {{ store.busy.value === 'Proposing groups…' ? 'Proposing' : groups.length ? 'Propose again' : 'Propose groups' }}
      </button>
    </div>

    <AiProgress
      v-if="store.busy.value === 'Proposing groups…'"
      title="Claude is proposing groups"
      detail="Splitting reachable customers by behaviour so every person lands in one group."
      compact
    />

    <div v-if="note" class="box" :class="{ warn: note.outcome === 'not_meaningful' }" role="status">
      <p>{{ note.message }}</p>
      <ul v-if="note.warnings.length" class="muted">
        <li v-for="w in note.warnings" :key="w">{{ w }}</li>
      </ul>
      <p v-if="note.outcome === 'not_meaningful'" class="muted">Choose “Single message” instead; nothing is lost.</p>
    </div>

    <ul v-if="groups.length" class="list">
      <li v-for="g in groups" :key="g.position" class="group" :class="g.status">
        <header class="g-head">
          <div class="g-title">
            <h3>{{ g.name }}</h3>
            <p class="muted">{{ g.description }}</p>
          </div>
          <div class="g-states">
            <span class="state" :class="STATUS[g.status]?.cls ?? 'todo'">{{ STATUS[g.status]?.label ?? g.status }}</span>
            <span v-if="g.stale" class="state dirty" title="The base draft changed after these messages were written">Base draft changed</span>
          </div>
        </header>

        <div class="g-size">
          <p>
            <strong>{{ number.format(g.size) }}</strong> people
            <span class="muted">· {{ share(g) }}% of reachable</span>
          </p>
          <div class="meter" aria-hidden="true"><span :style="{ width: `${share(g)}%` }" /></div>
        </div>

        <div class="g-meta">
          <LangChip v-for="(n, l) in g.languages" :key="l" :lang="String(l)" :count="n" />
          <span v-for="c in g.conditions_text" :key="c" class="rule">{{ c }}</span>
        </div>
        <!-- Why this group gets its own message: the angle planned with the groups, then how the message builds on it. -->
        <p v-if="g.status === 'generated' && g.rationale" class="why"><strong>Why this message:</strong> {{ g.rationale }}</p>
        <p v-else-if="g.position !== 0 && g.angle" class="why"><strong>Planned angle:</strong> {{ g.angle }}</p>
        <p v-else-if="g.position === 0" class="why muted">General message: these customers share no behavior beyond the audience, so nothing is assumed about them.</p>
        <div v-if="g.error" class="fail" role="alert"><span class="state failed">Failed</span><p class="msg">{{ g.error }}</p></div>

        <div v-if="g.status === 'generated' && editing !== g.position" class="messages">
          <div v-for="(v, l) in g.content" :key="l" class="message">
            <LangChip :lang="String(l)" plain />
            <p class="pre bubble">{{ v.sms_body }}</p>
          </div>
          <ul v-if="g.issues.length" class="issues">
            <li v-for="i in g.issues" :key="i.code + i.language + i.message" :class="{ 'error-text': i.blocking }">
              <LangChip v-if="i.language" :lang="i.language" short plain /> {{ i.message }}
            </li>
          </ul>
        </div>

        <div v-if="editing === g.position" class="editor">
          <div class="tabs">
            <span class="seg" role="group" aria-label="Language">
              <button v-for="l in Object.keys(draft)" :key="l" type="button" :aria-pressed="editLang === l" @click="editLang = l"><LangChip :lang="l" plain /></button>
            </span>
            <span class="seg" role="group" aria-label="Channel">
              <button type="button" :aria-pressed="editChannel === 'sms'" @click="editChannel = 'sms'">SMS</button>
              <button type="button" :aria-pressed="editChannel === 'email'" @click="editChannel = 'email'">Email</button>
            </span>
          </div>
          <template v-if="draft[editLang]">
            <ChannelEditor
              v-show="editChannel === 'sms'"
              v-model:body="draft[editLang]!.sms_body"
              channel="sms"
              :issues="issuesFor(g, 'sms')"
              :max-name-len="campaign.audience.max_first_name_len"
              :readonly="false"
            />
            <ChannelEditor
              v-show="editChannel === 'email'"
              v-model:body="draft[editLang]!.email_body"
              v-model:subject="draft[editLang]!.email_subject"
              channel="email"
              :issues="issuesFor(g, 'email')"
              :max-name-len="campaign.audience.max_first_name_len"
              :readonly="false"
            />
          </template>
          <div class="row">
            <button type="button" class="btn" @click="editing = null">Cancel</button>
            <button type="button" class="btn primary" :disabled="!!store.busy.value" :aria-busy="store.busy.value === 'Saving…'" @click="saveEdit(g)">Save group messages</button>
          </div>
        </div>

        <div class="actions">
          <button type="button" class="btn" :aria-expanded="!!examples[g.position]" @click="toggleExamples(g)">
            {{ examples[g.position] ? 'Hide typical customers' : 'Preview typical customers' }}
          </button>
          <button v-if="g.status === 'generated' && !approved && editing !== g.position" type="button" class="btn" @click="edit(g)">Edit messages</button>
        </div>

        <div v-if="examples[g.position] === 'loading'" class="people" aria-label="Loading preview" role="status">
          <div v-for="n in 2" :key="n" class="person">
            <div class="p-head"><span class="skeleton" style="width: 40px; height: 40px; border-radius: 50%" /><div style="flex: 1"><div class="skeleton-line short" /><div class="skeleton-line short" /></div></div>
            <div class="skeleton" style="height: 56px" />
          </div>
        </div>
        <ul v-else-if="examples[g.position]" class="people">
          <li v-for="ex in examples[g.position] as { label: string; member: Member }[]" :key="ex.member.id" class="person">
            <div class="p-head">
              <Avatar :name="ex.member.first_name" :size="40" />
              <div class="p-who">
                <strong>{{ ex.member.first_name }}</strong>
                <span class="muted">{{ ex.member.city }}</span>
              </div>
              <LangChip :lang="ex.member.language" />
            </div>

            <dl class="p-stats">
              <div><dt>Orders</dt><dd>{{ ex.member.order_count }}</dd></div>
              <div><dt>Spent</dt><dd>{{ number.format(Math.round(ex.member.total_spent)) }} TRY</dd></div>
              <div><dt>Last order</dt><dd>{{ ago(ex.member.last_order_at) }}</dd></div>
              <div><dt>Last cart</dt><dd>{{ ago(ex.member.last_cart_at) }}</dd></div>
            </dl>
            <p class="p-channels">
              <span class="ch" :class="{ off: !ex.member.sms_consent }">SMS</span>
              <span class="ch" :class="{ off: !ex.member.email_consent }">Email</span>
            </p>

            <div v-if="contentFor(g, ex.member).v" class="p-msg">
              <p class="receives">
                Receives <LangChip :lang="contentFor(g, ex.member).lang" plain />
                <span class="src" :class="{ fallback: contentFor(g, ex.member).source !== 'group message' }">
                  {{ contentFor(g, ex.member).source === 'group message' ? 'Group message' : 'Base draft · group message not written yet' }}
                </span>
              </p>
              <p class="pre bubble">{{ render(contentFor(g, ex.member).v!.sms_body, ex.member.first_name) }}</p>
              <p class="subject">
                <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="3.5" width="12" height="9" rx="1.5" /><path d="M2.5 4.5 8 8.5l5.5-4" /></svg>
                {{ render(contentFor(g, ex.member).v!.email_subject, ex.member.first_name) }}
              </p>
            </div>
          </li>
        </ul>
      </li>
    </ul>

    <div v-if="groups.length && !approved" class="box cost">
      <template v-if="running">
        <AiProgress
          title="Claude is writing group messages"
          :detail="`${groups.filter((g) => g.status === 'generated').length} of ${groups.filter((g) => g.size).length} groups done`"
          :stages="groups.filter((g) => g.size).map((g) => g.name)"
          :current="groups.filter((g) => g.size).findIndex((g) => g.status !== 'generated')"
          :progress="groups.filter((g) => g.size).length ? groups.filter((g) => g.status === 'generated').length / groups.filter((g) => g.size).length : 0"
          compact
        />
      </template>
      <template v-else>
        <p v-if="estimate">
          {{ estimate.calls }} AI calls · ~{{ formatTokens(estimate.input_tokens + estimate.output_tokens) }} tokens ·
          <strong>≈ {{ formatUSD(estimate.usd) }}</strong> <span class="muted">projected</span>
        </p>
        <button type="button" class="btn primary" :disabled="!!store.busy.value" @click="generate">
          {{ generated ? 'Regenerate all group messages' : 'Generate group messages' }}
        </button>
      </template>
    </div>
  </div>
</template>

<style scoped>
.groups {
  display: grid;
  gap: 12px;
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.box.warn {
  border-color: var(--danger);
}
.box ul {
  margin: 6px 0 0;
  padding-left: 18px;
}
.list,
.people {
  display: grid;
  gap: 12px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.group {
  display: grid;
  gap: 12px;
  padding: 16px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 2px 10px rgb(32 77 60 / 0.04);
  transition: box-shadow 0.2s, border-color 0.2s;
}
.group:hover {
  border-color: #cad7c4;
  box-shadow: var(--shadow);
}
.group.generated {
  border-left: 4px solid var(--success);
}
.group.failed {
  border-left: 4px solid var(--danger);
}
.g-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.g-title {
  display: grid;
  gap: 2px;
}
.g-title h3 {
  font-size: 16px;
}
.g-title .muted {
  font-size: 13px;
}
.g-states {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}
.g-size {
  display: grid;
  gap: 6px;
}
.g-size p {
  font-size: 13px;
}
.g-size strong {
  font-size: 18px;
}
.meter {
  height: 6px;
  overflow: hidden;
  border-radius: 99px;
  background: var(--surface-muted);
  box-shadow: inset 0 0 0 1px var(--border);
}
.meter span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, #9cbf86, var(--accent));
  transition: width 0.5s ease;
}
.g-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.rule {
  padding: 2px 9px;
  font-size: 12px;
  color: var(--accent);
  border-radius: 99px;
  background: var(--selected-surface);
}
.messages {
  display: grid;
  gap: 8px;
}
.message {
  display: grid;
  gap: 4px;
}
.why {
  margin: 0;
  padding: 8px 10px;
  font-size: 13px;
  border-left: 3px solid var(--accent-soft);
  background: var(--surface-muted);
  border-radius: 0 var(--radius-control) var(--radius-control) 0;
}
.issues {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
}
.editor {
  display: grid;
  gap: 8px;
}
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.seg {
  display: inline-flex;
  padding: 2px;
  border-radius: 99px;
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.seg button {
  padding: 4px 12px;
  font: inherit;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  border: 0;
  border-radius: 99px;
  background: none;
  cursor: pointer;
}
.seg button[aria-pressed='true'] {
  color: var(--accent);
  background: var(--accent-soft);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.bubble {
  justify-self: start;
  max-width: 100%;
  padding: 9px 12px;
  font-size: 13px;
  border-radius: 14px 14px 14px 4px;
  background: var(--selected-surface);
}

/* Typical customers */
.people {
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  animation: people-in 0.25s ease-out;
}
.person {
  display: grid;
  align-content: start;
  gap: 10px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-muted);
}
.p-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.p-who {
  display: grid;
  flex: 1;
  min-width: 0;
  line-height: 1.3;
}
.p-who strong {
  font-size: 15px;
}
.p-who .muted {
  font-size: 12px;
}
.p-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 6px;
  margin: 0;
}
.p-stats div {
  padding: 6px 8px;
  border-radius: var(--radius-control);
  background: var(--surface);
  border: 1px solid var(--border);
}
.p-stats dt {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}
.p-stats dd {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}
.p-channels {
  display: flex;
  gap: 6px;
}
.ch {
  padding: 1px 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--success);
  border-radius: 99px;
  background: var(--success-soft);
}
.ch::before {
  content: '✓ ';
}
.ch.off {
  color: var(--text-muted);
  background: var(--surface);
  border: 1px solid var(--border);
  text-decoration: line-through;
}
.ch.off::before {
  content: '';
}
.p-msg {
  display: grid;
  gap: 6px;
  padding-top: 10px;
  border-top: 1px dashed var(--border);
}
.receives {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted);
}
.src {
  padding: 1px 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--success);
  border-radius: 99px;
  background: var(--success-soft);
}
.src.fallback {
  color: #8a5a00;
  background: #fff4dc;
}
.p-msg .bubble {
  background: var(--surface);
  border: 1px solid var(--border);
}
.subject {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
}
.subject svg {
  flex: none;
  width: 16px;
  height: 16px;
  margin-top: 1px;
  fill: none;
  stroke: var(--text-muted);
  stroke-width: 1.5;
}
@keyframes people-in {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
}
@media (max-width: 560px) {
  .p-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .g-head {
    flex-direction: column;
  }
}
.cost {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
</style>
