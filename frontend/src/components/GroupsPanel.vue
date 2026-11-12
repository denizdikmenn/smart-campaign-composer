<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  formatTokens,
  formatUSD,
  generateGroups,
  groupExamples,
  groupsEstimate,
  languageName,
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
const days = (iso: string | null) => (iso ? `${Math.round((Date.now() - new Date(iso).getTime()) / 86400000)} days ago` : 'never')

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
      <li v-for="g in groups" :key="g.position" class="group">
        <div class="head">
          <strong>{{ g.name }}</strong>
          <span class="chip">{{ number.format(g.size) }} · {{ total ? Math.round((g.size / total) * 100) : 0 }}%</span>
          <span v-for="(n, l) in g.languages" :key="l" class="chip">{{ languageName(String(l)) }} {{ n }}</span>
          <span class="chip" :class="{ ok: g.status === 'generated', bad: g.status === 'failed', accent: g.status === 'generating' }">
            {{ g.status === 'proposed' ? 'no messages yet' : g.status }}
          </span>
          <span v-if="g.stale" class="chip bad" title="The base draft changed after these messages were written">base changed</span>
        </div>
        <p class="muted">{{ g.description }}</p>
        <p class="muted rules">{{ g.conditions_text.join(' · ') }}</p>
        <p v-if="g.error" class="error-text"><span class="state failed">Failed</span> {{ g.error }}</p>

        <div v-if="g.status === 'generated' && editing !== g.position" class="messages">
          <p v-for="(v, l) in g.content" :key="l" class="pre">
            <span class="chip">{{ String(l).toUpperCase() }}</span> {{ v.sms_body }}
          </p>
          <ul v-if="g.issues.length" class="issues">
            <li v-for="i in g.issues" :key="i.code + i.language + i.message" :class="{ 'error-text': i.blocking }">{{ i.language?.toUpperCase() }}: {{ i.message }}</li>
          </ul>
        </div>

        <div v-if="editing === g.position" class="editor">
          <div class="tabs">
            <button v-for="l in Object.keys(draft)" :key="l" type="button" class="btn" :class="{ cta: editLang === l }" @click="editLang = l">{{ languageName(l) }}</button>
            <button type="button" class="btn" :class="{ cta: editChannel === 'sms' }" @click="editChannel = 'sms'">SMS</button>
            <button type="button" class="btn" :class="{ cta: editChannel === 'email' }" @click="editChannel = 'email'">Email</button>
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
            <button type="button" class="btn primary" :disabled="!!store.busy.value" @click="saveEdit(g)">Save group messages</button>
          </div>
        </div>

        <div class="row">
          <button type="button" class="btn" @click="toggleExamples(g)">{{ examples[g.position] ? 'Hide preview' : 'Preview a typical customer' }}</button>
          <button v-if="g.status === 'generated' && !approved && editing !== g.position" type="button" class="btn" @click="edit(g)">Edit messages</button>
        </div>
        <div v-if="examples[g.position] === 'loading'" class="examples" aria-label="Loading preview" role="status">
          <div v-for="n in 2" :key="n" class="box"><div class="skeleton-line short" /><div class="skeleton-line" /><div class="skeleton-line" /></div>
        </div>
        <ul v-else-if="examples[g.position]" class="examples">
          <li v-for="ex in examples[g.position] as { label: string; member: Member }[]" :key="ex.member.id" class="box">
            <p>
              <strong>{{ ex.member.first_name }}</strong> · {{ ex.member.city }} · speaks {{ ex.member.language }}
            </p>
            <p class="muted">
              {{ ex.member.order_count }} orders · {{ number.format(Math.round(ex.member.total_spent)) }} TRY spent · last order
              {{ days(ex.member.last_order_at) }} · last cart {{ days(ex.member.last_cart_at) }} ·
              {{ [ex.member.email_consent && 'email', ex.member.sms_consent && 'SMS'].filter(Boolean).join(' + ') }}
            </p>
            <template v-if="contentFor(g, ex.member).v">
              <p class="muted">{{ languageName(contentFor(g, ex.member).lang) }} · {{ contentFor(g, ex.member).source }}</p>
              <p class="pre bubble">{{ render(contentFor(g, ex.member).v!.sms_body, ex.member.first_name) }}</p>
              <p class="pre"><strong>{{ render(contentFor(g, ex.member).v!.email_subject, ex.member.first_name) }}</strong></p>
            </template>
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
.examples {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.status {
  display: flex;
  align-items: center;
  gap: 8px;
}
.group {
  display: grid;
  gap: 6px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.rules {
  font-style: italic;
}
.messages {
  display: grid;
  gap: 4px;
  font-size: 13px;
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
  gap: 6px;
}
.bubble {
  padding: 8px 10px;
  border-radius: 12px 12px 12px 4px;
  background: var(--surface);
  border: 1px solid var(--border);
  font-size: 13px;
}
.cost {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
</style>
