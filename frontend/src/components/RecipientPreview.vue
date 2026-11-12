<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { contentLanguage, MAX_SMS, MAX_SUBJECT, previewCustomers, render, type CampaignAudience, type PreviewCustomer } from '../api'
import Avatar from './Avatar.vue'
import LangChip from './LangChip.vue'

/** Messages rendered for a few real (synthetic) customers of the audience. */
const props = defineProps<{
  audience: CampaignAudience
  channel: 'sms' | 'email'
  /** Content language of the texts; customers who receive it are preferred. */
  language: string
  smsBody: string
  emailSubject: string
  emailBody: string
}>()

const customers = ref<PreviewCustomer[]>([])
const loading = ref(true)
const error = ref('')
let ctl: AbortController | null = null

async function load() {
  ctl?.abort()
  ctl = new AbortController()
  loading.value = true
  error.value = ''
  try {
    customers.value = await previewCustomers(props.audience, ctl.signal)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    error.value = e instanceof Error ? e.message : 'Could not load sample recipients'
  } finally {
    loading.value = false
  }
}
onMounted(load)
onUnmounted(() => ctl?.abort())

// Prefer people who actually receive this channel in this language.
const sample = computed(() => {
  const reach = (c: PreviewCustomer) => (props.channel === 'sms' ? c.sms_consent : c.email_consent)
  const lang = (c: PreviewCustomer) => contentLanguage(c.language) === props.language
  const ranked = [...customers.value].sort((a, b) => Number(lang(b)) * 2 + Number(reach(b)) - (Number(lang(a)) * 2 + Number(reach(a))))
  return ranked.slice(0, 3)
})
const len = (s: string) => [...s].length
const unresolved = (s: string) => /\{\{|\}\}/.test(s)
</script>

<template>
  <section class="preview" aria-labelledby="preview-title">
    <h3 id="preview-title">Recipient preview</h3>
    <p class="note"><LangChip :lang="language" plain /> · sample customers</p>
    <ul v-if="loading" class="list" aria-label="Loading sample recipients" role="status">
      <li v-for="n in 3" :key="n"><div class="skeleton-line short" /><div class="skeleton" style="height: 56px" /></li>
    </ul>
    <div v-else-if="error" class="fail" role="alert"><span class="state failed">Failed</span><p class="msg">Couldn’t load sample recipients.</p><button type="button" class="btn" @click="load">Retry</button></div>
    <p v-else-if="!sample.length" class="note">No customers to preview.</p>
    <ul v-else class="list">
      <li v-for="c in sample" :key="c.email">
        <div class="who">
          <Avatar :name="`${c.first_name} ${c.last_name}`" :size="32" />
          <div class="who-text">
            <strong>{{ c.first_name }} {{ c.last_name }}</strong>
            <span>{{ c.city }} · {{ channel === 'sms' ? (c.sms_consent ? 'SMS opted in' : 'no SMS consent') : (c.email_consent ? 'email opted in' : 'no email consent') }}</span>
          </div>
          <LangChip :lang="c.language" />
          <span v-if="contentLanguage(c.language) !== c.language" class="gets">gets <LangChip :lang="contentLanguage(c.language)" plain /></span>
        </div>
        <template v-if="channel === 'sms'">
          <p class="bubble">{{ render(smsBody, c.first_name) || '—' }}</p>
          <p class="meta" :class="{ over: len(render(smsBody, c.first_name)) > MAX_SMS }">
            {{ len(render(smsBody, c.first_name)) }} / {{ MAX_SMS }} characters
            <span v-if="unresolved(render(smsBody, c.first_name))" class="flag">· unresolved variable</span>
          </p>
        </template>
        <div v-else class="mail">
          <p class="subject" :class="{ over: len(render(emailSubject, c.first_name)) > MAX_SUBJECT }">
            {{ render(emailSubject, c.first_name) || '—' }}
          </p>
          <p class="body">{{ render(emailBody, c.first_name) }}</p>
          <p v-if="unresolved(render(emailSubject + emailBody, c.first_name))" class="meta flag">Unresolved variable</p>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.preview {
  display: grid;
  gap: 8px;
  align-content: start;
  padding: 16px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface-muted);
}
h3 {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}
.note,
.error {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.error {
  color: var(--danger);
}
.list {
  display: grid;
  gap: 14px;
  margin: 4px 0 0;
  padding: 0;
  list-style: none;
}
.who {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  margin: 0 0 8px;
  font-size: 13px;
}
.who-text {
  display: grid;
  flex: 1;
  min-width: 0;
  line-height: 1.3;
}
.who-text span,
.gets {
  color: var(--text-muted);
  font-size: 12px;
}
.bubble {
  margin: 0;
  padding: 10px 12px;
  border-radius: 14px 14px 14px 4px;
  background: var(--surface);
  border: 1px solid var(--border);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.mail {
  padding: 10px 12px;
  border-radius: var(--radius-control);
  background: var(--surface);
  border: 1px solid var(--border);
}
.subject {
  margin: 0 0 6px;
  font-weight: 600;
}
.body {
  margin: 0;
  max-height: 220px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 13px;
}
.meta {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}
.over,
.flag {
  color: var(--danger);
}
</style>
