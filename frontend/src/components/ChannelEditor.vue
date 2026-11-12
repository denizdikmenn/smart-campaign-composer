<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { FIRST_NAME, MAX_SMS, MAX_SUBJECT, personalizedLength, type Issue } from '../api'

/**
 * Editable template for one channel. Counters update locally on every
 * keystroke; issues come from the backend (authoritative) after each save.
 */
const props = defineProps<{
  channel: 'sms' | 'email'
  issues: Issue[]
  /** Longest first name among reachable audience members. */
  maxNameLen: number
  readonly: boolean
  /** Text is still being written by the model. */
  streaming?: boolean
}>()
const body = defineModel<string>('body', { required: true })
const subject = defineModel<string>('subject', { default: '' })

const len = (s: string) => [...s].length
const nameLen = computed(() => props.maxNameLen || 15)
const smsWorst = computed(() => personalizedLength(body.value, nameLen.value))
const subjectWorst = computed(() => personalizedLength(subject.value, nameLen.value))
const usesName = (s: string) => s.includes(FIRST_NAME)
const words = computed(() => body.value.trim().split(/\s+/).filter(Boolean).length)

const fieldIssues = (field: string) => props.issues.filter((i) => i.field === field)
const bodyField = computed(() => (props.channel === 'sms' ? 'sms_body' : 'email_body'))

// While streaming, keep the newest text in view.
const bodyEl = ref<HTMLTextAreaElement | null>(null)
watch(body, async () => {
  if (!props.streaming) return
  await nextTick()
  if (bodyEl.value) bodyEl.value.scrollTop = bodyEl.value.scrollHeight
})
</script>

<template>
  <div class="editor" :class="{ streaming }">
    <template v-if="channel === 'email'">
      <label class="label" for="email-subject">Subject</label>
      <input
        id="email-subject"
        v-model="subject"
        class="field"
        type="text"
        :readonly="readonly"
        :placeholder="streaming ? 'Waiting for Claude…' : undefined"
        :aria-invalid="fieldIssues('email_subject').some((i) => i.blocking)"
        aria-describedby="email-subject-count"
      />
      <p id="email-subject-count" class="count" :class="{ over: subjectWorst > MAX_SUBJECT }">
        {{ len(subject) }} / {{ MAX_SUBJECT }}
        <template v-if="usesName(subject)"> · up to {{ subjectWorst }} with the longest name</template>
      </p>
      <ul v-if="fieldIssues('email_subject').length" class="issues">
        <li v-for="i in fieldIssues('email_subject')" :key="i.code + i.message" :class="{ warn: !i.blocking }">{{ i.message }}</li>
      </ul>
    </template>

    <label class="label" :for="`${channel}-body`">{{ channel === 'sms' ? 'SMS message' : 'Email body' }}</label>
    <textarea
      :id="`${channel}-body`"
      ref="bodyEl"
      v-model="body"
      class="field"
      :rows="channel === 'sms' ? 4 : 12"
      :readonly="readonly"
      :placeholder="streaming ? 'Waiting for Claude…' : undefined"
      :aria-busy="streaming || undefined"
      :aria-invalid="fieldIssues(bodyField).some((i) => i.blocking)"
      :aria-describedby="`${channel}-body-count`"
    />
    <p v-if="channel === 'sms'" :id="`${channel}-body-count`" class="count" :class="{ over: smsWorst > MAX_SMS }">
      {{ len(body) }} characters as written ·
      <strong>{{ smsWorst }} / {{ MAX_SMS }}</strong> for the longest first name ({{ nameLen }} letters)
    </p>
    <p v-else :id="`${channel}-body-count`" class="count">{{ words }} words · plain text</p>
    <ul v-if="fieldIssues(bodyField).length" class="issues">
      <li v-for="i in fieldIssues(bodyField)" :key="i.code + i.message" :class="{ warn: !i.blocking }">{{ i.message }}</li>
    </ul>
    <p v-if="!readonly" class="hint">Variable: <code>{{ FIRST_NAME }}</code></p>
  </div>
</template>

<style scoped>
.editor {
  display: grid;
  gap: 6px;
}
.label {
  margin-top: 6px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-muted);
}
.field {
  width: 100%;
  padding: 10px 12px;
  font: inherit;
  color: inherit;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  resize: vertical;
}
.field:read-only {
  background: var(--surface-muted);
}
.field[aria-invalid='true'] {
  border-color: var(--danger);
}
/* Being written: soft breathing glow; empty fields shimmer until text arrives. */
.streaming .field {
  border-color: #b9cc86;
  background: var(--surface);
  animation: writing 1.8s ease-in-out infinite;
}
.streaming .field:placeholder-shown {
  background: linear-gradient(90deg, var(--surface-muted) 25%, var(--selected-surface) 50%, var(--surface-muted) 75%);
  background-size: 200% 100%;
  animation:
    writing 1.8s ease-in-out infinite,
    shimmer 1.4s linear infinite;
}
@keyframes writing {
  50% {
    box-shadow: 0 0 0 4px rgb(221 232 181 / 0.6);
  }
}
.count,
.hint {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}
.count.over,
.count.over strong {
  color: var(--danger);
}
.hint code {
  padding: 0 4px;
  border-radius: 4px;
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.issues {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--danger);
}
.issues .warn {
  color: var(--text-muted);
}
@media (max-width: 720px) {
  .field {
    font-size: 16px;
  }
}
</style>
