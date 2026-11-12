<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import {
  getMessage,
  languageName,
  listMessages,
  retryMessage,
  type CampaignView,
  type DeliveryMessage,
  type MessageDetail,
  type MessageFilter,
  type MessagePage,
} from '../api'

/**
 * Recipient-level deliveries: a filtered, paged table with each message's
 * attempt history and a Retry action for temporary failures. It reloads
 * whenever the parent polls (tick) or a filter changes.
 */
const props = defineProps<{ view: CampaignView; tick: number }>()
const status = defineModel<MessageFilter>('status', { required: true })
const emit = defineEmits<{ retried: [] }>()
const id = props.view.campaign.id
const number = new Intl.NumberFormat('en-US')

const channel = ref<'' | 'sms' | 'email'>('')
const page = ref(1)
const data = ref<MessagePage | null>(null)
const error = ref('')
const open = ref<number | null>(null)
const detail = ref<MessageDetail | null>(null)
const notice = ref<{ id: number; text: string; bad: boolean } | null>(null)
const busy = ref<number | null>(null)

let ctl: AbortController | null = null
async function load() {
  ctl?.abort()
  ctl = new AbortController()
  try {
    data.value = await listMessages(id, { status: status.value, channel: channel.value, page: page.value }, ctl.signal)
    error.value = ''
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') return
    error.value = e instanceof Error ? e.message : 'Could not load messages'
  }
  if (open.value !== null) await loadDetail(open.value)
}
async function loadDetail(messageId: number) {
  try {
    detail.value = await getMessage(id, messageId)
  } catch {
    // the row stays usable; history shows on the next refresh
  }
}

watch([status, channel], () => {
  page.value = 1
  void load()
})
watch(page, () => void load())
watch(() => props.tick, () => void load(), { immediate: true })
onUnmounted(() => ctl?.abort())

async function toggle(m: DeliveryMessage) {
  if (open.value === m.id) {
    open.value = null
    detail.value = null
    return
  }
  open.value = m.id
  detail.value = null
  await loadDetail(m.id)
}

async function retry(m: DeliveryMessage) {
  busy.value = m.id
  try {
    await retryMessage(id, m.id)
    notice.value = { id: m.id, text: 'Queued again. It gets a new round of up to 4 attempts.', bad: false }
    emit('retried')
    await load()
  } catch (e) {
    notice.value = { id: m.id, text: e instanceof Error ? e.message : 'Retry failed', bad: true }
  } finally {
    busy.value = null
  }
}

const FILTERS: { value: MessageFilter; label: string }[] = [
  { value: '', label: 'All' },
  { value: 'pending', label: 'Queued' },
  { value: 'retrying', label: 'Retrying' },
  { value: 'failed', label: 'Failed' },
  { value: 'skipped', label: 'Skipped' },
  { value: 'sent', label: 'Sent' },
]
const TONE: Record<string, string> = { sent: 'ok', failed: 'bad', retrying: 'accent', sending: 'accent' }
const time = (iso: string | null) => (iso ? new Date(iso).toLocaleTimeString('en-GB') : '')
const origin = (m: DeliveryMessage) => {
  const parts: string[] = []
  if (m.variant_position) parts.push(`Variant ${props.view.campaign.variants.find((v) => v.position === m.variant_position)?.label ?? m.variant_position}`)
  if (m.group_position !== null) parts.push(props.view.groups.find((g) => g.position === m.group_position)?.name ?? `Group ${m.group_position}`)
  parts.push(languageName(m.language))
  return parts.join(' · ')
}
const pages = () => (data.value ? Math.max(1, Math.ceil(data.value.total / data.value.page_size)) : 1)
</script>

