<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import type { AudienceAlternative, PreviewCustomer } from '../api'

defineProps<{
  alternative: AudienceAlternative | null
  customers: PreviewCustomer[]
  loading: boolean
  error: string
  selected: boolean
}>()
const emit = defineEmits<{ close: []; select: [] }>()

const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'TRY', maximumFractionDigits: 0 })
const initials = (c: PreviewCustomer) => (c.first_name[0] ?? '') + (c.last_name[0] ?? '')
const onKey = (e: KeyboardEvent) => e.key === 'Escape' && emit('close')
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <Transition name="sheet">
      <div v-if="alternative" class="overlay" @click.self="$emit('close')">
        <aside class="sheet" role="dialog" aria-modal="true" :aria-label="`Preview of ${alternative.name}`">
          <header>
            <div>
              <p class="eyebrow">Customer preview</p>
              <h3>{{ alternative.name }}</h3>
              <p class="sub">A sample of {{ customers.length || 5 }} of {{ alternative.counts.total.toLocaleString() }} matching customers</p>
            </div>
            <button type="button" class="close" aria-label="Close preview" @click="$emit('close')">✕</button>
          </header>

          <ul class="conds">
            <li v-for="c in alternative.conditions_text" :key="c">{{ c }}</li>
          </ul>

          <div class="body">
            <template v-if="loading">
              <div v-for="n in 5" :key="n" class="skeleton" />
            </template>
            <p v-else-if="error" class="err" role="alert">{{ error }}</p>
            <p v-else-if="!customers.length" class="empty">No customers match these conditions.</p>
            <ul v-else class="list">
              <li v-for="c in customers" :key="c.email" class="person">
                <span class="avatar" aria-hidden="true">{{ initials(c) }}</span>
                <div class="info">
                  <strong>{{ c.first_name }} {{ c.last_name }}</strong>
                  <span class="muted">{{ c.city }} · {{ c.orders }} orders · {{ money.format(c.total_spent) }}</span>
                  <span class="muted">{{ c.email }}</span>
                </div>
                <div class="chan">
                  <span class="pill" :class="{ on: c.email_consent }">Email</span>
                  <span class="pill" :class="{ on: c.sms_consent }">SMS</span>
                </div>
              </li>
            </ul>
          </div>

          <footer>
            <button type="button" class="primary" :disabled="selected" @click="$emit('select'); $emit('close')">
              {{ selected ? 'Selected' : 'Select this audience' }}
            </button>
          </footer>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  justify-content: flex-end;
  background: rgb(0 0 0 / 0.45);
  backdrop-filter: blur(2px);
}
.sheet {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: min(440px, 100%);
  height: 100%;
  padding: 22px;
  overflow-y: auto;
  background: var(--surface);
  border-left: 1px solid var(--border);
  box-shadow: -12px 0 40px rgb(0 0 0 / 0.35);
  box-sizing: border-box;
}
header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.eyebrow {
  margin: 0;
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--accent);
}
h3 {
  margin: 2px 0;
  font-size: 18px;
}
.sub,
.muted {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.close {
  align-self: flex-start;
  width: 32px;
  height: 32px;
  color: inherit;
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: 8px;
  cursor: pointer;
}
.conds {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.conds li {
  padding: 2px 8px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-muted);
}
.body {
  flex: 1;
}
.list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.person {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: 12px;
  transition: border-color 0.15s, transform 0.15s;
}
.person:hover {
  border-color: var(--accent);
  transform: translateX(-2px);
}
.avatar {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  flex: none;
  font-size: 13px;
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 50%;
}
.info {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.info .muted {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chan {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.pill {
  padding: 1px 8px;
  font-size: 11px;
  text-align: center;
  color: var(--text-muted);
  border: 1px solid var(--border);
  border-radius: 999px;
  text-decoration: line-through;
}
.pill.on {
  color: var(--success);
  border-color: var(--success);
  text-decoration: none;
}
.skeleton {
  height: 64px;
  margin-bottom: 10px;
  border-radius: 12px;
  background: linear-gradient(90deg, var(--surface-muted), var(--border), var(--surface-muted));
  background-size: 200% 100%;
  animation: shimmer 1.4s infinite;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
.err {
  color: var(--danger);
}
.empty {
  color: var(--text-muted);
}
.primary {
  width: 100%;
  padding: 10px 16px;
  font: inherit;
  color: #fff;
  background: var(--accent);
  border: 0;
  border-radius: 8px;
  cursor: pointer;
}
.primary:disabled {
  opacity: 0.6;
  cursor: default;
}
.sheet-enter-active,
.sheet-leave-active {
  transition: opacity 0.2s;
}
.sheet-enter-active .sheet,
.sheet-leave-active .sheet {
  transition: transform 0.25s ease;
}
.sheet-enter-from,
.sheet-leave-to {
  opacity: 0;
}
.sheet-enter-from .sheet,
.sheet-leave-to .sheet {
  transform: translateX(100%);
}
</style>