<template>
  <div class="messages">
    <div class="bar">
      <h3>Messages</h3>
      <div class="filters" role="group" aria-label="Filter by status">
        <button
          v-for="f in FILTERS"
          :key="f.value"
          type="button"
          class="chip"
          :class="{ accent: status === f.value }"
          :aria-pressed="status === f.value"
          @click="status = f.value"
        >
          {{ f.label }}
        </button>
      </div>
      <select v-model="channel" aria-label="Channel">
        <option value="">All channels</option>
        <option value="sms">SMS</option>
        <option value="email">Email</option>
      </select>
    </div>
    <p v-if="error" class="error-text" role="alert">{{ error }}</p>

    <table v-if="data && data.items.length" class="table">
      <thead>
        <tr>
          <th>Recipient</th>
          <th>Channel</th>
          <th>Status</th>
          <th>Attempts</th>
          <th>Last error</th>
          <th><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="m in data.items" :key="m.id">
          <tr :class="{ opened: open === m.id }">
            <td>
              <strong>{{ m.name }}</strong>
              <span class="muted block">#{{ m.customer_id }} · {{ m.to }} · {{ origin(m) }}</span>
            </td>
            <td>{{ m.channel === 'sms' ? 'SMS' : 'Email' }}</td>
            <td>
              <span class="chip" :class="TONE[m.status]">{{ m.status }}</span>
              <span v-if="m.permanent" class="muted block">permanent</span>
              <span v-else-if="m.next_attempt_at" class="muted block">next {{ time(m.next_attempt_at) }}</span>
            </td>
            <td>{{ m.attempts }}</td>
            <td class="err">{{ m.last_error ?? '–' }}</td>
            <td class="act">
              <button type="button" class="btn" :aria-expanded="open === m.id" @click="toggle(m)">History</button>
              <button v-if="m.retryable" type="button" class="btn primary" :disabled="busy === m.id" @click="retry(m)">Retry</button>
            </td>
          </tr>
          <tr v-if="notice?.id === m.id" class="sub">
            <td colspan="6" :class="notice.bad ? 'error-text' : 'muted'" role="status">{{ notice.text }}</td>
          </tr>
          <tr v-if="open === m.id" class="sub">
            <td colspan="6">
              <div v-if="!detail" role="status" aria-label="Loading history"><div class="skeleton-line short" /><div class="skeleton-line" /><div class="skeleton-line" /></div>
              <template v-else>
                <p v-if="!detail.attempts_history.length" class="muted">
                  No provider calls{{ detail.status === 'skipped' ? ': skipped before sending.' : ' yet.' }}
                </p>
                <table v-else class="table history">
                  <thead>
                    <tr>
                      <th>#</th>
                      <th>Time</th>
                      <th>Latency</th>
                      <th>Response</th>
                      <th>Outcome</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="(a, i) in detail.attempts_history" :key="i">
                      <td>{{ a.attempt }} <span v-if="a.manual" class="chip">manual retry</span></td>
                      <td>{{ time(a.started_at) }}</td>
                      <td>{{ number.format(a.latency_ms) }} ms</td>
                      <td>{{ a.http_status ?? 'no response' }} {{ a.code ?? '' }}</td>
                      <td>
                        <span class="chip" :class="TONE[a.outcome]">{{ a.outcome }}</span>
                        <span v-if="a.error" class="muted"> {{ a.error }}</span>
                        <span v-if="a.message_id" class="muted"> {{ a.message_id }}</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p class="muted preview">
                  <strong v-if="detail.subject">{{ detail.subject }} · </strong>{{ detail.body || 'No message: the recipient was not eligible.' }}
                </p>
              </template>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
    <p v-else-if="data" class="muted">No messages match this filter.</p>

    <div v-if="data && data.total > data.page_size" class="pager">
      <button type="button" class="btn" :disabled="page <= 1" @click="page--">← Previous</button>
      <span class="muted">
        {{ number.format((data.page - 1) * data.page_size + 1) }}–{{ number.format(Math.min(data.page * data.page_size, data.total)) }}
        of {{ number.format(data.total) }}
      </span>
      <button type="button" class="btn" :disabled="page >= pages()" @click="page++">Next →</button>
    </div>
  </div>
</template>

<style scoped>
.messages {
  display: grid;
  gap: 10px;
}
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
}
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  flex: 1;
}
.filters .chip {
  font: inherit;
  font-size: 12px;
  cursor: pointer;
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
  vertical-align: top;
  border-bottom: 1px solid var(--border);
}
.table th {
  font-size: 12px;
  color: var(--text-muted);
  font-weight: 600;
}
.block {
  display: block;
}
.err {
  max-width: 320px;
  overflow-wrap: anywhere;
}
.act {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
}
.act .btn {
  padding: 4px 10px;
}
tr.opened td,
tr.sub td {
  background: var(--surface-muted);
}
.history {
  font-size: 12px;
}
.preview {
  margin-top: 6px;
  white-space: pre-wrap;
}
.pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
@media (max-width: 720px) {
  .table {
    display: block;
    overflow-x: auto;
  }
}
</style>
